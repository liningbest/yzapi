package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"yzapi/internal/model"
)

func TestReview104CountTokensLimits(t *testing.T) {
	up := newRecorder(respondJSON(200, `{"input_tokens":12}`))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}})
	p := e.g.settings.Get().Performance
	p.MaxBodyKB = 1
	p.MaxConcurrency = 1
	if err := e.g.settings.SetPerformance(p); err != nil {
		t.Fatal(err)
	}
	t.Run("body_limit", func(t *testing.T) {
		body := `{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("x", 2048) + `"}]}`
		w := e.call(t, "/v1/messages/count_tokens", body, nil)
		if w.Code != 413 {
			t.Fatalf("2KB body exceeds configured 1KB but count_tokens returned %d", w.Code)
		}
	})
	t.Run("concurrency", func(t *testing.T) {
		if err := e.g.gate.acquire(context.Background(), time.Second); err != nil {
			t.Fatal(err)
		}
		defer e.g.gate.release()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		r := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(`{"model":"m","messages":[]}`)).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+e.key)
		w := httptest.NewRecorder()
		e.g.HandleCountTokens(w, r)
		if w.Code == 200 {
			t.Fatal("count_tokens reached upstream while configured global concurrency slot was occupied")
		}
	})
}

func TestReview104CountTokensAuthorizedGroup(t *testing.T) {
	up := newRecorder(respondJSON(200, anthropicOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}})
	mg := model.ModelGroup{Name: "coding", Type: "text", Models: model.StringList{"m"}}
	e.db.Create(&mg)
	e.db.Exec("INSERT INTO user_group_model_groups (user_group_id,model_group_id) VALUES (1,?)", mg.ID)
	if err := e.g.Reload(); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"coding","max_tokens":20,"messages":[{"role":"user","content":"hello"}]}`
	main := e.call(t, "/v1/messages", body, nil)
	count := e.call(t, "/v1/messages/count_tokens", body, nil)
	if main.Code != 200 {
		t.Fatalf("main path rejected: %d %s", main.Code, main.Body.String())
	}
	if count.Code == 403 {
		t.Fatalf("authorized group name: generation=%d count_tokens=%d %s", main.Code, count.Code, count.Body.String())
	}
}

func TestReview104ModelMetadataType(t *testing.T) {
	up := newRecorder(respondJSON(200, anthropicOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}})
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+e.key)
	w := httptest.NewRecorder()
	e.g.HandleModels(w, r)
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) == 0 {
		t.Fatal("empty models")
	}
	if out.Data[0]["type"] != "model" {
		t.Fatalf("Anthropic ModelInfo requires type=model, got %v", out.Data[0]["type"])
	}
}

func TestReview104PassthroughModelLookup(t *testing.T) {
	up := newRecorder(respondJSON(200, ok200))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "custom", Type: "text", Protocols: []string{model.ProtoOpenAIChat}, Passthrough: true})
	main := e.call(t, "/v1/chat/completions", `{"model":"unlisted-model","messages":[{"role":"user","content":"hello"}]}`, nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/models/unlisted-model", nil)
	r.Header.Set("Authorization", "Bearer "+e.key)
	w := httptest.NewRecorder()
	e.g.HandleModel(w, r)
	if main.Code != 200 {
		t.Fatalf("generation setup=%d", main.Code)
	}
	if w.Code != 200 {
		t.Fatalf("passthrough model generation=%d lookup=%d", main.Code, w.Code)
	}
}

func TestReview104AmbiguousNameWithPassthrough(t *testing.T) {
	up := newRecorder(respondJSON(200, ok200))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "custom", Type: "text", Protocols: []string{model.ProtoOpenAIChat}, Passthrough: true, Mappings: map[string]string{"claude-sonnet-4-5-20250929": "a", "claude-sonnet-4-5-20251001": "b"}})
	w := e.call(t, "/v1/chat/completions", `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`, nil)
	if w.Code == 200 {
		got, _, _ := up.last()
		t.Fatalf("ambiguous configured model was forwarded through passthrough instead of rejected: %v", got["model"])
	}
}
