package api

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/crypto"
	"yzapi/internal/model"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

func (s *Server) adminCount(tx *gorm.DB) int64 {
	var n int64
	tx.Model(&model.User{}).Where("role = ? AND enabled = ?", model.RoleAdmin, true).Count(&n)
	return n
}

func (s *Server) listUsers(c *gin.Context) {
	pg := paging(c)
	q := s.db.Model(&model.User{}).Preload("Group")
	if v := c.Query("role"); v != "" {
		q = q.Where("role = ?", v)
	}
	if v := queryUint(c, "group_id"); v > 0 {
		q = q.Where("group_id = ?", v)
	}
	if v, ok := queryBool(c, "enabled"); ok {
		q = q.Where("enabled = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("username LIKE ? ESCAPE '\\' OR note LIKE ? ESCAPE '\\'", likeEscape(v), likeEscape(v))
	}
	var total int64
	q.Count(&total)
	var rows []model.User
	if err := q.Order("id ASC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	// key counts
	type kc struct {
		UserID uint
		N      int64
	}
	var counts []kc
	ids := make([]uint, 0, len(rows))
	for _, u := range rows {
		ids = append(ids, u.ID)
	}
	if len(ids) > 0 {
		s.db.Model(&model.APIKey{}).Select("user_id, COUNT(*) AS n").Where("user_id IN ?", ids).Group("user_id").Scan(&counts)
	}
	cm := map[uint]int64{}
	for _, k := range counts {
		cm[k.UserID] = k.N
	}
	admins := s.adminCount(s.db)
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		v := userView(&rows[i])
		v["api_keys_count"] = cm[rows[i].ID]
		v["is_last_admin"] = rows[i].Role == model.RoleAdmin && rows[i].Enabled && admins <= 1
		out = append(out, v)
	}
	listResp(c, out, total)
}

func (s *Server) getUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var u model.User
	if err := s.db.Preload("Group").First(&u, id).Error; err != nil {
		notFound(c)
		return
	}
	c.JSON(200, userView(&u))
}

func (s *Server) resolveGroup(id uint) (uint, string) {
	if id == 0 {
		var g model.UserGroup
		if err := s.db.Where("is_default = ?", true).First(&g).Error; err != nil {
			return 0, "默认用户组不存在"
		}
		return g.ID, ""
	}
	var g model.UserGroup
	if err := s.db.First(&g, id).Error; err != nil {
		return 0, "用户组不存在"
	}
	if !g.Enabled {
		return 0, "用户组已禁用"
	}
	return g.ID, ""
}

func (s *Server) createUser(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		GroupID  uint   `json:"group_id"`
		Role     string `json:"role"`
		Note     string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if !usernameRe.MatchString(in.Username) {
		badRequest(c, "用户名需为 3-64 位 ASCII 字符，以字母或数字开头，仅允许字母、数字、点、下划线和连字符")
		return
	}
	if msg := validPassword(in.Password); msg != "" {
		badRequest(c, msg)
		return
	}
	if in.Role == "" {
		in.Role = model.RoleUser
	}
	if in.Role != model.RoleUser && in.Role != model.RoleAdmin {
		badRequest(c, "角色无效")
		return
	}
	gid, msg := s.resolveGroup(in.GroupID)
	if msg != "" {
		badRequest(c, msg)
		return
	}
	h, err := crypto.HashPassword(in.Password)
	if err != nil {
		serverError(c, err)
		return
	}
	u := model.User{Username: in.Username, PasswordHash: h, Role: in.Role, GroupID: gid, Enabled: true, MustChangePassword: true, Note: in.Note}
	if err := s.db.Create(&u).Error; err != nil {
		conflictOrServerError(c, err, "用户名已存在")
		return
	}
	s.db.Preload("Group").First(&u, u.ID)
	c.JSON(200, userView(&u))
}

func (s *Server) updateUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		notFound(c)
		return
	}
	var in struct {
		GroupID *uint   `json:"group_id"`
		Role    *string `json:"role"`
		Note    *string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	upd := map[string]any{}
	if in.GroupID != nil {
		gid, msg := s.resolveGroup(*in.GroupID)
		if msg != "" {
			badRequest(c, msg)
			return
		}
		upd["group_id"] = gid
	}
	if in.Role != nil {
		if *in.Role != model.RoleUser && *in.Role != model.RoleAdmin {
			badRequest(c, "角色无效")
			return
		}
		if u.Role == model.RoleAdmin && *in.Role != model.RoleAdmin && u.Enabled && s.adminCount(s.db) <= 1 {
			fail(c, 409, "last_admin", "系统至少需要保留一名管理员")
			return
		}
		upd["role"] = *in.Role
		if *in.Role != u.Role {
			upd["session_version"] = u.SessionVersion + 1
		}
	}
	if in.Note != nil {
		upd["note"] = *in.Note
	}
	if len(upd) > 0 {
		if err := s.db.Model(&u).Updates(upd).Error; err != nil {
			serverError(c, err)
			return
		}
	}
	s.auth.invalidate(u.ID)
	s.gw.InvalidateKeys()
	s.db.Preload("Group").First(&u, id)
	c.JSON(200, userView(&u))
}

func (s *Server) deleteUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		notFound(c)
		return
	}
	if u.ID == cur(c).ID {
		fail(c, 409, "self", "不能删除当前登录账号")
		return
	}
	if u.Role == model.RoleAdmin && u.Enabled && s.adminCount(s.db) <= 1 {
		fail(c, 409, "last_admin", "系统至少需要保留一名管理员")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&model.APIKey{}).Error; err != nil {
			return err
		}
		return tx.Delete(&u).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	s.auth.invalidate(id)
	s.gw.InvalidateKeys()
	c.JSON(200, gin.H{})
}

func (s *Server) setUserEnabled(c *gin.Context) {
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
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		notFound(c)
		return
	}
	if !in.Enabled && u.Role == model.RoleAdmin && u.Enabled && s.adminCount(s.db) <= 1 {
		fail(c, 409, "last_admin", "系统至少需要保留一名管理员")
		return
	}
	if !in.Enabled && u.ID == cur(c).ID {
		fail(c, 409, "self", "不能禁用当前登录账号")
		return
	}
	upd := map[string]any{"enabled": in.Enabled}
	if !in.Enabled {
		upd["session_version"] = u.SessionVersion + 1
	}
	if err := s.db.Model(&u).Updates(upd).Error; err != nil {
		serverError(c, err)
		return
	}
	s.auth.invalidate(id)
	s.gw.InvalidateKeys()
	c.JSON(200, gin.H{"enabled": in.Enabled})
}

func (s *Server) resetUserPassword(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := validPassword(in.Password); msg != "" {
		badRequest(c, msg)
		return
	}
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		notFound(c)
		return
	}
	h, err := crypto.HashPassword(in.Password)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := s.db.Model(&u).Updates(map[string]any{"password_hash": h, "locked": false, "failed_logins": 0,
		"must_change_password": true, "session_version": u.SessionVersion + 1}).Error; err != nil {
		serverError(c, err)
		return
	}
	s.auth.invalidate(id)
	c.JSON(200, gin.H{})
}

func (s *Server) unlockUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	res := s.db.Model(&model.User{}).Where("id = ?", id).Updates(map[string]any{"locked": false, "failed_logins": 0})
	if res.Error != nil {
		serverError(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		notFound(c)
		return
	}
	s.auth.invalidate(id)
	c.JSON(200, gin.H{})
}
