package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/pricing"
	"yzapi/internal/settings"
)

// Independent acceptance findings R112-03/04, adopted with the fixes.

// R112-04: an attempt whose consumption is unknown makes the request's cost a lower
// bound, so cost_known must be false even when the answering attempt is priced.
func TestR112UnknownAttemptCost(t *testing.T) {
	bad := jsonUpstream(500, `{"error":{"message":"failed after send"}}`)
	defer bad.Close()
	good := jsonUpstream(200, ok200)
	defer good.Close()
	e := newE2E(t, bad.URL, good.URL)
	e.g.SetPricer(testPricer{table: map[string]float64{"m": 100}})
	if w := e.chat(t, context.Background(), false); w.Code != 200 {
		t.Fatal(w.Code)
	}
	l, a := e.callLog(t)
	if len(a) != 2 || a[0].UsageStatus != model.UsageUnknown {
		t.Fatal("setup needs unknown attempt")
	}
	if l.CostKnown || l.CostMicros != 2500 {
		t.Fatalf("unknown attempt followed by success: known=%v cost=%d (want false, 2500)", l.CostKnown, l.CostMicros)
	}
}

// R112-03: Anthropic cache_creation_input_tokens are priced at the cache-write rate,
// non-streaming and streaming, same-protocol and converted.
func TestR112CacheWritePricing(t *testing.T) {
	const body = `{"id":"m","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":1000}}`
	const sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"cache_creation_input_tokens\":1000}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":0}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	var streaming atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if streaming.Load() {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, sse)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, body)
	}))
	defer up.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"m": "m"}})
	if err := e.g.settings.SetPricing(settings.Pricing{Currency: "USD", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	p := model.ModelPrice{Pattern: "m", Provider: "anthropic", Currency: "USD", InputPerM: 3, CacheWritePerM: 3.75, Enabled: true}
	if err := e.db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	svc, err := pricing.New(e.db, e.g.settings)
	if err != nil {
		t.Fatal(err)
	}
	e.g.SetPricer(svc)
	// Converted: chat client on the Anthropic upstream.
	if w := e.chat(t, context.Background(), false); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	l, att := e.callLog(t)
	if l.CostMicros != 3750 || l.CacheWriteTokens != 1000 || len(att) != 1 || att[0].CacheWriteTokens != 1000 {
		t.Fatalf("1000 cache-write tokens: cost=%d micros (want 3750), log write=%d attempt write=%d", l.CostMicros, l.CacheWriteTokens, att[0].CacheWriteTokens)
	}
	// Same protocol, non-streaming.
	if w := e.call(t, "/v1/messages", `{"model":"m","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if l, _ := e.lastLog(t, 2); l.CostMicros != 3750 {
		t.Fatalf("same-protocol cost=%d", l.CostMicros)
	}
	// Streaming, same protocol and converted: message_start carries the cache write.
	streaming.Store(true)
	if w := e.call(t, "/v1/messages", `{"model":"m","max_tokens":5,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if l, _ := e.lastLog(t, 3); l.CostMicros != 3750 || !l.Stream {
		t.Fatalf("same-protocol stream cost=%d", l.CostMicros)
	}
	if w := e.chat(t, context.Background(), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if l, _ := e.lastLog(t, 4); l.CostMicros != 3750 {
		t.Fatalf("converted stream cost=%d", l.CostMicros)
	}
	if w := e.call(t, "/v1/responses", `{"model":"m","input":"hi","stream":true}`, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if l, _ := e.lastLog(t, 5); l.CostMicros != 3750 {
		t.Fatalf("chained stream (anthropic -> chat -> responses) cost=%d", l.CostMicros)
	}
}
