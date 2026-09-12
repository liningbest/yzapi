package gateway

import (
	"sync"
	"time"
	"unicode/utf8"

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
	// Half-open state: once a cooldown expires, exactly one request is let through as a
	// probe. Success closes the circuit, failure extends the cooldown; other callers keep
	// seeing the account as unavailable meanwhile so a still-broken upstream is not hit
	// by a burst the moment its cooldown ends.
	probing    bool
	probeSince time.Time
}

// probeTimeout bounds how long a probe may stay outstanding (a request that never
// reports back, e.g. failed before sending) before another probe is allowed.
const probeTimeout = 30 * time.Second

func newHealthTracker(db *gorm.DB) *healthTracker {
	return &healthTracker{db: db, m: map[uint]*accountHealth{}}
}

func (h *healthTracker) available(id uint) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.m[id]
	if !ok {
		return true
	}
	if s.unavailable {
		return false
	}
	now := time.Now()
	if now.Before(s.cooldownUntil) {
		return false
	}
	if s.failures == 0 {
		return true
	}
	// Cooldown has expired but the account has not proven itself yet: admit one probe.
	if s.probing && now.Sub(s.probeSince) < probeTimeout {
		return false
	}
	s.probing, s.probeSince = true, now
	return true
}

// probing reports whether the account is currently in its half-open probe window.
func (h *healthTracker) isProbing(id uint) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.m[id]
	return ok && s.probing && time.Since(s.probeSince) < probeTimeout
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
	s.probing = false                 // a failed probe re-opens the circuit with a longer cooldown
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
	s.probing = false
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
	s.probing = false
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
	if h.db == nil { // in-memory only (tests)
		return
	}
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

// truncate keeps s within n bytes, ellipsis included, without splitting a multi-byte
// character (the result fits a size:n column on every database).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const ellipsis = "…"
	cut := n - len(ellipsis)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}
