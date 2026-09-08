package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestCounterLimit(t *testing.T) {
	cm := newCounterMap()
	c := cm.get(1, 2)
	if !c.tryAcquire() || !c.tryAcquire() {
		t.Fatal("expected two acquires")
	}
	if c.tryAcquire() {
		t.Fatal("third acquire should fail")
	}
	c.release()
	if !c.tryAcquire() {
		t.Fatal("acquire after release should succeed")
	}
	u := cm.get(2, 0)
	for i := 0; i < 1000; i++ {
		if !u.tryAcquire() {
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
