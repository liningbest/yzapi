package api

import (
	"context"
	"testing"
	"time"

	"yzapi/internal/settings"
	"yzapi/internal/vector"
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

// A request that obtained its client before an invalidation must never fill the cache
// afterwards, even if it observed the newer generation later (client and generation are
// taken in one critical section).
func TestVectorSnapshotConsistency(t *testing.T) {
	s := &Server{}
	s.vec.client = &vector.Client{BaseURL: "http://old", Model: "m"}
	s.vec.key = "1|http://old|m"
	snap, msg := s.snapshotVector(settings.Vector{AccountID: 1, Model: "m"})
	if snap.client == nil || msg != "" {
		t.Fatalf("snapshot failed: %q", msg)
	}
	// Invalidate between the snapshot and the cache fill (what an admin update does).
	s.InvalidateVector()
	s.vec.mu.Lock()
	fresh := s.vec.gen == snap.gen && s.vec.cache == snap.cache
	s.vec.mu.Unlock()
	if fresh {
		t.Fatal("stale snapshot must not be considered fresh after invalidation")
	}
	// A new snapshot after invalidation sees a new generation and a distinct client slot.
	s.vec.client = &vector.Client{BaseURL: "http://new", Model: "m"}
	s.vec.key = "1|http://new|m"
	snap2, _ := s.snapshotVector(settings.Vector{AccountID: 1, Model: "m"})
	if snap2.gen == snap.gen || snap2.key == snap.key {
		t.Fatalf("expected new generation/key, got gen=%d key=%s", snap2.gen, snap2.key)
	}
}
