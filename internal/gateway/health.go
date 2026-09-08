package gateway

import (
	"sync"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

// healthTracker keeps per-account cooldown state in memory and mirrors it to the DB.
// All reads copy the state under the lock; DB writes carry a per-account sequence so an
// older write that completes late can never overwrite a newer state.
type healthTracker struct {
	db        *gorm.DB
	mu        sync.Mutex
	m         map[uint]*accountHealth
	persistMu sync.Mutex // serialises DB writes so they land in state order
}

type accountHealth struct {
	failures      int
	cooldownUntil time.Time
	lastError     string
	unavailable   bool
	seq           uint64 // bumped on every state change
	persisted     uint64 // highest seq written to the DB
}

func newHealthTracker(db *gorm.DB) *healthTracker {
	return &healthTracker{db: db, m: map[uint]*accountHealth{}}
}

func (h *healthTracker) available(id uint) bool {
	h.mu.Lock()
	s, ok := h.m[id]
	if !ok {
		h.mu.Unlock()
		return true
	}
	unavailable, until := s.unavailable, s.cooldownUntil
	h.mu.Unlock()
	return !unavailable && time.Now().After(until)
}

func (h *healthTracker) state(id uint) (status string, until time.Time, lastErr string) {
	h.mu.Lock()
	s, ok := h.m[id]
	if !ok {
		h.mu.Unlock()
		return model.HealthAvailable, time.Time{}, ""
	}
	cp := *s
	h.mu.Unlock()
	if cp.unavailable {
		return model.HealthUnavailable, cp.cooldownUntil, cp.lastError
	}
	if time.Now().Before(cp.cooldownUntil) {
		return model.HealthCooling, cp.cooldownUntil, cp.lastError
	}
	return model.HealthAvailable, time.Time{}, cp.lastError
}

// fail records an upstream failure and applies exponential cooldown.
func (h *healthTracker) fail(id uint, base time.Duration, msg string) {
	h.mu.Lock()
	s, ok := h.m[id]
	if !ok {
		s = &accountHealth{}
		h.m[id] = s
	}
	s.failures++
	mult := 1 << min(s.failures-1, 4) // 1,2,4,8,16
	d := base * time.Duration(mult)
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	s.cooldownUntil = time.Now().Add(d)
	s.lastError = msg
	s.seq++
	seq, until := s.seq, s.cooldownUntil
	h.mu.Unlock()
	h.persist(id, seq, map[string]any{"health": model.HealthCooling, "cooldown_until": until, "last_error": truncate(msg, 500)})
}

// failFor puts the account into cooldown for exactly d (e.g. Retry-After or credential errors).
func (h *healthTracker) failFor(id uint, d time.Duration, msg string) {
	h.mu.Lock()
	s, ok := h.m[id]
	if !ok {
		s = &accountHealth{}
		h.m[id] = s
	}
	s.failures++
	s.cooldownUntil = time.Now().Add(d)
	s.lastError = msg
	s.seq++
	seq, until := s.seq, s.cooldownUntil
	h.mu.Unlock()
	h.persist(id, seq, map[string]any{"health": model.HealthCooling, "cooldown_until": until, "last_error": truncate(msg, 500)})
}

func (h *healthTracker) ok(id uint) {
	h.mu.Lock()
	s, ok := h.m[id]
	if !ok || (s.failures == 0 && !s.unavailable) {
		h.mu.Unlock()
		return
	}
	s.failures = 0
	s.unavailable = false
	s.cooldownUntil = time.Time{}
	s.seq++
	seq := s.seq
	h.mu.Unlock()
	h.persist(id, seq, map[string]any{"health": model.HealthAvailable, "cooldown_until": nil, "last_error": ""})
}

func (h *healthTracker) reset(id uint) {
	h.mu.Lock()
	delete(h.m, id)
	h.mu.Unlock()
}

// persist writes asynchronously; a write is skipped if a newer state was persisted meanwhile.
func (h *healthTracker) persist(id uint, seq uint64, upd map[string]any) {
	go func() {
		h.persistMu.Lock()
		defer h.persistMu.Unlock()
		h.mu.Lock()
		s, ok := h.m[id]
		if !ok || seq < s.persisted {
			h.mu.Unlock()
			return
		}
		s.persisted = seq
		h.mu.Unlock()
		_ = h.db.Model(&model.Account{}).Where("id = ?", id).Updates(upd).Error
	}()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
