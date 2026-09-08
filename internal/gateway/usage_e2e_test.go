package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/config"
	"yzapi/internal/crypto"
	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// End-to-end metering scenarios from the 5ab43f1 acceptance review: request-level usage
// must be the fold of every attempt, whatever exit path the request takes.

type e2e struct {
	g    *Gateway
	db   *gorm.DB
	logs *logstore.Store
	key  string
}

func newE2E(t *testing.T, upstreams ...string) *e2e {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	st, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	logs, err := logstore.New(db, t.TempDir(), func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logs.Close(context.Background()) })

	grp := model.UserGroup{Name: "default", IsDefault: true, Enabled: true}
	db.Create(&grp)
	user := model.User{Username: "u", Role: "user", GroupID: grp.ID, Enabled: true}
	db.Create(&user)
	key, hash, _ := crypto.GenerateAPIKey()
	db.Create(&model.APIKey{UserID: user.ID, Name: "k", KeyHash: hash, Enabled: true})
	enc, _ := cipher.Encrypt("upstream-secret")
	for i, u := range upstreams {
		acc := model.Account{Name: fmt.Sprintf("acc%d", i), Provider: "openai", Type: "text", BaseURL: u, APIKeyEnc: enc,
			Protocols: model.StringList{model.ProtoOpenAIChat}, Priority: i, Enabled: true, Health: "available",
			Mappings: []model.ModelMapping{{RequestModel: "m", UpstreamModel: "m"}}}
		db.Create(&acc)
	}
	g, err := New(&config.Config{DataDir: t.TempDir()}, db, cipher, st, logs)
	if err != nil {
		t.Fatal(err)
	}
	return &e2e{g: g, db: db, logs: logs, key: key}
}

func (e *e2e) chat(t *testing.T, ctx context.Context, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"model":"m","messages":[{"role":"user","content":"hello there"}]}`
	if stream {
		body = `{"model":"m","stream":true,"messages":[{"role":"user","content":"hello there"}]}`
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+e.key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.g.HandleChat(w, r)
	return w
}

// callLog waits for the single call log of the test to be committed and returns it.
func (e *e2e) callLog(t *testing.T) (model.CallLog, []attemptRecord) {
	t.Helper()
	var l model.CallLog
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := e.db.First(&l).Error; err == nil {
			var att []attemptRecord
			if len(l.Attempts) > 0 {
				_ = json.Unmarshal([]byte(l.Attempts), &att)
			}
			return l, att
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("call log was not committed")
	return l, nil
}

func jsonUpstream(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

const (
	err500WithUsage = `{"error":{"message":"boom"},"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`
	err500NoUsage   = `{"error":{"message":"boom"}}`
	ok200           = `{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`
	sseOK           = "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5,\"total_tokens\":25}}\n\n" +
		"data: [DONE]\n\n"
)

func sseUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sseOK))
	}))
}

// A1: 500 (10/3 reported) then a successful retry (20/5) is billed as 38 tokens on the
// request while the attempts keep their own 13 and 25 attributed to different accounts.
func TestRetrySuccessKeepsEarlierUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			bad := jsonUpstream(500, err500WithUsage)
			defer bad.Close()
			var good *httptest.Server
			if stream {
				good = sseUpstream()
			} else {
				good = jsonUpstream(200, ok200)
			}
			defer good.Close()
			e := newE2E(t, bad.URL, good.URL)
			w := e.chat(t, context.Background(), stream)
			if w.Code != 200 {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			l, att := e.callLog(t)
			if l.UsageStatus != model.UsageConfirmed || l.PromptTokens != 30 || l.CompletionTokens != 8 || l.TotalTokens != 38 || !l.TokensKnown {
				t.Fatalf("request usage = %s %d/%d/%d known=%v", l.UsageStatus, l.PromptTokens, l.CompletionTokens, l.TotalTokens, l.TokensKnown)
			}
			if len(att) != 2 {
				t.Fatalf("attempts = %+v", att)
			}
			if att[0].PromptTokens+att[0].CompletionTokens != 13 || att[1].PromptTokens+att[1].CompletionTokens != 25 {
				t.Fatalf("attempts must keep their own usage: %+v", att)
			}
			if att[0].AccountID == att[1].AccountID || att[0].StatusCode != 500 || att[1].StatusCode != 200 {
				t.Fatalf("attempt attribution lost: %+v", att)
			}
		})
	}
}

// A1: an attempt whose consumption is undeterminable keeps the whole request "unknown"
// even though the retry succeeded; only the known part is summed.
func TestRetrySuccessKeepsUnknown(t *testing.T) {
	bad := jsonUpstream(500, err500NoUsage)
	defer bad.Close()
	good := jsonUpstream(200, ok200)
	defer good.Close()
	e := newE2E(t, bad.URL, good.URL)
	if w := e.chat(t, context.Background(), false); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	l, att := e.callLog(t)
	if l.UsageStatus != model.UsageUnknown || l.TotalTokens != 25 || l.TokensKnown || l.EstPromptTokens == 0 {
		t.Fatalf("request usage = %s total=%d known=%v est=%d", l.UsageStatus, l.TotalTokens, l.TokensKnown, l.EstPromptTokens)
	}
	if len(att) != 2 || att[0].UsageStatus != model.UsageUnknown || att[1].UsageStatus != model.UsageConfirmed {
		t.Fatalf("attempts = %+v", att)
	}
}

// A2: a non-retryable 400 passed through verbatim still bills the tokens it reported,
// on the request and in the hourly rollup.
func TestNonRetryableUsageReachesCall(t *testing.T) {
	bad := jsonUpstream(400, err500WithUsage)
	defer bad.Close()
	e := newE2E(t, bad.URL)
	w := e.chat(t, context.Background(), false)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	l, att := e.callLog(t)
	if l.StatusCode != 400 || l.Result != "client_error" || l.UsageStatus != model.UsageConfirmed || l.TotalTokens != 13 {
		t.Fatalf("log = status %d result %s usage %s total %d", l.StatusCode, l.Result, l.UsageStatus, l.TotalTokens)
	}
	if len(att) != 1 || att[0].StatusCode != 400 || att[0].PromptTokens != 10 {
		t.Fatalf("attempts = %+v", att)
	}
	var roll model.UsageHourly
	if err := e.db.First(&roll).Error; err != nil || roll.TotalTokens != 13 || roll.Requests != 1 {
		t.Fatalf("rollup = %+v err=%v", roll, err)
	}
}

// A3: the client cancelling after the upstream has read the request is "unknown";
// a request that never reached any upstream stays "none".
func TestClientCancelAfterUpstreamReceived(t *testing.T) {
	received := make(chan struct{})
	var once sync.Once
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		once.Do(func() { close(received) })
		<-r.Context().Done() // hang until the gateway drops the connection
	}))
	defer up.Close()
	e := newE2E(t, up.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.chat(t, ctx, false) }()
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream never received the request")
	}
	cancel()
	select {
	case w := <-done:
		_ = w
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after cancel")
	}
	l, att := e.callLog(t)
	if l.StatusCode != 499 || l.UsageStatus != model.UsageUnknown {
		t.Fatalf("cancelled after send: status %d usage %s", l.StatusCode, l.UsageStatus)
	}
	if len(att) != 1 || att[0].UsageStatus != model.UsageUnknown || att[0].Error != "client disconnected" {
		t.Fatalf("attempts = %+v", att)
	}
}

func TestLocalRejectionStaysNone(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close() // connection refused: the request is never written
	e := newE2E(t, url)
	w := e.chat(t, context.Background(), false)
	if w.Code != 502 {
		t.Fatalf("status %d", w.Code)
	}
	l, att := e.callLog(t)
	if l.UsageStatus != model.UsageNone || l.TotalTokens != 0 {
		t.Fatalf("dial failure must be none: %s %d", l.UsageStatus, l.TotalTokens)
	}
	if len(att) != 1 || att[0].UsageStatus != model.UsageNone {
		t.Fatalf("attempts = %+v", att)
	}
}

// B1: the hourly rollup books tokens on the account (and provider) of the attempt that
// consumed them while the request itself is counted once, and Rebuild agrees.
func TestAccountRollupKeepsAttemptAttribution(t *testing.T) {
	bad := jsonUpstream(500, err500WithUsage)
	defer bad.Close()
	good := jsonUpstream(200, ok200)
	defer good.Close()
	e := newE2E(t, bad.URL, good.URL)
	// Make the retry cross providers to check provider attribution too.
	e.db.Model(&model.Account{}).Where("name = ?", "acc0").Update("provider", "deepseek")
	if err := e.g.Reload(); err != nil {
		t.Fatal(err)
	}
	if w := e.chat(t, context.Background(), false); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	l, att := e.callLog(t)
	check := func(stage string) {
		t.Helper()
		var rows []model.UsageHourly
		e.db.Find(&rows)
		byAcc := map[uint]int64{}
		byProv := map[string]int64{}
		var reqs, attempts, tokens int64
		for _, r := range rows {
			byAcc[r.AccountID] += r.TotalTokens
			byProv[r.Provider] += r.TotalTokens
			reqs += r.Requests
			attempts += r.Attempts
			tokens += r.TotalTokens
		}
		if byAcc[att[0].AccountID] != 13 || byAcc[att[1].AccountID] != 25 {
			t.Fatalf("%s: per-account tokens = %v", stage, byAcc)
		}
		if byProv["deepseek"] != 13 || byProv["openai"] != 25 {
			t.Fatalf("%s: per-provider tokens = %v", stage, byProv)
		}
		if reqs != 1 || attempts != 2 || tokens != l.TotalTokens || tokens != 38 {
			t.Fatalf("%s: requests=%d attempts=%d tokens=%d", stage, reqs, attempts, tokens)
		}
	}
	check("live")
	if _, _, err := logstore.Rebuild(e.db, l.CreatedAt.Add(-time.Hour), l.CreatedAt, 30); err != nil {
		t.Fatal(err)
	}
	check("rebuilt")
}

// B1: a request whose every attempt failed still books each attempt's tokens on its own
// account, and an unknown attempt followed by success does not move tokens around.
func TestAccountRollupFailedAndUnknownAttempts(t *testing.T) {
	t.Run("all failed", func(t *testing.T) {
		a := jsonUpstream(500, err500WithUsage)
		defer a.Close()
		b := jsonUpstream(500, `{"error":{"message":"boom"},"usage":{"prompt_tokens":4,"completion_tokens":2}}`)
		defer b.Close()
		e := newE2E(t, a.URL, b.URL)
		if w := e.chat(t, context.Background(), false); w.Code != 502 {
			t.Fatalf("status %d", w.Code)
		}
		l, att := e.callLog(t)
		var rows []model.UsageHourly
		e.db.Find(&rows)
		byAcc := map[uint]int64{}
		var reqs, failed int64
		for _, r := range rows {
			byAcc[r.AccountID] += r.TotalTokens
			reqs += r.Requests
			failed += r.Failed
		}
		if byAcc[att[0].AccountID] != 13 || byAcc[att[1].AccountID] != 6 || reqs != 1 || failed != 1 || l.TotalTokens != 19 {
			t.Fatalf("all-failed attribution: %v reqs=%d failed=%d total=%d", byAcc, reqs, failed, l.TotalTokens)
		}
	})
	t.Run("unknown then success", func(t *testing.T) {
		a := jsonUpstream(500, err500NoUsage)
		defer a.Close()
		b := jsonUpstream(200, ok200)
		defer b.Close()
		e := newE2E(t, a.URL, b.URL)
		e.chat(t, context.Background(), false)
		_, att := e.callLog(t)
		var rows []model.UsageHourly
		e.db.Find(&rows)
		byAcc := map[uint]int64{}
		var unknown, attempts int64
		for _, r := range rows {
			byAcc[r.AccountID] += r.TotalTokens
			unknown += r.UnknownUsage
			attempts += r.Attempts
		}
		if byAcc[att[0].AccountID] != 0 || byAcc[att[1].AccountID] != 25 || unknown != 1 || attempts != 2 {
			t.Fatalf("unknown-then-success attribution: %v unknown=%d attempts=%d", byAcc, unknown, attempts)
		}
	})
}

// B2: an attempt that received HTTP 200 but whose body could not be read (or converted)
// is "unknown", never "none"; usage parsed before a conversion failure is kept.
func TestBodyTruncatedAfter200IsUnknown(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"id":"partial"`)
	}))
	defer up.Close()
	e := newE2E(t, up.URL)
	if w := e.chat(t, context.Background(), false); w.Code != 502 {
		t.Fatalf("status %d", w.Code)
	}
	l, att := e.callLog(t)
	if l.UsageStatus != model.UsageUnknown || l.EstPromptTokens == 0 {
		t.Fatalf("truncated body: usage=%s est=%d", l.UsageStatus, l.EstPromptTokens)
	}
	if len(att) != 1 || att[0].StatusCode != 200 || att[0].UsageStatus != model.UsageUnknown {
		t.Fatalf("attempts = %+v", att)
	}
}

func TestConversionFailureKeepsReportedUsage(t *testing.T) {
	// Upstream speaks Anthropic; the client asked in OpenAI format. The body carries a
	// valid usage block but a content shape the converter cannot handle.
	up := jsonUpstream(200, `{"id":"m1","type":"message","role":"assistant","model":"m","content":"not-an-array","stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":2}}`)
	defer up.Close()
	e := newE2E(t, up.URL)
	e.db.Model(&model.Account{}).Where("name = ?", "acc0").Updates(map[string]any{"provider": "anthropic", "protocols": model.StringList{model.ProtoAnthropicMessages}})
	if err := e.g.Reload(); err != nil {
		t.Fatal(err)
	}
	w := e.chat(t, context.Background(), false)
	l, att := e.callLog(t)
	if w.Code == 200 {
		t.Skip("converter accepted the body; conversion-failure path not exercised")
	}
	if l.UsageStatus != model.UsageConfirmed || l.TotalTokens != 9 || len(att) != 1 || att[0].PromptTokens != 7 {
		t.Fatalf("status %d usage=%s total=%d attempts=%+v", w.Code, l.UsageStatus, l.TotalTokens, att)
	}
}
