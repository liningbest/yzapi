package gateway

import (
	"sync"
	"time"
)

// rateLimiter keeps trailing-60-second request and token counts per subject ("g:<group>"
// or "k:<key>") in one-second ring buffers. Requests are counted on admission; tokens are
// counted when the request finishes, so the token limit is enforced against what the
// previous minute actually consumed (a burst is admitted until the window is full).
type rateLimiter struct {
	mu sync.Mutex
	m  map[string]*rateWindow
}

type rateWindow struct {
	reqs   [60]int64
	toks   [60]int64
	stamps [60]int64 // unix second each slot currently represents
	seen   time.Time
}

func newRateLimiter() *rateLimiter { return &rateLimiter{m: map[string]*rateWindow{}} }

func (w *rateWindow) slot(now time.Time) int {
	sec := now.Unix()
	i := int(sec % 60)
	if w.stamps[i] != sec {
		w.stamps[i] = sec
		w.reqs[i], w.toks[i] = 0, 0
	}
	return i
}

func (w *rateWindow) sums(now time.Time) (reqs, toks int64) {
	min := now.Unix() - 59
	for i := 0; i < 60; i++ {
		if w.stamps[i] >= min {
			reqs += w.reqs[i]
			toks += w.toks[i]
		}
	}
	return
}

func (r *rateLimiter) window(id string, now time.Time) *rateWindow {
	w, ok := r.m[id]
	if !ok {
		w = &rateWindow{}
		r.m[id] = w
	}
	w.seen = now
	return w
}

// admit checks the subject against its limits (0 = unlimited) and, if admitted, counts
// one request. It returns the violated limit's error, or nil.
func (r *rateLimiter) admit(id string, rpm int, tpm int64) *GatewayError {
	if rpm <= 0 && tpm <= 0 {
		return nil
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.window(id, now)
	reqs, toks := w.sums(now)
	if rpm > 0 && reqs >= int64(rpm) {
		return ErrRateLimited
	}
	if tpm > 0 && toks >= tpm {
		return ErrTokenRateLimited
	}
	w.reqs[w.slot(now)]++
	return nil
}

// addTokens books consumed tokens against the subject's current second.
func (r *rateLimiter) addTokens(id string, n int64) {
	if n <= 0 {
		return
	}
	now := time.Now()
	r.mu.Lock()
	w := r.window(id, now)
	w.toks[w.slot(now)] += n
	r.mu.Unlock()
}

// prune drops subjects idle for longer than maxIdle.
func (r *rateLimiter) prune(maxIdle time.Duration) {
	cut := time.Now().Add(-maxIdle)
	r.mu.Lock()
	for id, w := range r.m {
		if w.seen.Before(cut) {
			delete(r.m, id)
		}
	}
	r.mu.Unlock()
}
