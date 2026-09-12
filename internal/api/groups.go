package api

import (
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
)

type userGroupIn struct {
	Name              string `json:"name"`
	MaxConcurrency    int    `json:"max_concurrency"`
	KeyMaxConcurrency int    `json:"key_max_concurrency"`
	TokenQuota        int64  `json:"token_quota"`
	TokensPerMinute   int64  `json:"tokens_per_minute"`
	RequestsPerMinute int    `json:"requests_per_minute"`
	ModelGroupIDs     []uint `json:"model_group_ids"`
	Enabled           *bool  `json:"enabled"`
	Note              string `json:"note"`
}

func (s *Server) userGroupView(g *model.UserGroup, members int64) gin.H {
	ids := make([]uint, 0, len(g.ModelGroups))
	mgs := make([]gin.H, 0, len(g.ModelGroups))
	for _, mg := range g.ModelGroups {
		ids = append(ids, mg.ID)
		mgs = append(mgs, gin.H{"id": mg.ID, "name": mg.Name, "type": mg.Type, "models": mg.Models})
	}
	return gin.H{"id": g.ID, "name": g.Name, "max_concurrency": g.MaxConcurrency, "key_max_concurrency": g.KeyMaxConcurrency,
		"token_quota": g.TokenQuota, "tokens_per_minute": g.TokensPerMinute, "requests_per_minute": g.RequestsPerMinute,
		"is_default": g.IsDefault, "enabled": g.Enabled, "note": g.Note,
		"model_group_ids": ids, "model_groups": mgs, "members_count": members, "tokens_used_month": s.gw.GroupUsage(g.ID),
		"created_at": g.CreatedAt, "updated_at": g.UpdatedAt}
}

func (s *Server) memberCounts() map[uint]int64 {
	type row struct {
		GroupID uint
		N       int64
	}
	var rows []row
	s.db.Model(&model.User{}).Select("group_id, COUNT(*) AS n").Group("group_id").Scan(&rows)
	m := map[uint]int64{}
	for _, r := range rows {
		m[r.GroupID] = r.N
	}
	return m
}

func (s *Server) listUserGroups(c *gin.Context) {
	q := s.db.Model(&model.UserGroup{}).Preload("ModelGroups")
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likeEscape(v))
	}
	var rows []model.UserGroup
	if err := q.Order("is_default DESC, id ASC").Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	mc := s.memberCounts()
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, s.userGroupView(&rows[i], mc[rows[i].ID]))
	}
	listResp(c, out, int64(len(out)))
}

func (s *Server) getUserGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var g model.UserGroup
	if err := s.db.Preload("ModelGroups").First(&g, id).Error; err != nil {
		notFound(c)
		return
	}
	c.JSON(200, s.userGroupView(&g, s.memberCounts()[g.ID]))
}

func validateUserGroup(in *userGroupIn) string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		return "名称不能为空且不超过 64 字符"
	}
	if in.MaxConcurrency < 0 || in.MaxConcurrency > 100000 || in.KeyMaxConcurrency < 0 || in.KeyMaxConcurrency > 100000 {
		return "并发范围 0-100000"
	}
	if in.TokensPerMinute < 0 || in.RequestsPerMinute < 0 {
		return "每分钟限速不能为负数"
	}
	if in.TokenQuota < 0 {
		return "Token 配额不能为负数"
	}
	if len(in.ModelGroupIDs) > 100 {
		return "最多授权 100 个模型组"
	}
	if len(in.Note) > 255 {
		return "备注不能超过 255 字符"
	}
	return ""
}

func (s *Server) loadModelGroups(ids []uint) ([]model.ModelGroup, string) {
	if len(ids) == 0 {
		return nil, ""
	}
	var mgs []model.ModelGroup
	if err := s.db.Where("id IN ?", ids).Find(&mgs).Error; err != nil {
		return nil, err.Error()
	}
	if len(mgs) != len(dedupe(ids)) {
		return nil, "部分模型组不存在"
	}
	return mgs, ""
}

func dedupe(ids []uint) []uint {
	seen := map[uint]bool{}
	var out []uint
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (s *Server) createUserGroup(c *gin.Context) {
	var in userGroupIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := validateUserGroup(&in); msg != "" {
		badRequest(c, msg)
		return
	}
	mgs, msg := s.loadModelGroups(in.ModelGroupIDs)
	if msg != "" {
		badRequest(c, msg)
		return
	}
	g := model.UserGroup{Name: in.Name, MaxConcurrency: in.MaxConcurrency, KeyMaxConcurrency: in.KeyMaxConcurrency,
		TokenQuota: in.TokenQuota, TokensPerMinute: in.TokensPerMinute, RequestsPerMinute: in.RequestsPerMinute,
		Enabled: in.Enabled == nil || *in.Enabled, Note: in.Note, ModelGroups: mgs}
	disabled := !g.Enabled // decided before Create: gorm writes default:true back into the struct
	if err := createWithEnabled(s.db, &g, disabled); err != nil {
		conflictOrServerError(c, err, "用户组名称已存在")
		return
	}
	g.Enabled = !disabled
	if !s.reloadRuntimesFor(c, gin.H{"id": g.ID}, "gateway") {
		return
	}
	s.db.Preload("ModelGroups").First(&g, g.ID)
	c.JSON(200, s.userGroupView(&g, 0))
}

func (s *Server) updateUserGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var g model.UserGroup
	if err := s.db.First(&g, id).Error; err != nil {
		notFound(c)
		return
	}
	var in userGroupIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := validateUserGroup(&in); msg != "" {
		badRequest(c, msg)
		return
	}
	mgs, msg := s.loadModelGroups(in.ModelGroupIDs)
	if msg != "" {
		badRequest(c, msg)
		return
	}
	if g.IsDefault && in.Enabled != nil && !*in.Enabled {
		fail(c, 409, "is_default", "默认用户组不能禁用")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{"name": in.Name, "max_concurrency": in.MaxConcurrency, "key_max_concurrency": in.KeyMaxConcurrency,
			"token_quota": in.TokenQuota, "tokens_per_minute": in.TokensPerMinute, "requests_per_minute": in.RequestsPerMinute, "note": in.Note}
		if in.Enabled != nil {
			upd["enabled"] = *in.Enabled
		}
		if err := tx.Model(&g).Updates(upd).Error; err != nil {
			return err
		}
		if mgs == nil {
			mgs = []model.ModelGroup{}
		}
		return tx.Model(&g).Association("ModelGroups").Replace(mgs)
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			fail(c, 409, "duplicate", "用户组名称已存在")
			return
		}
		serverError(c, err)
		return
	}
	if !s.reloadRuntimes(c, "gateway") {
		return
	}
	s.db.Preload("ModelGroups").First(&g, id)
	c.JSON(200, s.userGroupView(&g, s.memberCounts()[g.ID]))
}

func (s *Server) deleteUserGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var g model.UserGroup
	if err := s.db.First(&g, id).Error; err != nil {
		notFound(c)
		return
	}
	if g.IsDefault {
		fail(c, 409, "is_default", "默认用户组不能删除")
		return
	}
	if s.memberCounts()[g.ID] > 0 {
		fail(c, 409, "has_members", "该用户组仍有成员，请先迁移成员")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&g).Association("ModelGroups").Clear(); err != nil {
			return err
		}
		return tx.Delete(&g).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	if !s.reloadRuntimes(c, "gateway") {
		return
	}
	c.JSON(200, gin.H{})
}

func (s *Server) setUserGroupEnabled(c *gin.Context) {
	id, ok := idParam(c)
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
	var g model.UserGroup
	if err := s.db.First(&g, id).Error; err != nil {
		notFound(c)
		return
	}
	if g.IsDefault && !in.Enabled {
		fail(c, 409, "is_default", "默认用户组不能禁用")
		return
	}
	if err := s.db.Model(&g).Update("enabled", in.Enabled).Error; err != nil {
		serverError(c, err)
		return
	}
	if !s.reloadRuntimes(c, "gateway") {
		return
	}
	c.JSON(200, gin.H{"enabled": in.Enabled})
}
