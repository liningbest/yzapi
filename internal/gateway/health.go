package gateway

import (
	"sync"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

// healthTracker keeps per-account cooldown state in memory and mirrors it to the DB.
type healthTracker struct {
	db *gorm.DB
	mu sync.RWMutex
	m  map[uint]*accountHealth
}

type accountHealth struct {
	failures      int
	cooldownUntil time.Time
	lastError     string
	unavailable   bool
}

func newHealthTracker(db *gorm.DB) *healthTracker {
	return &healthTracker{db: db, m: map[uint]*accountHealth{}}
}

func (h *healthTracker) available(id uint) bool {
	h.mu.RLock()
	s, ok := h.m[id]
	h.mu.RUnlock()
	if !ok {
		return true
	}
	if s.unavailable {
		return false
	}
	return time.Now().After(s.cooldownUntil)
}

func (h *healthTracker) state(id uint) (status string, until time.Time, lastErr string) {
	h.mu.RLock()
	s, ok := h.m[id]
	h.mu.RUnlock()
	if !ok {
		return model.HealthAvailable, time.Time{}, ""
	}
	if s.unavailable {
		return model.HealthUnavailable, s.cooldownUntil, s.lastError
	}
	if time.Now().Before(s.cooldownUntil) {
		return model.HealthCooling, s.cooldownUntil, s.lastError
	}
	return model.HealthAvailable, time.Time{}, s.lastError
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
	until := s.cooldownUntil
	h.mu.Unlock()
	go h.db.Model(&model.Account{}).Where("id = ?", id).Updates(map[string]any{
		"health": model.HealthCooling, "cooldown_until": until, "last_error": truncate(msg, 500),
	})
}

func (h *healthTracker) ok(id uint) {
	h.mu.Lock()
	s, ok := h.m[id]
	changed := ok && (s.failures > 0 || s.unavailable)
	if ok {
		s.failures = 0
		s.unavailable = false
		s.cooldownUntil = time.Time{}
	}
	h.mu.Unlock()
	if changed {
		go h.db.Model(&model.Account{}).Where("id = ?", id).Updates(map[string]any{
			"health": model.HealthAvailable, "cooldown_until": nil, "last_error": "",
		})
	}
}

func (h *healthTracker) reset(id uint) {
	h.mu.Lock()
	delete(h.m, id)
	h.mu.Unlock()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
