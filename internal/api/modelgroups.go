package api

import (
	"errors"
	"gorm.io/gorm"
	"strings"
	"yzapi/internal/settings"

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
	var roles []string
	if sr.Enabled && sr.SimpleGroupID == mg.ID {
		roles = append(roles, "simple")
	}
	if sr.Enabled && sr.ComplexGroupID == mg.ID {
		roles = append(roles, "complex")
	}
	if roles == nil {
		roles = []string{}
	}
	return gin.H{"id": mg.ID, "name": mg.Name, "type": mg.Type, "models": mg.Models, "note": mg.Note,
		"created_at": mg.CreatedAt, "updated_at": mg.UpdatedAt,
		"in_use_by_route": len(roles) > 0, "route_roles": roles}
}

func (s *Server) listModelGroups(c *gin.Context) {
	pg := paging(c)
	q := s.db.Model(&model.ModelGroup{})
	if v := c.Query("type"); v != "" {
		q = q.Where("type = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("name LIKE ? ESCAPE '\\' OR models LIKE ? ESCAPE '\\'", likeEscape(v), likeEscape(v))
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
		conflictOrServerError(c, err, "分组名称已存在")
		return
	}
	if !s.reloadRuntimes(c, "gateway") {
		return
	}
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
		conflictOrServerError(c, err, "分组名称已存在")
		return
	}
	if !s.reloadRuntimes(c, "gateway") {
		return
	}
	s.db.First(&mg, id)
	c.JSON(200, s.modelGroupView(&mg))
}

func (s *Server) deleteModelGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	errInUse := errors.New("in use")
	// Group row, join rows and the smart-route reference commit or roll back together,
	// under the settings write lock so a concurrent smart-route edit cannot interleave.
	err := s.st.WithTx(func(tx *gorm.DB, cur settings.All) error {
		sr := cur.SmartRoute
		referenced := sr.SimpleGroupID == id || sr.ComplexGroupID == id
		if referenced && sr.Enabled {
			return errInUse
		}
		res := tx.Delete(&model.ModelGroup{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Exec("DELETE FROM user_group_model_groups WHERE model_group_id = ?", id).Error; err != nil {
			return err
		}
		if referenced {
			// Smart routing is off: drop the stale reference in the same transaction.
			if sr.SimpleGroupID == id {
				sr.SimpleGroupID = 0
			}
			if sr.ComplexGroupID == id {
				sr.ComplexGroupID = 0
			}
			return settings.SaveIn(tx, "smart_route", sr)
		}
		return nil
	})
	switch {
	case errors.Is(err, errInUse):
		fail(c, 409, "in_use", "该分组正被智能路由引用，请先在设置中更换")
		return
	case isNotFound(err):
		notFound(c)
		return
	case err != nil:
		serverError(c, err)
		return
	}
	if !s.reloadRuntimes(c, "gateway") {
		return
	}
	c.JSON(200, gin.H{})
}
