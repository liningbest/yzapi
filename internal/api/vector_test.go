package api

import (
	"context"
	"testing"
	"time"
)

// Lowering the vector concurrency limit while calls are in flight must not open a
// second, independent set of slots.
func TestVecLimiterDynamicLimit(t *testing.T) {
	var l vecLimiter
	if !l.acquire(context.Background(), 2) || !l.acquire(context.Background(), 2) {
		t.Fatal("two acquires should succeed with limit 2")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if l.acquire(ctx, 1) {
		t.Fatal("third call must wait while 2 are in flight and the limit is now 1")
	}
	l.release()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if l.acquire(ctx2, 1) {
		t.Fatal("still 1 in flight at limit 1: must wait")
	}
	l.release()
	if !l.acquire(context.Background(), 1) {
		t.Fatal("slot free: acquire must succeed")
	}
	l.release()
	if l.current() != 0 {
		t.Fatalf("inflight=%d", l.current())
	}
}
