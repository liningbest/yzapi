package api

import (
	"strings"

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
	return gin.H{"id": k.ID, "name": k.Name, "prefix": k.Prefix, "suffix": k.Suffix,
		"masked": k.Prefix + "…" + k.Suffix, "enabled": k.Enabled, "last_used_at": k.LastUsedAt, "created_at": k.CreatedAt}
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
	var in struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		badRequest(c, "名称长度需为 1-64 字节")
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
	k := model.APIKey{UserID: cur(c).ID, Name: in.Name, KeyHash: hash, Prefix: key[:7], Suffix: key[len(key)-4:], Enabled: true}
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
	var in struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		badRequest(c, "名称不能为空")
		return
	}
	if err := s.db.Model(k).Update("name", strings.TrimSpace(in.Name)).Error; err != nil {
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
