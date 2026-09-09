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

func TestReview105CountTokensAccountLimit(t *testing.T) {
	var calls atomic.Int64
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
		calls.Add(1)
		respondJSON(200, `{"input_tokens":12}`)(w, r, nil)
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}})
	if err := e.db.Model(&model.Account{}).Where("id = ?", 1).Update("max_concurrency", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.g.Reload(); err != nil {
		t.Fatal(err)
	}
	ctr := e.g.accounts.get(1)
	if !ctr.tryAcquire(1) {
		t.Fatal("could not occupy account slot")
	}
	defer ctr.release()
	w := e.call(t, "/v1/messages/count_tokens", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, nil)
	// The implementation may queue/refuse or return a labelled estimate; it must not
	// send an upstream count request when this account has no available slot.
	if calls.Load() != 0 {
		t.Fatalf("account max_concurrency=1 already full, but count_tokens sent %d upstream request(s), status=%d", calls.Load(), w.Code)
	}
}

func TestReview105CaseCollisionWithVersionSuffix(t *testing.T) {
	var calls atomic.Int64
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
		calls.Add(1)
		respondJSON(200, ok200)(w, r, nil)
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "custom", Type: "text", Protocols: []string{model.ProtoOpenAIChat}, Passthrough: true, Mappings: map[string]string{"sample-model": "up-a", "SAMPLE-MODEL": "up-b"}})
	w := e.call(t, "/v1/chat/completions", `{"model":"Sample-Model-20260101","messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "model_ambiguous") || calls.Load() != 0 {
		got, _, _ := up.last()
		t.Fatalf("case collision plus date suffix not rejected: status=%d calls=%d upstream model=%v", w.Code, calls.Load(), got["model"])
	}
}

// Extra passing boundary checks: no upstream access or retained slots/budget after
// a queue cancellation; count_tokens must not put its estimate in the usage ledger.
func TestReview105CountCancellationReleasesResources(t *testing.T) {
	var calls atomic.Int64
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
		calls.Add(1)
		respondJSON(200, `{"input_tokens":12}`)(w, r, nil)
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}})
	p := e.g.settings.Get().Performance
	p.MaxConcurrency = 1
	if err := e.g.settings.SetPerformance(p); err != nil {
		t.Fatal(err)
	}
	if err := e.g.gate.acquire(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(`{"model":"m","messages":[]}`)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+e.key)
	w := httptest.NewRecorder()
	e.g.HandleCountTokens(w, r)
	e.g.gate.release()
	used, _ := e.g.bodyBudget.stats()
	inflight, _, waiting, _ := e.g.gate.stats()
	if used != 0 || inflight != 0 || waiting != 0 || calls.Load() != 0 {
		t.Fatalf("resource leak or unwanted call: body=%d inflight=%d waiting=%d calls=%d", used, inflight, waiting, calls.Load())
	}
	var n int64
	if err := e.db.Model(&model.CallLog{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count request entered generation ledger: %d", n)
	}
}
