package gateway

import (
	"sync"
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
	GroupID     uint
}

type keyCache struct {
	db   *gorm.DB
	mu   sync.RWMutex
	m    map[string]*keyEntry
	last sync.Map // keyID -> time.Time of last persisted LastUsedAt
}

type keyEntry struct {
	p       *Principal
	expires time.Time
}

func newKeyCache(db *gorm.DB) *keyCache { return &keyCache{db: db, m: map[string]*keyEntry{}} }

func (c *keyCache) invalidate() {
	c.mu.Lock()
	c.m = map[string]*keyEntry{}
	c.mu.Unlock()
}

func (c *keyCache) lookup(raw string) *Principal {
	h := crypto.HashAPIKey(raw)
	c.mu.RLock()
	e, ok := c.m[h]
	c.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return e.p
	}
	var k model.APIKey
	if err := c.db.Preload("User").Where("key_hash = ?", h).First(&k).Error; err != nil {
		// Negative-cache misses briefly to blunt brute force scans.
		c.mu.Lock()
		c.m[h] = &keyEntry{p: nil, expires: time.Now().Add(5 * time.Second)}
		c.mu.Unlock()
		return nil
	}
	p := &Principal{KeyID: k.ID, KeyName: k.Name, KeyEnabled: k.Enabled}
	if k.User != nil {
		p.UserID = k.User.ID
		p.Username = k.User.Username
		p.UserRole = k.User.Role
		p.UserEnabled = k.User.Enabled && !k.User.Locked
		p.GroupID = k.User.GroupID
	}
	c.mu.Lock()
	c.m[h] = &keyEntry{p: p, expires: time.Now().Add(30 * time.Second)}
	c.mu.Unlock()
	return p
}

// touch updates LastUsedAt at most once per minute per key.
func (c *keyCache) touch(keyID uint) {
	now := time.Now()
	if v, ok := c.last.Load(keyID); ok && now.Sub(v.(time.Time)) < time.Minute {
		return
	}
	c.last.Store(keyID, now)
	go c.db.Model(&model.APIKey{}).Where("id = ?", keyID).Update("last_used_at", now)
}
