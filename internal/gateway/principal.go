package gateway

import (
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/crypto"
	"yzapi/internal/model"
)

// Principal is the authenticated caller of a data-plane request.
type Principal struct {
	KeyID       uint
	KeyName     string
	UserID      uint
	Username    string
	UserRole    string
	UserEnabled bool
	KeyEnabled  bool
	KeyExpired  bool
	KeyModels   map[string]bool // nil = inherit the group's models
	KeyTPM      int64
	KeyRPM      int
	GroupID     uint
}

const (
	keyCacheMax      = 20000
	keyCacheTTL      = 30 * time.Second
	keyCacheNegTTL   = 5 * time.Second
	keyTouchInterval = time.Minute
)

// keyCache caches API key lookups. Every invalidate() bumps a generation; a lookup that
// started before the bump must not write its (possibly stale) result back.
type keyCache struct {
	db   *gorm.DB
	mu   sync.RWMutex
	m    map[string]*keyEntry
	gen  uint64
	last sync.Map // keyID -> *atomic.Int64 (unix seconds of last persisted LastUsedAt)
}

type keyEntry struct {
	p       *Principal
	expires time.Time
}

func newKeyCache(db *gorm.DB) *keyCache { return &keyCache{db: db, m: map[string]*keyEntry{}} }

func (c *keyCache) size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}

func (c *keyCache) invalidate() {
	c.mu.Lock()
	c.gen++
	c.m = map[string]*keyEntry{}
	c.mu.Unlock()
}

func (c *keyCache) lookup(raw string) *Principal {
	h := crypto.HashAPIKey(raw)
	c.mu.RLock()
	e, ok := c.m[h]
	gen := c.gen
	c.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return e.p
	}
	var k model.APIKey
	var p *Principal
	ttl := keyCacheNegTTL
	if err := c.db.Preload("User").Where("key_hash = ?", h).First(&k).Error; err == nil {
		p = &Principal{KeyID: k.ID, KeyName: k.Name, KeyEnabled: k.Enabled, KeyTPM: k.TokensPerMinute, KeyRPM: k.RequestsPerMinute}
		if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
			p.KeyExpired = true
		}
		if len(k.AllowedModels) > 0 {
			p.KeyModels = map[string]bool{}
			for _, m := range k.AllowedModels {
				p.KeyModels[m] = true
			}
		}
		if k.User != nil {
			p.UserID = k.User.ID
			p.Username = k.User.Username
			p.UserRole = k.User.Role
			p.UserEnabled = k.User.Enabled && !k.User.Locked
			p.GroupID = k.User.GroupID
		}
		ttl = keyCacheTTL
	}
	c.mu.Lock()
	if c.gen == gen { // nobody invalidated while we were querying
		c.store(h, &keyEntry{p: p, expires: time.Now().Add(ttl)})
	}
	c.mu.Unlock()
	return p
}

// store inserts under the write lock, sweeping expired entries when the cache is full.
func (c *keyCache) store(h string, e *keyEntry) {
	if len(c.m) >= keyCacheMax {
		now := time.Now()
		for k, v := range c.m {
			if now.After(v.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= keyCacheMax {
			// Still full of live entries (an abusive scan): drop everything rather than grow.
			c.m = map[string]*keyEntry{}
		}
	}
	c.m[h] = e
}

// touch updates LastUsedAt at most once per interval per key; the claim is atomic so
// concurrent first-time callers do not all write.
func (c *keyCache) touch(keyID uint) {
	now := time.Now().Unix()
	v, _ := c.last.LoadOrStore(keyID, new(atomic.Int64))
	slot := v.(*atomic.Int64)
	prev := slot.Load()
	if now-prev < int64(keyTouchInterval/time.Second) || !slot.CompareAndSwap(prev, now) {
		return
	}
	go c.db.Model(&model.APIKey{}).Where("id = ?", keyID).Update("last_used_at", time.Unix(now, 0))
}
