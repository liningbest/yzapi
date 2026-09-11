package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// ---- configuration snapshots: routing config (accounts, mappings, model groups, their
// user-group bindings) plus every settings section, captured before each change so an
// operator can roll back a bad edit in one click. ----

const maxConfigSnapshots = 50

type configPayload struct {
	Accounts    []model.Account       `json:"accounts"` // includes Mappings and the encrypted key
	ModelGroups []model.ModelGroup    `json:"model_groups"`
	GroupLinks  []userGroupModelGroup `json:"group_links"`
	Settings    map[string]string     `json:"settings"` // key -> raw JSON value
	Prices      []model.ModelPrice    `json:"prices"`
	Meta        map[string]int        `json:"meta"`
}

type userGroupModelGroup struct {
	UserGroupID  uint `json:"user_group_id"`
	ModelGroupID uint `json:"model_group_id"`
}

func (userGroupModelGroup) TableName() string { return "user_group_model_groups" }

// captureConfig serialises the current routing configuration and settings.
func (s *Server) captureConfig(tx *gorm.DB) ([]byte, error) {
	var p configPayload
	if err := tx.Preload("Mappings").Order("id").Find(&p.Accounts).Error; err != nil {
		return nil, err
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
	if err := tx.Order("id").Find(&p.Prices).Error; err != nil {
		return nil, err
	}
	p.Meta = map[string]int{"accounts": len(p.Accounts), "model_groups": len(p.ModelGroups), "prices": len(p.Prices)}
	return json.Marshal(p)
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
	skip := []string{"/test", "/discover", "/test-model", "/cache-check", "/reset-health", "/vector/test", "/elasticsearch/test", "/config/snapshots", "/prices/lookup"}
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
	var p configPayload
	_ = json.Unmarshal([]byte(snap.Data), &p)
	// Summary only; the encrypted keys are never returned to the browser.
	accounts := make([]gin.H, 0, len(p.Accounts))
	for _, a := range p.Accounts {
		accounts = append(accounts, gin.H{"id": a.ID, "name": a.Name, "provider": a.Provider, "base_url": a.BaseURL, "enabled": a.Enabled, "mappings": len(a.Mappings), "priority": a.Priority, "weight": a.Weight})
	}
	groups := make([]gin.H, 0, len(p.ModelGroups))
	for _, g := range p.ModelGroups {
		groups = append(groups, gin.H{"id": g.ID, "name": g.Name, "type": g.Type, "models": g.Models})
	}
	c.JSON(200, gin.H{"id": snap.ID, "actor": snap.Actor, "reason": snap.Reason, "created_at": snap.CreatedAt,
		"accounts": accounts, "model_groups": groups, "settings": p.Settings, "meta": p.Meta})
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
	var p configPayload
	if err := json.Unmarshal([]byte(snap.Data), &p); err != nil {
		serverError(c, err)
		return
	}
	if err := s.snapshotConfig(cur(c).Username, "before restore of #"+strings.TrimSpace(c.Param("id"))); err != nil {
		serverError(c, err)
		return
	}
	err := s.st.WithTx(func(tx *gorm.DB, _ settings.All) error {
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
			a := p.Accounts[i]
			maps := a.Mappings
			a.Mappings = nil
			if err := tx.Create(&a).Error; err != nil {
				return err
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
		if len(p.Prices) > 0 {
			if err := tx.Exec("DELETE FROM model_prices").Error; err != nil {
				return err
			}
			for i := range p.Prices {
				pr := p.Prices[i]
				if err := tx.Create(&pr).Error; err != nil {
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
	_ = s.pricer.Reload()
	s.vectorChanged()
	if err := s.gw.Reload(); err != nil {
		serverError(c, err)
		return
	}
	s.reloadCompliance()
	if s.eng.Route != nil {
		_ = s.eng.Route.Reload()
	}
	c.JSON(200, gin.H{"restored": snap.ID})
}
