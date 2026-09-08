package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// counter is a bounded in-flight counter. limit<=0 means unlimited.
type counter struct {
	cur   atomic.Int64
	limit atomic.Int64
}

func (c *counter) tryAcquire() bool {
	lim := c.limit.Load()
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
func (c *counter) Limit() int64   { return c.limit.Load() }

// counterMap keeps per-id counters (groups, keys, accounts).
type counterMap struct {
	mu sync.RWMutex
	m  map[uint]*counter
}

func newCounterMap() *counterMap { return &counterMap{m: map[uint]*counter{}} }

func (cm *counterMap) get(id uint, limit int) *counter {
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
	c.limit.Store(int64(limit))
	return c
}

func (cm *counterMap) snapshot() map[uint][2]int64 {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make(map[uint][2]int64, len(cm.m))
	for id, c := range cm.m {
		out[id] = [2]int64{c.cur.Load(), c.limit.Load()}
	}
	return out
}

// gate implements the global concurrency limit with a bounded FIFO queue.
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
