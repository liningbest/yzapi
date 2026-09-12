package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/pricing"
	"yzapi/internal/settings"
)

// ---- configuration snapshots: routing config (accounts, mappings, model groups, their
// user-group bindings) plus every settings section, captured before each change so an
// operator can roll back a bad edit in one click. ----

const maxConfigSnapshots = 50

// configPayloadVersion 2: accounts carry their encrypted key (v1 lost it because
// Account.APIKeyEnc is json:"-"), prices are a complete set (an empty set is restored
// as empty), and the payload carries a checksum.
const configPayloadVersion = 2

// snapshotAccount is the persisted form of an account: the public Account plus the
// encrypted key, which the public JSON shape deliberately omits. Never returned to the
// browser; getConfigSnapshot builds a summary instead.
type snapshotAccount struct {
	model.Account
	APIKeyEnc string `json:"api_key_enc,omitempty"`
}

type configPayload struct {
	Version     int                   `json:"version"`
	Accounts    []snapshotAccount     `json:"accounts"`
	ModelGroups []model.ModelGroup    `json:"model_groups"`
	GroupLinks  []userGroupModelGroup `json:"group_links"`
	Settings    map[string]string     `json:"settings"` // key -> raw JSON value
	Prices      *[]model.ModelPrice   `json:"prices"`   // nil only in v1 snapshots written before prices existed
	Meta        map[string]int        `json:"meta"`
	Checksum    string                `json:"checksum,omitempty"` // sha256 of the payload with Checksum empty
}

// sealPayload marshals the payload with its checksum filled in.
func sealPayload(p *configPayload) ([]byte, error) {
	p.Checksum = ""
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	p.Checksum = hex.EncodeToString(sum[:])
	return json.Marshal(p)
}

// openPayload parses a snapshot and verifies its checksum when it has one.
func openPayload(data []byte) (*configPayload, error) {
	var p configPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Checksum != "" {
		want := p.Checksum
		p.Checksum = ""
		raw, err := json.Marshal(&p)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			return nil, errSnapshotCorrupt
		}
		p.Checksum = want
	}
	return &p, nil
}

type snapshotError string

func (e snapshotError) Error() string { return string(e) }

const errSnapshotCorrupt = snapshotError("snapshot checksum mismatch")

type userGroupModelGroup struct {
	UserGroupID  uint `json:"user_group_id"`
	ModelGroupID uint `json:"model_group_id"`
}

func (userGroupModelGroup) TableName() string { return "user_group_model_groups" }

// captureConfig serialises the current routing configuration and settings.
func (s *Server) captureConfig(tx *gorm.DB) ([]byte, error) {
	p := configPayload{Version: configPayloadVersion}
	var accounts []model.Account
	if err := tx.Preload("Mappings").Order("id").Find(&accounts).Error; err != nil {
		return nil, err
	}
	p.Accounts = make([]snapshotAccount, 0, len(accounts))
	for _, a := range accounts {
		p.Accounts = append(p.Accounts, snapshotAccount{Account: a, APIKeyEnc: a.APIKeyEnc})
	}
	if err := tx.Order("id").Find(&p.ModelGroups).Error; err != nil {
		return nil, err
	}
	if err := tx.Table("user_group_model_groups").Find(&p.GroupLinks).Error; err != nil {
		return nil, err
	}
	var rows []model.Setting
	if err := tx.Find(&rows).Error; err != nil {
		return nil, err
	}
	p.Settings = map[string]string{}
	for _, r := range rows {
		switch r.Key {
		case "basic", "performance", "vector", "smart_route", "compliance", "elasticsearch", "pricing":
			p.Settings[r.Key] = r.Value
		}
	}
	prices := []model.ModelPrice{}
	if err := tx.Order("id").Find(&prices).Error; err != nil {
		return nil, err
	}
	p.Prices = &prices
	p.Meta = map[string]int{"accounts": len(p.Accounts), "model_groups": len(p.ModelGroups), "prices": len(prices)}
	return sealPayload(&p)
}

// snapshotConfig stores a snapshot and trims the history. Failures are logged by the
// caller; a snapshot must never block the change itself.
func (s *Server) snapshotConfig(actor, reason string) error {
	data, err := s.captureConfig(s.db)
	if err != nil {
		return err
	}
	snap := model.ConfigSnapshot{Actor: actor, Reason: reason, Data: model.JSON(data), CreatedAt: time.Now()}
	if err := s.db.Create(&snap).Error; err != nil {
		return err
	}
	var ids []uint
	s.db.Model(&model.ConfigSnapshot{}).Order("id DESC").Offset(maxConfigSnapshots).Pluck("id", &ids)
	if len(ids) > 0 {
		s.db.Where("id IN ?", ids).Delete(&model.ConfigSnapshot{})
	}
	return nil
}

// autoSnapshot is a middleware for mutating routing/settings endpoints: it captures the
// configuration before the handler runs. Read-only and probe endpoints are excluded.
func (s *Server) autoSnapshot() gin.HandlerFunc {
	skip := []string{"/test", "/discover", "/test-model", "/cache-check", "/reset-health", "/vector/test", "/elasticsearch/test", "/config/snapshots", "/prices/lookup", "/prices/import"}
	return func(c *gin.Context) {
		if c.Request.Method == "GET" {
			c.Next()
			return
		}
		p := c.Request.URL.Path
		for _, sfx := range skip {
			if strings.Contains(p, sfx) {
				c.Next()
				return
			}
		}
		if err := s.snapshotConfig(cur(c).Username, c.Request.Method+" "+p); err != nil {
			serverError(c, err)
			return
		}
		c.Next()
	}
}

func (s *Server) listConfigSnapshots(c *gin.Context) {
	var rows []model.ConfigSnapshot
	if err := s.db.Select("id", "actor", "reason", "created_at").Order("id DESC").Limit(maxConfigSnapshots).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, gin.H{"id": r.ID, "actor": r.Actor, "reason": r.Reason, "created_at": r.CreatedAt})
	}
	c.JSON(200, gin.H{"items": out, "total": len(out)})
}

func (s *Server) createConfigSnapshot(c *gin.Context) {
	var in struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&in)
	if strings.TrimSpace(in.Reason) == "" {
		in.Reason = "manual"
	}
	if err := s.snapshotConfig(cur(c).Username, strings.TrimSpace(in.Reason)); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{})
}

func (s *Server) getConfigSnapshot(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var snap model.ConfigSnapshot
	if err := s.db.First(&snap, id).Error; err != nil {
		notFound(c)
		return
	}
	p, err := openPayload([]byte(snap.Data))
	if err != nil {
		p = &configPayload{}
	}
	// Summary only; the encrypted keys are never returned to the browser.
	accounts := make([]gin.H, 0, len(p.Accounts))
	for _, a := range p.Accounts {
		accounts = append(accounts, gin.H{"id": a.ID, "name": a.Name, "provider": a.Provider, "base_url": a.BaseURL, "enabled": a.Enabled, "mappings": len(a.Mappings), "priority": a.Priority, "weight": a.Weight})
	}
	groups := make([]gin.H, 0, len(p.ModelGroups))
	for _, g := range p.ModelGroups {
		groups = append(groups, gin.H{"id": g.ID, "name": g.Name, "type": g.Type, "models": g.Models})
	}
	c.JSON(200, gin.H{"id": snap.ID, "actor": snap.Actor, "reason": snap.Reason, "created_at": snap.CreatedAt, "version": p.Version,
		"accounts": accounts, "model_groups": groups, "settings": p.Settings, "meta": p.Meta, "corrupt": err != nil})
}

// restoreConfigSnapshot replaces the live routing configuration and settings with the
// snapshot in one transaction (a fresh snapshot of the current state is taken first so
// the restore itself can be undone), then reloads settings, pricer and gateway.
func (s *Server) restoreConfigSnapshot(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var snap model.ConfigSnapshot
	if err := s.db.First(&snap, id).Error; err != nil {
		notFound(c)
		return
	}
	p, err := openPayload([]byte(snap.Data))
	if err != nil {
		fail(c, 409, "snapshot_corrupt", "快照校验失败，拒绝恢复："+err.Error())
		return
	}
	if err := s.snapshotConfig(cur(c).Username, "before restore of #"+strings.TrimSpace(c.Param("id"))); err != nil {
		serverError(c, err)
		return
	}
	var missingKeys []string
	var priceRowsSkipped []string
	var priceRowsMerged int
	err = s.st.WithTx(func(tx *gorm.DB, _ settings.All) error {
		// v1 snapshots carry no keys: keep the key the same account (by id) has now, and
		// restore an account whose key is nowhere to be found disabled rather than with an
		// empty credential that would fail against the upstream.
		current := map[uint]string{}
		var live []model.Account
		if err := tx.Select("id", "api_key_enc").Find(&live).Error; err != nil {
			return err
		}
		for _, a := range live {
			current[a.ID] = a.APIKeyEnc
		}
		for _, stmt := range []string{"DELETE FROM model_mappings", "DELETE FROM accounts", "DELETE FROM user_group_model_groups", "DELETE FROM model_groups"} {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		for i := range p.ModelGroups {
			g := p.ModelGroups[i]
			if err := tx.Create(&g).Error; err != nil {
				return err
			}
		}
		for i := range p.Accounts {
			a := p.Accounts[i].Account
			a.APIKeyEnc = p.Accounts[i].APIKeyEnc
			if a.APIKeyEnc == "" {
				if k := current[a.ID]; k != "" {
					a.APIKeyEnc = k
				} else {
					a.Enabled = false
					a.Note = strings.TrimSpace("[恢复时密钥缺失，请重新填写] " + a.Note)
					missingKeys = append(missingKeys, a.Name)
				}
			}
			maps := a.Mappings
			a.Mappings = nil
			disabled := !a.Enabled
			if err := tx.Create(&a).Error; err != nil {
				return err
			}
			if disabled { // gorm's default:true tag turns a false Enabled into true on Create
				if err := tx.Model(&model.Account{}).Where("id = ?", a.ID).Update("enabled", false).Error; err != nil {
					return err
				}
			}
			for j := range maps {
				m := maps[j]
				m.AccountID = a.ID
				if err := tx.Create(&m).Error; err != nil {
					return err
				}
			}
		}
		for _, l := range p.GroupLinks {
			if err := tx.Exec("INSERT INTO user_group_model_groups (user_group_id, model_group_id) VALUES (?, ?)", l.UserGroupID, l.ModelGroupID).Error; err != nil {
				return err
			}
		}
		// The price table is restored as a whole set, an empty set included; only a v1
		// snapshot written before the table existed (field absent) leaves it untouched.
		if p.Prices != nil {
			if err := tx.Exec("DELETE FROM model_prices").Error; err != nil {
				return err
			}
			// A snapshot written before the unique (provider, pattern) key existed may
			// hold rows that only differ in case, or rows the current validation rejects:
			// normalise and de-duplicate exactly like the upgrade migration does, and
			// report what was dropped instead of restoring a table that breaks the key.
			rows, skipped, merged := pricing.NormalizeRows(*p.Prices)
			priceRowsSkipped, priceRowsMerged = skipped, merged
			for i := range rows {
				pr := rows[i]
				disabled := !pr.Enabled // read before Create: gorm writes the default:true back into the struct
				if err := tx.Create(&pr).Error; err != nil {
					return err
				}
				if disabled {
					if err := tx.Model(&model.ModelPrice{}).Where("id = ?", pr.ID).Update("enabled", false).Error; err != nil {
						return err
					}
				}
			}
		}
		// Rows were inserted with their original ids; on Postgres the serial sequences do
		// not follow explicit ids, so move them past the highest id in use.
		if tx.Dialector.Name() == "postgres" {
			for _, table := range []string{"accounts", "model_mappings", "model_groups", "model_prices"} {
				if err := tx.Exec("SELECT setval(pg_get_serial_sequence('" + table + "', 'id'), COALESCE((SELECT MAX(id) FROM " + table + "), 0) + 1, false)").Error; err != nil {
					return err
				}
			}
		}
		// Sections absent from the snapshot were at their defaults back then: drop the row.
		for _, k := range []string{"basic", "performance", "vector", "smart_route", "compliance", "elasticsearch", "pricing"} {
			v, ok := p.Settings[k]
			if !ok {
				if err := tx.Where("key = ?", k).Delete(&model.Setting{}).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Save(&model.Setting{Key: k, Value: v, UpdatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		serverError(c, err)
		return
	}
	if len(missingKeys) > 0 {
		slog.Warn("config restore: accounts restored disabled because the snapshot carries no key", "snapshot", snap.ID, "accounts", missingKeys)
	}
	if len(priceRowsSkipped) > 0 || priceRowsMerged > 0 {
		slog.Warn("config restore: price rows normalised for the unique key", "snapshot", snap.ID, "skipped", priceRowsSkipped, "merged", priceRowsMerged)
	}
	out := gin.H{"restored": snap.ID, "missing_keys": missingKeys, "price_rows_skipped": priceRowsSkipped, "price_rows_merged": priceRowsMerged}
	// The database now holds the restored configuration. Every runtime copy is
	// refreshed (one failure never skips the others) and every failure is reported in
	// one 503 that still carries the restore report.
	if failed := s.refreshRuntimes("prices", "gateway", "vector"); len(failed) > 0 {
		reloadFailed(c, failed, out)
		return
	}
	c.JSON(200, out)
}
