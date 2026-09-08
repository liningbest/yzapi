package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// counter is an in-flight counter. The limit is passed by the caller on every
// acquire so a request holding an older configuration snapshot can never write a
// stale limit back; limit<=0 means unlimited.
type counter struct {
	cur atomic.Int64
}

func (c *counter) tryAcquire(lim int64) bool {
	for {
		cur := c.cur.Load()
		if lim > 0 && cur >= lim {
			return false
		}
		if c.cur.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (c *counter) release()       { c.cur.Add(-1) }
func (c *counter) Current() int64 { return c.cur.Load() }

// counterMap keeps per-id counters (groups, keys, accounts).
type counterMap struct {
	mu sync.RWMutex
	m  map[uint]*counter
}

func newCounterMap() *counterMap { return &counterMap{m: map[uint]*counter{}} }

func (cm *counterMap) get(id uint) *counter {
	cm.mu.RLock()
	c, ok := cm.m[id]
	cm.mu.RUnlock()
	if !ok {
		cm.mu.Lock()
		if c, ok = cm.m[id]; !ok {
			c = &counter{}
			cm.m[id] = c
		}
		cm.mu.Unlock()
	}
	return c
}

// snapshot returns current in-flight counts per id.
func (cm *counterMap) snapshot() map[uint]int64 {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make(map[uint]int64, len(cm.m))
	for id, c := range cm.m {
		out[id] = c.cur.Load()
	}
	return out
}

// prune drops idle counters whose ids are no longer configured.
func (cm *counterMap) prune(keep func(uint) bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for id, c := range cm.m {
		if !keep(id) && c.cur.Load() == 0 {
			delete(cm.m, id)
		}
	}
}

// gate implements the global concurrency limit with a bounded wait queue. Waiters
// are woken in an unspecified order (sync.Cond does not guarantee FIFO).
type gate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	inflight int
	waiting  int
	limit    int
	queue    int
	streams  atomic.Int64
}

func newGate(limit, queue int) *gate {
	g := &gate{limit: limit, queue: queue}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *gate) configure(limit, queue int) {
	g.mu.Lock()
	g.limit, g.queue = limit, queue
	g.mu.Unlock()
	g.cond.Broadcast()
}

// acquire blocks until a slot is free, the queue is full, or the timeout elapses.
func (g *gate) acquire(ctx context.Context, timeout time.Duration) error {
	g.mu.Lock()
	if g.limit <= 0 || g.inflight < g.limit {
		g.inflight++
		g.mu.Unlock()
		return nil
	}
	if g.queue > 0 && g.waiting >= g.queue {
		g.mu.Unlock()
		return ErrGatewayBusy
	}
	g.waiting++
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() { g.cond.Broadcast() })
	defer timer.Stop()
	stop := context.AfterFunc(ctx, func() { g.cond.Broadcast() })
	defer stop()
	for {
		if g.limit <= 0 || g.inflight < g.limit {
			g.waiting--
			g.inflight++
			g.mu.Unlock()
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			g.waiting--
			g.mu.Unlock()
			return ErrQueueTimeout
		}
		g.cond.Wait()
	}
}

func (g *gate) release() {
	g.mu.Lock()
	g.inflight--
	g.mu.Unlock()
	g.cond.Signal()
}

func (g *gate) stats() (inflight, limit, waiting, queue int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight, g.limit, g.waiting, g.queue
}
