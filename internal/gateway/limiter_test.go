package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"yzapi/internal/model"
)

func TestCounterLimit(t *testing.T) {
	cm := newCounterMap()
	c := cm.get(1)
	if !c.tryAcquire(2) || !c.tryAcquire(2) {
		t.Fatal("expected two acquires")
	}
	if c.tryAcquire(2) {
		t.Fatal("third acquire should fail")
	}
	c.release()
	if !c.tryAcquire(2) {
		t.Fatal("acquire after release should succeed")
	}
	// A lowered limit takes effect immediately for new acquires while existing holders drain.
	if c.tryAcquire(1) {
		t.Fatal("lowered limit must reject while over capacity")
	}
	u := cm.get(2)
	for i := 0; i < 1000; i++ {
		if !u.tryAcquire(0) {
			t.Fatal("unlimited counter refused")
		}
	}
}

func TestGateQueueAndTimeout(t *testing.T) {
	g := newGate(1, 1)
	if err := g.acquire(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	// second waits in queue; third is rejected because queue is full
	var wg sync.WaitGroup
	wg.Add(1)
	var second error
	go func() {
		defer wg.Done()
		second = g.acquire(context.Background(), 2*time.Second)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := g.acquire(context.Background(), 100*time.Millisecond); err != ErrGatewayBusy {
		t.Fatalf("third acquire err=%v want ErrGatewayBusy", err)
	}
	g.release()
	wg.Wait()
	if second != nil {
		t.Fatalf("queued acquire failed: %v", second)
	}
	g.release()

	// timeout path
	if err := g.acquire(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := g.acquire(context.Background(), 50*time.Millisecond); err != ErrQueueTimeout {
		t.Fatalf("err=%v want ErrQueueTimeout", err)
	}
	inflight, _, waiting, _ := g.stats()
	if inflight != 1 || waiting != 0 {
		t.Fatalf("inflight=%d waiting=%d", inflight, waiting)
	}
}

func TestGateContextCancel(t *testing.T) {
	g := newGate(1, 10)
	_ = g.acquire(context.Background(), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if err := g.acquire(ctx, 5*time.Second); err != ErrQueueTimeout {
		t.Fatalf("err=%v", err)
	}
}

func TestPickProto(t *testing.T) {
	up := &Upstream{Protocols: map[string]bool{"anthropic-messages": true}}
	if p := pickProto(up, "openai-completions", true); p != "anthropic-messages" {
		t.Fatalf("got %s", p)
	}
	if p := pickProto(up, "openai-completions", false); p != "" {
		t.Fatalf("conversion disabled should yield empty, got %s", p)
	}
	if p := pickProto(up, "openai-embeddings", true); p != "" {
		t.Fatalf("embeddings must not convert, got %s", p)
	}
	up2 := &Upstream{Protocols: map[string]bool{"openai-completions": true, "openai-responses": true}}
	if p := pickProto(up2, "anthropic-messages", true); p != "openai-completions" {
		t.Fatalf("prefer chat completions, got %s", p)
	}
}

func TestExtractText(t *testing.T) {
	raw := map[string]jsonRaw{"messages": jsonRaw(`[{"role":"system","content":"s"},{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"x"}}]}]`)}
	txt, n := extractText("openai-completions", raw)
	if txt != "hello" || n != 2 {
		t.Fatalf("txt=%q n=%d", txt, n)
	}
	raw = map[string]jsonRaw{"messages": jsonRaw(`[{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"r"},{"type":"text","text":"again"}]}]`)}
	txt, _ = extractText("anthropic-messages", raw)
	if txt != "again" {
		t.Fatalf("txt=%q", txt)
	}
	raw = map[string]jsonRaw{"input": jsonRaw(`"plain"`)}
	if txt, _ = extractText("openai-responses", raw); txt != "plain" {
		t.Fatalf("txt=%q", txt)
	}
}

type jsonRaw = json.RawMessage

func TestBudget(t *testing.T) {
	b := newBudget(100)
	if !b.acquire(context.Background(), 60, time.Second) || !b.acquire(context.Background(), 40, time.Second) {
		t.Fatal("expected both reservations to fit")
	}
	if b.acquire(context.Background(), 1, 50*time.Millisecond) {
		t.Fatal("budget exhausted, acquire must time out")
	}
	b.release(60)
	if !b.acquire(context.Background(), 60, time.Second) {
		t.Fatal("released bytes must be reusable")
	}
	b.release(100)
	// A single oversized request is admitted when the budget is idle.
	if !b.acquire(context.Background(), 500, time.Second) {
		t.Fatal("oversized request must be admitted alone")
	}
	if b.acquire(context.Background(), 1, 20*time.Millisecond) {
		t.Fatal("nothing else fits while the oversized request holds the budget")
	}
	b.release(500)
	used, _ := b.stats()
	if used != 0 {
		t.Fatalf("used=%d", used)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("30"); d != 30*time.Second {
		t.Fatalf("seconds: %v", d)
	}
	if d := parseRetryAfter("99999"); d != 15*time.Minute {
		t.Fatalf("cap: %v", d)
	}
	if d := parseRetryAfter(time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat)); d < 15*time.Second || d > 21*time.Second {
		t.Fatalf("http date: %v", d)
	}
	if d := parseRetryAfter("garbage"); d != 0 {
		t.Fatalf("garbage: %v", d)
	}
}

func TestFailedAttemptUsageClassification(t *testing.T) {
	if got := networkFailureUsage(&net.OpError{Op: "dial", Err: errors.New("refused")}, false); got != model.UsageNone {
		t.Fatalf("dial failure = %s, want none", got)
	}
	if got := networkFailureUsage(errors.New("unexpected EOF"), true); got != model.UsageUnknown {
		t.Fatalf("mid-response failure = %s, want unknown", got)
	}
	if got := networkFailureUsage(errors.New("context canceled"), false); got != model.UsageNone {
		t.Fatalf("cancelled before sending = %s, want none", got)
	}
	if got := networkFailureUsage(errors.New("context canceled"), true); got != model.UsageUnknown {
		t.Fatalf("cancelled after sending = %s, want unknown", got)
	}
	if httpFailureUsage(500) != model.UsageUnknown || httpFailureUsage(429) != model.UsageNone || httpFailureUsage(401) != model.UsageNone {
		t.Fatal("http failure classification")
	}
	// A 500 that still reported tokens must keep them; a later unknown attempt taints the request.
	st, p, c := usageFromAttempts([]attemptRecord{
		{StatusCode: 500, UsageStatus: model.UsageConfirmed, PromptTokens: 10, CompletionTokens: 3},
	})
	if st != model.UsageConfirmed || p != 10 || c != 3 {
		t.Fatalf("confirmed attempt: %s %d %d", st, p, c)
	}
	st, _, _ = usageFromAttempts([]attemptRecord{{UsageStatus: model.UsageNone}, {UsageStatus: model.UsageUnknown}})
	if st != model.UsageUnknown {
		t.Fatalf("unknown attempt must taint: %s", st)
	}
	// Retry after a failed-but-billed attempt keeps both; unknown before success still taints.
	st, p, c = usageFromAttempts([]attemptRecord{
		{StatusCode: 500, UsageStatus: model.UsageConfirmed, PromptTokens: 10, CompletionTokens: 3},
		{StatusCode: 200, UsageStatus: model.UsageConfirmed, PromptTokens: 20, CompletionTokens: 5},
	})
	if st != model.UsageConfirmed || p != 30 || c != 8 {
		t.Fatalf("retry sum: %s %d %d", st, p, c)
	}
	st, p, c = usageFromAttempts([]attemptRecord{
		{StatusCode: 500, UsageStatus: model.UsageUnknown},
		{StatusCode: 200, UsageStatus: model.UsageConfirmed, PromptTokens: 20, CompletionTokens: 5},
	})
	if st != model.UsageUnknown || p != 20 || c != 5 {
		t.Fatalf("unknown then success: %s %d %d", st, p, c)
	}
	st, _, _ = usageFromAttempts([]attemptRecord{{UsageStatus: model.UsageNone}})
	if st != model.UsageNone {
		t.Fatalf("all none: %s", st)
	}
}

// After a cooldown expires exactly one request probes the account; others wait until the
// probe reports. A failed probe extends the cooldown, a successful one closes the circuit.
func TestHealthHalfOpenProbe(t *testing.T) {
	h := newHealthTracker(nil)
	h.failFor(7, 20*time.Millisecond, "boom")
	if h.available(7) {
		t.Fatal("must be unavailable during cooldown")
	}
	time.Sleep(30 * time.Millisecond)
	if !h.available(7) {
		t.Fatal("first caller after cooldown must be admitted as the probe")
	}
	if h.available(7) {
		t.Fatal("second caller must wait while the probe is outstanding")
	}
	h.fail(7, 20*time.Millisecond, "still broken")
	if h.available(7) {
		t.Fatal("failed probe must re-open the circuit")
	}
	time.Sleep(60 * time.Millisecond) // failures=2 -> 2x base
	if !h.available(7) {
		t.Fatal("probe again after the extended cooldown")
	}
	h.ok(7)
	if !h.available(7) || !h.available(7) {
		t.Fatal("successful probe must fully close the circuit")
	}
}

// Equal-priority accounts are drawn by weight; a lower priority tier always comes first.
func TestOrderUpstreamsWeighted(t *testing.T) {
	a := &Upstream{ID: 1, Priority: 10, Weight: 90}
	b := &Upstream{ID: 2, Priority: 10, Weight: 10}
	c := &Upstream{ID: 3, Priority: 20, Weight: 1000}
	firstA := 0
	for i := 0; i < 2000; i++ {
		got := orderUpstreams([]*Upstream{a, b, c})
		if len(got) != 3 || got[2] != c {
			t.Fatalf("priority tiers must stay ordered: %v", []uint{got[0].ID, got[1].ID, got[2].ID})
		}
		if got[0] == a {
			firstA++
		}
	}
	if firstA < 1700 || firstA > 1900 { // expect ~90%
		t.Fatalf("weight 90 vs 10 should win ~90%% of draws, got %d/2000", firstA)
	}
}

// Trailing-window limits: requests are counted on admission, tokens when booked; the
// window forgets after 60 s; zero means unlimited.
func TestRateLimiterWindow(t *testing.T) {
	r := newRateLimiter()
	if e := r.admit("k:1", 0, 0); e != nil {
		t.Fatal("unlimited must admit")
	}
	if e := r.admit("k:2", 2, 0); e != nil {
		t.Fatal("first")
	}
	if e := r.admit("k:2", 2, 0); e != nil {
		t.Fatal("second")
	}
	if e := r.admit("k:2", 2, 0); e != ErrRateLimited {
		t.Fatalf("third must be rate limited, got %v", e)
	}
	r.addTokens("g:3", 900)
	if e := r.admit("g:3", 0, 1000); e != nil {
		t.Fatal("below token limit must admit")
	}
	r.addTokens("g:3", 200)
	if e := r.admit("g:3", 0, 1000); e != ErrTokenRateLimited {
		t.Fatalf("over token limit must be refused, got %v", e)
	}
	// Age the window: pretend the stamps are from a minute ago.
	w := r.m["g:3"]
	for i := range w.stamps {
		if w.stamps[i] != 0 {
			w.stamps[i] -= 61
		}
	}
	if e := r.admit("g:3", 0, 1000); e != nil {
		t.Fatal("window must forget after 60s")
	}
	r.prune(0)
	if len(r.m) != 0 {
		t.Fatal("prune must drop idle subjects")
	}
}
