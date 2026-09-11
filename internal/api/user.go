package api

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"yzapi/internal/crypto"
	"yzapi/internal/model"
)

func (s *Server) userModels(c *gin.Context) {
	u := cur(c)
	snap := s.gw.Snapshot()
	grp := snap.Groups[u.GroupID]
	if grp == nil || !grp.Enabled {
		grp = snap.DefaultGroup
	}
	out := make([]gin.H, 0, len(snap.Models))
	for _, m := range snap.Models {
		if grp != nil && grp.Allowed != nil && !grp.Allowed[m.Name] {
			if m.Kind != "group" || !grp.ModelGroupNames[m.Name] {
				continue
			}
		}
		out = append(out, gin.H{"name": m.Name, "type": m.Type, "kind": m.Kind, "provider": m.Provider, "models": m.Models})
	}
	c.JSON(200, gin.H{"base_url": s.st.Get().Basic.BaseURL, "models": out})
}

func keyView(k *model.APIKey) gin.H {
	expired := k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt)
	return gin.H{"id": k.ID, "name": k.Name, "prefix": k.Prefix, "suffix": k.Suffix,
		"masked": k.Prefix + "…" + k.Suffix, "enabled": k.Enabled, "last_used_at": k.LastUsedAt, "created_at": k.CreatedAt,
		"expires_at": k.ExpiresAt, "expired": expired, "allowed_models": orEmpty(k.AllowedModels),
		"tokens_per_minute": k.TokensPerMinute, "requests_per_minute": k.RequestsPerMinute}
}

// keyIn carries the owner-editable fields of an API key.
type keyIn struct {
	Name              string     `json:"name"`
	ExpiresAt         *time.Time `json:"expires_at"`          // null = never
	AllowedModels     []string   `json:"allowed_models"`      // empty = group's models
	TokensPerMinute   int64      `json:"tokens_per_minute"`   // 0 = unlimited
	RequestsPerMinute int        `json:"requests_per_minute"` // 0 = unlimited
}

// validateKeyIn normalises and checks restrictions; the whitelist must be a subset of
// the models the caller can see, so a key can never widen access.
func (s *Server) validateKeyIn(c *gin.Context, in *keyIn) string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		return "名称长度需为 1-64 字节"
	}
	if in.ExpiresAt != nil && in.ExpiresAt.Before(time.Now()) {
		return "有效期必须晚于当前时间"
	}
	if in.TokensPerMinute < 0 || in.RequestsPerMinute < 0 || in.TokensPerMinute > 1e9 || in.RequestsPerMinute > 1e6 {
		return "限速范围无效"
	}
	if len(in.AllowedModels) > 0 {
		visible := map[string]bool{}
		for _, m := range s.visibleModels(cur(c)) {
			visible[m] = true
		}
		seen := map[string]bool{}
		var out []string
		for _, m := range in.AllowedModels {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] {
				continue
			}
			if !visible[m] {
				return "模型 " + m + " 不在你可用的模型范围内"
			}
			seen[m] = true
			out = append(out, m)
		}
		in.AllowedModels = out
	}
	return ""
}

// visibleModels lists the model names (incl. group names) the user may call.
func (s *Server) visibleModels(u *model.User) []string {
	snap := s.gw.Snapshot()
	grp := snap.Groups[u.GroupID]
	if grp == nil || !grp.Enabled {
		grp = snap.DefaultGroup
	}
	var out []string
	for _, m := range snap.Models {
		if grp != nil && grp.Allowed != nil && !grp.Allowed[m.Name] {
			if m.Kind != "group" || !grp.ModelGroupNames[m.Name] {
				continue
			}
		}
		out = append(out, m.Name)
	}
	return out
}

func (s *Server) listMyKeys(c *gin.Context) {
	var rows []model.APIKey
	if err := s.db.Where("user_id = ?", cur(c).ID).Order("id DESC").Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, keyView(&rows[i]))
	}
	c.JSON(200, out)
}

func (s *Server) createMyKey(c *gin.Context) {
	var in keyIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := s.validateKeyIn(c, &in); msg != "" {
		badRequest(c, msg)
		return
	}
	var n int64
	s.db.Model(&model.APIKey{}).Where("user_id = ?", cur(c).ID).Count(&n)
	if n >= 100 {
		badRequest(c, "每个用户最多创建 100 个 API Key")
		return
	}
	key, hash, err := crypto.GenerateAPIKey()
	if err != nil {
		serverError(c, err)
		return
	}
	k := model.APIKey{UserID: cur(c).ID, Name: in.Name, KeyHash: hash, Prefix: key[:7], Suffix: key[len(key)-4:], Enabled: true,
		ExpiresAt: in.ExpiresAt, AllowedModels: in.AllowedModels, TokensPerMinute: in.TokensPerMinute, RequestsPerMinute: in.RequestsPerMinute}
	if err := s.db.Create(&k).Error; err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"key": key, "item": keyView(&k)})
}

func (s *Server) myKey(c *gin.Context) (*model.APIKey, bool) {
	id, ok := idParam(c)
	if !ok {
		return nil, false
	}
	var k model.APIKey
	if err := s.db.Where("id = ? AND user_id = ?", id, cur(c).ID).First(&k).Error; err != nil {
		notFound(c)
		return nil, false
	}
	return &k, true
}

func (s *Server) renameMyKey(c *gin.Context) {
	k, ok := s.myKey(c)
	if !ok {
		return
	}
	var in keyIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := s.validateKeyIn(c, &in); msg != "" {
		badRequest(c, msg)
		return
	}
	upd := map[string]any{"name": in.Name, "expires_at": in.ExpiresAt, "allowed_models": model.StringList(in.AllowedModels),
		"tokens_per_minute": in.TokensPerMinute, "requests_per_minute": in.RequestsPerMinute}
	if err := s.db.Model(k).Updates(upd).Error; err != nil {
		serverError(c, err)
		return
	}
	s.gw.InvalidateKeys()
	c.JSON(200, keyView(k))
}

func (s *Server) setMyKeyEnabled(c *gin.Context) {
	k, ok := s.myKey(c)
	if !ok {
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if err := s.db.Model(k).Update("enabled", in.Enabled).Error; err != nil {
		serverError(c, err)
		return
	}
	s.gw.InvalidateKeys()
	c.JSON(200, gin.H{"enabled": in.Enabled})
}

func (s *Server) deleteMyKey(c *gin.Context) {
	k, ok := s.myKey(c)
	if !ok {
		return
	}
	if err := s.db.Delete(k).Error; err != nil {
		serverError(c, err)
		return
	}
	s.gw.InvalidateKeys()
	c.JSON(200, gin.H{})
}

func (s *Server) userGroup(c *gin.Context) {
	u := cur(c)
	var g model.UserGroup
	if err := s.db.Preload("ModelGroups").First(&g, u.GroupID).Error; err != nil {
		if err := s.db.Preload("ModelGroups").Where("is_default = ?", true).First(&g).Error; err != nil {
			notFound(c)
			return
		}
	}
	mgs := make([]gin.H, 0, len(g.ModelGroups))
	for _, mg := range g.ModelGroups {
		mgs = append(mgs, gin.H{"id": mg.ID, "name": mg.Name, "type": mg.Type, "models": mg.Models})
	}
	c.JSON(200, gin.H{"id": g.ID, "name": g.Name, "max_concurrency": g.MaxConcurrency, "key_max_concurrency": g.KeyMaxConcurrency,
		"token_quota": g.TokenQuota, "tokens_used_month": s.gw.GroupUsage(g.ID), "model_groups": mgs, "is_default": g.IsDefault})
}
