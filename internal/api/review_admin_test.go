// Independent admin acceptance probes for revision 9f80afa.
// Loaded with Go -overlay; never contacts production services or databases.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"yzapi/internal/config"
	"yzapi/internal/crypto"
	"yzapi/internal/gateway"
	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

func auditServer(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	db, e := gorm.Open(sqlite.Open(filepath.Join(dir, "test.db")), &gorm.Config{Logger: logger.Discard})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := db.DB()
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	if e = db.AutoMigrate(model.All()...); e != nil {
		t.Fatal(e)
	}
	st, e := settings.New(db)
	if e != nil {
		t.Fatal(e)
	}
	ci, _ := crypto.NewCipher(make([]byte, 32))
	cfg := &config.Config{DataDir: dir, JWTSecret: "local-review-only"}
	ls, e := logstore.New(db, dir, func() int { return 30 })
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ls.Close(context.Background()) })
	gw, e := gateway.New(cfg, db, ci, st, ls)
	if e != nil {
		t.Fatal(e)
	}
	srv, e := New(cfg, db, gw, st, ci, Engines{}, "review")
	if e != nil {
		t.Fatal(e)
	}
	return srv
}
func auditCall(fn gin.HandlerFunc, actor *model.User, id uint, body any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	b, _ := json.Marshal(body)
	c.Request = httptest.NewRequest("POST", "/review", strings.NewReader(string(b)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}}
	if actor != nil {
		c.Set(ctxUser, actor)
	}
	fn(c)
	return w
}
func auditUser(s *Server, name string) *model.User {
	u := &model.User{Username: name, Enabled: true, Role: model.RoleAdmin}
	s.db.Create(u)
	return u
}
func TestAuditLogoutCacheRefill(t *testing.T) {
	s := auditServer(t)
	u := auditUser(s, "admin")
	entered := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	s.db.Callback().Query().After("gorm:after_query").Register("review_user_load", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			if _, ok := tx.Statement.Dest.(*model.User); ok {
				once.Do(func() { close(entered); <-resume })
			}
		}
	})
	done := make(chan struct{})
	go func() { s.auth.user(&claims{UID: u.ID, Ver: 0}); close(done) }()
	<-entered
	w := auditCall(s.logout, u, 0, map[string]any{})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	close(resume)
	<-done
	s.db.Callback().Query().Remove("review_user_load")
	if _, err := s.auth.user(&claims{UID: u.ID, Ver: 0}); err == nil {
		t.Fatal("fresh authorization after completed logout accepted revoked token after stale cache refill")
	}
}
func TestAuditConcurrentLastAdmin(t *testing.T) {
	s := auditServer(t)
	a := auditUser(s, "admin-a")
	b := auditUser(s, "admin-b")
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	s.db.Callback().Query().After("gorm:after_query").Register("review_count", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			if _, ok := tx.Statement.Dest.(*int64); ok {
				ready <- struct{}{}
				<-release
			}
		}
	})
	done := make(chan int, 2)
	go func() { done <- auditCall(s.setUserEnabled, a, b.ID, map[string]any{"enabled": false}).Code }()
	go func() { done <- auditCall(s.setUserEnabled, b, a.ID, map[string]any{"enabled": false}).Code }()
	// The first request must reach its count; the second may never get there while the
	// first holds the admin guard (that serialisation is the fix), so only wait briefly.
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("count barrier timeout")
	}
	select {
	case <-ready:
		t.Log("both counts ran concurrently (no guard)")
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	c1, c2 := <-done, <-done
	s.db.Callback().Query().Remove("review_count")
	n := s.adminCount(s.db)
	if n == 0 {
		t.Fatalf("both admins disabled each other: responses=%d/%d remaining enabled admins=%d", c1, c2, n)
	}
	if (c1 == 409) == (c2 == 409) {
		t.Fatalf("exactly one request must be refused: responses=%d/%d", c1, c2)
	}
}
func TestAuditConcurrentFailedLogins(t *testing.T) {
	s := auditServer(t)
	h, _ := crypto.HashPassword("RightPass123")
	u := &model.User{Username: "loginuser", Enabled: true, Role: model.RoleUser, PasswordHash: h, FailedLogins: 3}
	s.db.Create(u)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	s.db.Callback().Query().After("gorm:after_query").Register("review_login_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			if _, ok := tx.Statement.Dest.(*model.User); ok {
				ready <- struct{}{}
				<-release
			}
		}
	})
	done := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- auditCall(s.login, nil, 0, map[string]any{"username": "loginuser", "password": "WrongPass123"}).Code
		}()
	}
	for i := 0; i < 2; i++ {
		<-ready
	}
	close(release)
	<-done
	<-done
	s.db.Callback().Query().Remove("review_login_read")
	s.db.First(u, u.ID)
	if !u.Locked || u.FailedLogins < 5 {
		t.Fatalf("3 failures + 2 concurrent failures => failed_logins=%d locked=%v", u.FailedLogins, u.Locked)
	}
}
func TestAuditDeleteModelGroupSettingFailure(t *testing.T) {
	s := auditServer(t)
	mg := model.ModelGroup{Name: "group", Type: model.TypeText, Models: model.StringList{"m"}}
	s.db.Create(&mg)
	sr := s.st.Get().SmartRoute
	sr.Enabled = false
	sr.SimpleGroupID = mg.ID
	s.st.SetSmartRoute(sr)
	s.gw.Reload()
	inject := func(tx *gorm.DB) {
		if tx.Statement.Table == "settings" {
			tx.AddError(errors.New("injected settings write failure"))
		}
	}
	s.db.Callback().Update().Before("gorm:update").Register("review_setting_failure", inject)
	s.db.Callback().Create().Before("gorm:create").Register("review_setting_failure", inject)
	w := auditCall(s.deleteModelGroup, nil, mg.ID, nil)
	var n int64
	s.db.Model(&model.ModelGroup{}).Where("id = ?", mg.ID).Count(&n)
	if w.Code != 500 {
		t.Fatalf("expected injected settings failure, got status=%d body=%s", w.Code, w.Body.String())
	}
	if n != 1 || s.st.Get().SmartRoute.SimpleGroupID != mg.ID {
		t.Fatalf("delete returned 500 but rollback incomplete: group count=%d settings reference=%d", n, s.st.Get().SmartRoute.SimpleGroupID)
	}
}
func TestAuditBaseURLChangeResetsCooldown(t *testing.T) {
	s := auditServer(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"expired"}}`))
	}))
	defer bad.Close()
	var goodCalls atomic.Int64
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer good.Close()
	enc, _ := s.cipher.Encrypt("mock-key")
	acc := model.Account{Name: "account", Provider: "custom", Type: model.TypeText, BaseURL: bad.URL, APIKeyEnc: enc, Protocols: model.StringList{model.ProtoOpenAIChat}, Enabled: true, Mappings: []model.ModelMapping{{RequestModel: "m", UpstreamModel: "m"}}}
	s.db.Create(&acc)
	grp := model.UserGroup{Name: "default", Enabled: true, IsDefault: true}
	s.db.Create(&grp)
	u := model.User{Username: "caller", GroupID: grp.ID, Enabled: true, Role: model.RoleUser}
	s.db.Create(&u)
	key, hash, _ := crypto.GenerateAPIKey()
	s.db.Create(&model.APIKey{UserID: u.ID, KeyHash: hash, Enabled: true})
	s.gw.Reload()
	call := func() int {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.gw.HandleChat(w, r)
		return w.Code
	}
	if call() == 200 {
		t.Fatal("bad upstream should fail")
	}
	w := auditCall(s.updateAccount, nil, acc.ID, map[string]any{"name": "account", "provider": "custom", "type": "text", "base_url": good.URL, "api_key": "******", "protocols": []string{model.ProtoOpenAIChat}, "mappings": []map[string]string{{"request_model": "m", "upstream_model": "m"}}, "skip_test": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	status := call()
	if status != 200 || goodCalls.Load() == 0 {
		t.Fatalf("base URL successfully changed but new upstream blocked by previous cooldown: status=%d new upstream calls=%d", status, goodCalls.Load())
	}
}
func TestAuditNegativeNewPerformanceFields(t *testing.T) {
	s := auditServer(t)
	p := s.st.Get().Performance
	p.MaxBodyMemoryMB = -1
	p.VectorMaxConcurrency = -1
	p.VectorTimeoutSec = -1
	w := auditCall(s.putPerformance, nil, 0, p)
	if w.Code == 200 {
		t.Fatalf("negative resource limits accepted and saved: %+v", s.st.Get().Performance)
	}
}

type auditRouteEngine struct{ builds int }

func (e *auditRouteEngine) Reload() error { return nil }
func (e *auditRouteEngine) BuildVectors(context.Context, []uint) (int, int, error) {
	e.builds++
	return 1, 0, nil
}
func (e *auditRouteEngine) Preview(context.Context, string, int) (PreviewResult, error) {
	return PreviewResult{}, nil
}
func TestAuditRouteSampleEditBuild(t *testing.T) {
	s := auditServer(t)
	engine := &auditRouteEngine{}
	s.eng.Route = engine
	x := model.RouteSample{Label: "simple", Text: "old", VectorDim: 2}
	s.db.Create(&x)
	w := auditCall(s.updateRouteSample, nil, x.ID, map[string]any{"label": "simple", "text": "new", "build_vector": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if engine.builds != 1 {
		t.Fatalf("edited text with build_vector=true but BuildVectors calls=%d", engine.builds)
	}
}

// Creating an account on a provider's Anthropic-compatible endpoint must default to
// that endpoint's base URL and protocol set, never the OpenAI ones.
func TestAccountTypeNarrowsProtocols(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "admin-p")
	w := auditCall(s.createAccount, admin, 0, map[string]any{"name": "ds-claude", "provider": "deepseek", "account_type": "anthropic",
		"type": "text", "api_key": "sk-x", "mappings": []map[string]string{{"request_model": "deepseek-chat", "upstream_model": "deepseek-chat"}}, "skip_test": true})
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		BaseURL   string   `json:"base_url"`
		Protocols []string `json:"protocols"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.BaseURL != "https://api.deepseek.com/anthropic" || len(out.Protocols) != 1 || out.Protocols[0] != model.ProtoAnthropicMessages {
		t.Fatalf("anthropic account type: base=%s protocols=%v", out.BaseURL, out.Protocols)
	}
	// Explicitly asking for the OpenAI protocol on the Anthropic endpoint is ignored.
	w = auditCall(s.createAccount, admin, 0, map[string]any{"name": "ds-claude-2", "provider": "deepseek", "account_type": "anthropic",
		"type": "text", "api_key": "sk-x", "protocols": []string{model.ProtoOpenAIChat}, "mappings": []map[string]string{{"request_model": "m", "upstream_model": "m"}}, "skip_test": true})
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Protocols) != 1 || out.Protocols[0] != model.ProtoAnthropicMessages {
		t.Fatalf("protocol override must be dropped: %d %v", w.Code, out.Protocols)
	}
}
