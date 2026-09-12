package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/crypto"
	"yzapi/internal/gateway"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

type r132GenRouter struct {
	mu   sync.Mutex
	cfgs []settings.SmartRoute
}

func (r *r132GenRouter) Decide(context.Context, string, string, int) gateway.RouteResult {
	return gateway.RouteResult{Label: "simple", Source: "fallback"}
}
func (r *r132GenRouter) DecideWith(_ context.Context, _, _ string, _ int, cfg settings.SmartRoute) gateway.RouteResult {
	r.mu.Lock()
	r.cfgs = append(r.cfgs, cfg)
	r.mu.Unlock()
	return gateway.RouteResult{Label: "simple", Source: "fallback"}
}

// R132-01: the whole smart-route configuration is one snapshot generation. After a
// failed rebuild a real request keeps routing by the previous generation (group ids,
// thresholds and all, handed to the router); the next successful rebuild switches it
// as a whole.
func TestR132FailedSmartRouteReloadKeepsWholeRoutingGeneration(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var in map[string]any
		_ = json.Unmarshal(b, &in)
		mu.Lock()
		seen = append(seen, in["model"].(string))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer up.Close()
	s := auditServer(t)
	admin := auditUser(s, "r132-smart-admin")
	enc, _ := s.cipher.Encrypt("upstream-key")
	a := model.Account{Name: "r132-upstream", Provider: "openai", Type: model.TypeText, BaseURL: up.URL, APIKeyEnc: enc,
		Protocols: model.StringList{model.ProtoOpenAIChat}, Enabled: true, Health: model.HealthAvailable,
		Mappings: []model.ModelMapping{{RequestModel: "r132-old", UpstreamModel: "r132-old"}, {RequestModel: "r132-new", UpstreamModel: "r132-new"}}}
	s.db.Create(&a)
	oldGroup := model.ModelGroup{Name: "r132-old-group", Type: model.TypeText, Models: model.StringList{"r132-old"}}
	newGroup := model.ModelGroup{Name: "r132-new-group", Type: model.TypeText, Models: model.StringList{"r132-new"}}
	s.db.Create(&oldGroup)
	s.db.Create(&newGroup)
	ug := model.UserGroup{Name: "r132-default", IsDefault: true, Enabled: true}
	s.db.Create(&ug)
	u := model.User{Username: "r132-caller", Role: model.RoleUser, GroupID: ug.ID, Enabled: true}
	s.db.Create(&u)
	key, hash, _ := crypto.GenerateAPIKey()
	s.db.Create(&model.APIKey{UserID: u.ID, Name: "r132-key", KeyHash: hash, Enabled: true})
	if err := s.st.SetVector(settings.Vector{AccountID: a.ID, Model: "r132-embed"}); err != nil {
		t.Fatal(err)
	}
	old := settings.SmartRoute{Enabled: true, VirtualModel: "r132-auto", SimpleGroupID: oldGroup.ID, ComplexGroupID: oldGroup.ID, Threshold: 0.7, ConfidenceGap: 0.1, TopK: 5}
	if err := s.st.SetSmartRoute(old); err != nil {
		t.Fatal(err)
	}
	if err := s.gw.Reload(); err != nil {
		t.Fatal(err)
	}
	router := &r132GenRouter{}
	s.gw.SetRouter(router)
	// The failure injection is registered before any data-plane traffic (the log store
	// writes call logs on a background goroutine, and gorm's callback registry is not
	// safe to mutate concurrently) and switched with an atomic flag.
	var failRebuild atomic.Bool
	if err := s.db.Callback().Query().Before("gorm:query").Register("r132:gateway_reload", func(tx *gorm.DB) {
		if failRebuild.Load() && tx.Statement.Table == "accounts" {
			tx.AddError(errors.New("injected gateway snapshot query failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	call := func() string {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"r132-auto","messages":[{"role":"user","content":"hi"}]}`))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.gw.HandleChat(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("gateway request: %d %s", w.Code, w.Body.String())
		}
		mu.Lock()
		defer mu.Unlock()
		return seen[len(seen)-1]
	}
	if got := call(); got != "r132-old" {
		t.Fatalf("baseline: %q", got)
	}
	failRebuild.Store(true)
	body := map[string]any{"enabled": true, "virtual_model": "r132-auto", "simple_group_id": newGroup.ID, "complex_group_id": newGroup.ID, "threshold": 0.9, "confidence_gap": 0.2, "top_k": 9}
	w := review126Call(s, s.putSmartRoute, admin, "/api/admin/settings/smart-route", body)
	failRebuild.Store(false)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("injected rebuild must fail: %d %s", w.Code, w.Body.String())
	}
	if got := call(); got != "r132-old" {
		t.Fatalf("failed reload must keep the previous complete routing generation; request used %q", got)
	}
	router.mu.Lock()
	last := router.cfgs[len(router.cfgs)-1]
	router.mu.Unlock()
	if last.SimpleGroupID != oldGroup.ID || last.Threshold != 0.7 || last.TopK != 5 {
		t.Fatalf("router must receive the old generation's configuration: %+v", last)
	}
	// The recovery action switches the whole generation at once.
	if w := review126Call(s, s.reloadRuntime, admin, "/api/admin/runtime/reload", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("runtime reload: %d %s", w.Code, w.Body.String())
	}
	if got := call(); got != "r132-new" {
		t.Fatalf("after a successful rebuild the new generation applies: %q", got)
	}
	router.mu.Lock()
	last = router.cfgs[len(router.cfgs)-1]
	router.mu.Unlock()
	if last.SimpleGroupID != newGroup.ID || last.Threshold != 0.9 || last.TopK != 9 {
		t.Fatalf("router must receive the new generation's configuration: %+v", last)
	}
}

// R132-02: a create whose runtime refresh fails answers a committed 503 that names the
// created resource; repeating the identical create returns that resource instead of a
// duplicate, for accounts, words, audit samples and route samples.
func TestR132CommittedCreateRetryDoesNotDuplicate(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r132-account-admin")
	body := map[string]any{
		"name": "r132-duplicate", "provider": "openai", "type": "text", "base_url": "http://127.0.0.1:1/v1", "api_key": "sk-r132",
		"protocols": []string{model.ProtoOpenAIChat}, "mappings": []map[string]string{{"request_model": "r132-model", "upstream_model": "r132-model"}}, "skip_test": true,
	}
	const cb = "r132:create_reload"
	if err := s.db.Callback().Query().Before("gorm:query").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == "accounts" && len(tx.Statement.Selects) == 0 && !strings.Contains(tx.Statement.SQL.String(), "name") {
			tx.AddError(errors.New("injected gateway snapshot query failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	first := review126Call(s, s.createAccount, admin, "/api/admin/accounts", body)
	s.db.Callback().Query().Remove(cb)
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first create must hit the committed-write 503: %d %s", first.Code, first.Body.String())
	}
	var out struct {
		Committed bool `json:"committed"`
		ID        uint `json:"id"`
		Resource  map[string]any
	}
	_ = json.Unmarshal(first.Body.Bytes(), &out)
	if !out.Committed || out.ID == 0 || out.Resource == nil || out.Resource["name"] != "r132-duplicate" {
		t.Fatalf("committed 503 must name the created resource: %s", first.Body.String())
	}
	if second := review126Call(s, s.createAccount, admin, "/api/admin/accounts", body); second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"id":`+strings.TrimSpace(strings.Split(strings.Split(first.Body.String(), `"id":`)[1], ",")[0])) {
		t.Fatalf("identical retry must return the existing account: %d %s", second.Code, second.Body.String())
	}
	var n int64
	s.db.Model(&model.Account{}).Where("name = ?", "r132-duplicate").Count(&n)
	if n != 1 {
		t.Fatalf("one logical create became %d accounts", n)
	}
	// Same name, different endpoint: not a retry.
	other := map[string]any{}
	for k, v := range body {
		other[k] = v
	}
	other["base_url"] = "http://127.0.0.1:2/v1"
	if w := review126Call(s, s.createAccount, admin, "/api/admin/accounts", other); w.Code != http.StatusConflict {
		t.Fatalf("same name with different settings must be 409: %d %s", w.Code, w.Body.String())
	}
	// Words, audit samples and route samples: identical creates are idempotent.
	pg := model.PolicyGroup{Name: "r132-pg", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	for i := 0; i < 2; i++ {
		if w := review126Call(s, s.createWord, admin, "/x", map[string]any{"policy_group_id": pg.ID, "word": "r132-word"}); w.Code != http.StatusOK {
			t.Fatalf("word create %d: %d %s", i, w.Code, w.Body.String())
		}
		if w := review126Call(s, s.createAuditSample, admin, "/x", map[string]any{"policy_group_id": pg.ID, "text": "r132-sample"}); w.Code != http.StatusOK {
			t.Fatalf("sample create %d: %d %s", i, w.Code, w.Body.String())
		}
		if w := review126Call(s, s.createRouteSample, admin, "/x", map[string]any{"label": "simple", "text": "r132-route"}); w.Code != http.StatusOK {
			t.Fatalf("route sample create %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	var words, samples, routes int64
	s.db.Model(&model.SensitiveWord{}).Where("word = ?", "r132-word").Count(&words)
	s.db.Model(&model.AuditSample{}).Where("text = ?", "r132-sample").Count(&samples)
	s.db.Model(&model.RouteSample{}).Where("text = ?", "r132-route").Count(&routes)
	if words != 1 || samples != 1 || routes != 1 {
		t.Fatalf("identical creates must not duplicate: words=%d samples=%d routes=%d", words, samples, routes)
	}
}
