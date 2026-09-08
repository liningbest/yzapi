package api

import (
	"strings"

	"github.com/gin-gonic/gin"

	"yzapi/internal/model"
)

type modelGroupIn struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Models []string `json:"models"`
	Note   string   `json:"note"`
}

func (s *Server) modelGroupView(mg *model.ModelGroup) gin.H {
	sr := s.st.Get().SmartRoute
	if mg.Models == nil {
		mg.Models = model.StringList{}
	}
	return gin.H{"id": mg.ID, "name": mg.Name, "type": mg.Type, "models": mg.Models, "note": mg.Note,
		"created_at": mg.CreatedAt, "updated_at": mg.UpdatedAt,
		"in_use_by_route": sr.SimpleGroupID == mg.ID || sr.ComplexGroupID == mg.ID}
}

func (s *Server) listModelGroups(c *gin.Context) {
	pg := paging(c)
	q := s.db.Model(&model.ModelGroup{})
	if v := c.Query("type"); v != "" {
		q = q.Where("type = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("name LIKE ? OR models LIKE ?", likeEscape(v), likeEscape(v))
	}
	var total int64
	q.Count(&total)
	var rows []model.ModelGroup
	if err := q.Order("id ASC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, s.modelGroupView(&rows[i]))
	}
	listResp(c, out, total)
}

func (s *Server) getModelGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var mg model.ModelGroup
	if err := s.db.First(&mg, id).Error; err != nil {
		notFound(c)
		return
	}
	c.JSON(200, s.modelGroupView(&mg))
}

func validateModelGroup(in *modelGroupIn) string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 64 {
		return "名称不能为空且不超过 64 字符"
	}
	if in.Type != model.TypeText && in.Type != model.TypeImage && in.Type != model.TypeEmbedding {
		return "协议类型无效"
	}
	var ms []string
	seen := map[string]bool{}
	for _, m := range in.Models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		ms = append(ms, m)
	}
	if len(ms) == 0 {
		return "至少选择一个模型"
	}
	if len(ms) > 100 {
		return "最多 100 个模型"
	}
	in.Models = ms
	return ""
}

func (s *Server) createModelGroup(c *gin.Context) {
	var in modelGroupIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := validateModelGroup(&in); msg != "" {
		badRequest(c, msg)
		return
	}
	mg := model.ModelGroup{Name: in.Name, Type: in.Type, Models: in.Models, Note: in.Note}
	if err := s.db.Create(&mg).Error; err != nil {
		fail(c, 409, "duplicate", "分组名称已存在")
		return
	}
	_ = s.gw.Reload()
	c.JSON(200, s.modelGroupView(&mg))
}

func (s *Server) updateModelGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var mg model.ModelGroup
	if err := s.db.First(&mg, id).Error; err != nil {
		notFound(c)
		return
	}
	var in modelGroupIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Type = mg.Type
	if msg := validateModelGroup(&in); msg != "" {
		badRequest(c, msg)
		return
	}
	if err := s.db.Model(&mg).Updates(map[string]any{"name": in.Name, "models": model.StringList(in.Models), "note": in.Note}).Error; err != nil {
		fail(c, 409, "duplicate", "分组名称已存在")
		return
	}
	_ = s.gw.Reload()
	s.db.First(&mg, id)
	c.JSON(200, s.modelGroupView(&mg))
}

func (s *Server) deleteModelGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	sr := s.st.Get().SmartRoute
	if sr.SimpleGroupID == id || sr.ComplexGroupID == id {
		fail(c, 409, "in_use", "该分组正被智能路由引用，请先在设置中更换")
		return
	}
	if err := s.db.Exec("DELETE FROM user_group_model_groups WHERE model_group_id = ?", id).Error; err != nil {
		serverError(c, err)
		return
	}
	if err := s.db.Delete(&model.ModelGroup{}, id).Error; err != nil {
		serverError(c, err)
		return
	}
	_ = s.gw.Reload()
	c.JSON(200, gin.H{})
}
