package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"yzapi/internal/model"
)

func TestReview106ResolutionComposition(t *testing.T) {
	tests := []struct {
		name      string
		mappings  map[string]string
		request   string
		ambiguous bool
	}{
		{"duplicate_longer_candidate", map[string]string{"demo": "base", "demo-1": "version-a", "DEMO-1": "version-b"}, "Demo-1-20260101", true},
		{"non_version_variant_passthrough", map[string]string{"demo": "a", "DEMO": "b"}, "demo-coder", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
				calls.Add(1)
				respondJSON(200, ok200)(w, r, nil)
			})
			defer up.srv.Close()
			e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Type: "text", Provider: "custom", Protocols: []string{model.ProtoOpenAIChat}, Passthrough: true, Mappings: tc.mappings})
			w := e.call(t, "/v1/chat/completions", `{"model":"`+tc.request+`","messages":[{"role":"user","content":"hi"}]}`, nil)
			got, _, _ := up.last()
			if tc.ambiguous {
				if w.Code != 400 || !strings.Contains(w.Body.String(), "model_ambiguous") || calls.Load() != 0 {
					t.Fatalf("longer case-conflicting candidate must not fall back to shorter model: status=%d upstream calls=%d model=%v", w.Code, calls.Load(), got["model"])
				}
			} else {
				if w.Code != 200 || calls.Load() != 1 || got["model"] != tc.request {
					t.Fatalf("non-version variant should remain unmatched and passthrough unchanged: status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
				}
			}
		})
	}
}

func TestReview106CountSlotsReleased(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			first := newRecorder(respondJSON(status, `{"input_tokens":12}`))
			defer first.srv.Close()
			second := newRecorder(respondJSON(200, `{"input_tokens":24}`))
			defer second.srv.Close()
			spec := func(url string) acctSpec {
				return acctSpec{URL: url, Type: "text", Provider: "anthropic", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}}
			}
			e := newE2EAccounts(t, spec(first.srv.URL), spec(second.srv.URL))
			if err := e.db.Model(&model.Account{}).Where("id > 0").Update("max_concurrency", 1).Error; err != nil {
				t.Fatal(err)
			}
			if err := e.g.Reload(); err != nil {
				t.Fatal(err)
			}
			w := e.call(t, "/v1/messages/count_tokens", `{"model":"m","messages":[]}`, nil)
			if w.Code != 200 {
				t.Fatalf("status=%d", w.Code)
			}
			if status != 200 && !strings.Contains(w.Body.String(), "24") {
				t.Fatalf("next account not used: %s", w.Body.String())
			}
			for _, id := range []uint{1, 2} {
				if n := e.g.accounts.get(id).Current(); n != 0 {
					t.Errorf("account=%d retained %d slots", id, n)
				}
			}
			used, _ := e.g.bodyBudget.stats()
			busy, _, waiting, _ := e.g.gate.stats()
			if used != 0 || busy != 0 || waiting != 0 {
				t.Fatalf("resources: body=%d busy=%d waiting=%d", used, busy, waiting)
			}
		})
	}
}

func TestReview106CancelActiveCountReleasesAccount(t *testing.T) {
	entered := make(chan struct{}, 1)
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) { entered <- struct{}{}; <-r.Context().Done() })
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Type: "text", Provider: "anthropic", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}})
	if err := e.db.Model(&model.Account{}).Where("id=1").Update("max_concurrency", 1).Error; err != nil {
		t.Fatal(err)
	}
	e.g.Reload()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(`{"model":"m","messages":[]}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+e.key)
	done := make(chan struct{})
	go func() { e.g.HandleCountTokens(httptest.NewRecorder(), r); close(done) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("upstream not reached")
	}
	active := e.g.accounts.get(1).Current()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not stop")
	}
	if active != 1 || e.g.accounts.get(1).Current() != 0 {
		t.Fatalf("account slots during=%d after=%d", active, e.g.accounts.get(1).Current())
	}
	used, _ := e.g.bodyBudget.stats()
	busy, _, waiting, _ := e.g.gate.stats()
	if used != 0 || busy != 0 || waiting != 0 {
		t.Fatalf("resources: body=%d busy=%d waiting=%d", used, busy, waiting)
	}
}
