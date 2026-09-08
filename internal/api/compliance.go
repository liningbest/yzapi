package api

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
)

func (s *Server) registerComplianceAPI(r *gin.RouterGroup) {
	r.GET("/policy-groups", s.listPolicyGroups)
	r.POST("/policy-groups", s.createPolicyGroup)
	r.PUT("/policy-groups/:id", s.updatePolicyGroup)
	r.DELETE("/policy-groups/:id", s.deletePolicyGroup)
	r.PATCH("/policy-groups/:id/enabled", s.setPolicyGroupEnabled)

	r.GET("/words", s.listWords)
	r.POST("/words", s.createWord)
	r.POST("/words/batch", s.batchWords)
	r.PUT("/words/:id", s.updateWord)
	r.DELETE("/words/:id", s.deleteWord)
	r.PATCH("/words/:id/enabled", s.setWordEnabled)

	r.GET("/samples", s.listAuditSamples)
	r.POST("/samples", s.createAuditSample)
	r.POST("/samples/build", s.buildAuditVectors)
	r.PUT("/samples/:id", s.updateAuditSample)
	r.DELETE("/samples/:id", s.deleteAuditSample)

	r.GET("/audit-logs", s.listAuditLogs)
	r.GET("/audit-logs/:id", s.getAuditLog)
	r.POST("/test", s.complianceTest)
}

func (s *Server) reloadCompliance() {
	if s.eng.Compliance != nil {
		_ = s.eng.Compliance.Reload()
	}
}

// ---- policy groups ----

func policyView(p *model.PolicyGroup, words, samples int64) gin.H {
	return gin.H{"id": p.ID, "name": p.Name, "action": p.Action, "risk_level": p.RiskLevel, "enabled": p.Enabled,
		"description": p.Description, "words_count": words, "samples_count": samples, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt}
}

func policyRef(p *model.PolicyGroup) gin.H {
	if p == nil {
		return nil
	}
	return gin.H{"id": p.ID, "name": p.Name, "action": p.Action, "risk_level": p.RiskLevel, "enabled": p.Enabled}
}

func validPolicy(action, risk string) bool {
	return (action == "block" || action == "audit") && (risk == "low" || risk == "medium" || risk == "high")
}

func (s *Server) listPolicyGroups(c *gin.Context) {
	var rows []model.PolicyGroup
	if err := s.db.Order("id ASC").Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	type cnt struct {
		PolicyGroupID uint
		N             int64
	}
	var wc, sc []cnt
	s.db.Model(&model.SensitiveWord{}).Select("policy_group_id, COUNT(*) AS n").Group("policy_group_id").Scan(&wc)
	s.db.Model(&model.AuditSample{}).Select("policy_group_id, COUNT(*) AS n").Group("policy_group_id").Scan(&sc)
	wm, sm := map[uint]int64{}, map[uint]int64{}
	for _, x := range wc {
		wm[x.PolicyGroupID] = x.N
	}
	for _, x := range sc {
		sm[x.PolicyGroupID] = x.N
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, policyView(&rows[i], wm[rows[i].ID], sm[rows[i].ID]))
	}
	listResp(c, out, int64(len(out)))
}

func (s *Server) createPolicyGroup(c *gin.Context) {
	var in struct {
		Name        string `json:"name"`
		Action      string `json:"action"`
		RiskLevel   string `json:"risk_level"`
		Enabled     *bool  `json:"enabled"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validPolicy(in.Action, in.RiskLevel) {
		badRequest(c, "名称、动作(block/audit)、风险等级(low/medium/high) 均为必填")
		return
	}
	p := model.PolicyGroup{Name: in.Name, Action: in.Action, RiskLevel: in.RiskLevel, Enabled: in.Enabled == nil || *in.Enabled, Description: in.Description}
	if err := s.db.Create(&p).Error; err != nil {
		fail(c, 409, "duplicate", "策略组名称已存在")
		return
	}
	if !p.Enabled {
		s.db.Model(&p).Update("enabled", false)
	}
	s.reloadCompliance()
	c.JSON(200, policyView(&p, 0, 0))
}

func (s *Server) updatePolicyGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var p model.PolicyGroup
	if err := s.db.First(&p, id).Error; err != nil {
		notFound(c)
		return
	}
	var in struct {
		Name        string `json:"name"`
		Action      string `json:"action"`
		RiskLevel   string `json:"risk_level"`
		Enabled     *bool  `json:"enabled"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validPolicy(in.Action, in.RiskLevel) {
		badRequest(c, "名称、动作、风险等级均为必填")
		return
	}
	upd := map[string]any{"name": in.Name, "action": in.Action, "risk_level": in.RiskLevel, "description": in.Description}
	if in.Enabled != nil {
		upd["enabled"] = *in.Enabled
	}
	if err := s.db.Model(&p).Updates(upd).Error; err != nil {
		fail(c, 409, "duplicate", "策略组名称已存在")
		return
	}
	s.reloadCompliance()
	s.db.First(&p, id)
	c.JSON(200, policyView(&p, 0, 0))
}

func (s *Server) deletePolicyGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var n int64
	s.db.Model(&model.SensitiveWord{}).Where("policy_group_id = ?", id).Count(&n)
	var m int64
	s.db.Model(&model.AuditSample{}).Where("policy_group_id = ?", id).Count(&m)
	if n+m > 0 {
		fail(c, 409, "in_use", "该策略组仍被敏感词或审核样本引用")
		return
	}
	if err := s.db.Delete(&model.PolicyGroup{}, id).Error; err != nil {
		serverError(c, err)
		return
	}
	s.reloadCompliance()
	c.JSON(200, gin.H{})
}

func (s *Server) setEnabledGeneric(c *gin.Context, tbl any) {
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
	if err := s.db.Model(tbl).Where("id = ?", id).Update("enabled", in.Enabled).Error; err != nil {
		serverError(c, err)
		return
	}
	s.reloadCompliance()
	c.JSON(200, gin.H{"enabled": in.Enabled})
}

func (s *Server) setPolicyGroupEnabled(c *gin.Context) { s.setEnabledGeneric(c, &model.PolicyGroup{}) }
func (s *Server) setWordEnabled(c *gin.Context)        { s.setEnabledGeneric(c, &model.SensitiveWord{}) }

// ---- sensitive words ----

func wordView(w *model.SensitiveWord) gin.H {
	return gin.H{"id": w.ID, "policy_group_id": w.PolicyGroupID, "policy_group": policyRef(w.PolicyGroup), "word": w.Word,
		"note": w.Note, "enabled": w.Enabled, "created_at": w.CreatedAt}
}

func (s *Server) listWords(c *gin.Context) {
	pg := paging(c)
	q := s.db.Model(&model.SensitiveWord{}).Preload("PolicyGroup")
	if v := queryUint(c, "policy_group_id"); v > 0 {
		q = q.Where("policy_group_id = ?", v)
	}
	if v, ok := queryBool(c, "enabled"); ok {
		q = q.Where("enabled = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("word LIKE ? OR note LIKE ?", likeEscape(v), likeEscape(v))
	}
	var total int64
	q.Count(&total)
	var rows []model.SensitiveWord
	if err := q.Order("id DESC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, wordView(&rows[i]))
	}
	listResp(c, out, total)
}

func (s *Server) policyExists(id uint) bool {
	var n int64
	s.db.Model(&model.PolicyGroup{}).Where("id = ?", id).Count(&n)
	return n > 0
}

func (s *Server) createWord(c *gin.Context) {
	var in struct {
		PolicyGroupID uint   `json:"policy_group_id"`
		Word          string `json:"word"`
		Note          string `json:"note"`
		Enabled       *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Word = strings.TrimSpace(in.Word)
	if in.Word == "" || len(in.Word) > 255 || !s.policyExists(in.PolicyGroupID) {
		badRequest(c, "敏感词不能为空，且必须选择有效的策略组")
		return
	}
	w := model.SensitiveWord{PolicyGroupID: in.PolicyGroupID, Word: in.Word, Note: in.Note, Enabled: in.Enabled == nil || *in.Enabled}
	if err := s.db.Create(&w).Error; err != nil {
		serverError(c, err)
		return
	}
	if !w.Enabled {
		s.db.Model(&w).Update("enabled", false)
	}
	s.reloadCompliance()
	s.db.Preload("PolicyGroup").First(&w, w.ID)
	c.JSON(200, wordView(&w))
}

func (s *Server) updateWord(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var w model.SensitiveWord
	if err := s.db.First(&w, id).Error; err != nil {
		notFound(c)
		return
	}
	var in struct {
		PolicyGroupID uint   `json:"policy_group_id"`
		Word          string `json:"word"`
		Note          string `json:"note"`
		Enabled       *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Word = strings.TrimSpace(in.Word)
	if in.Word == "" || !s.policyExists(in.PolicyGroupID) {
		badRequest(c, "敏感词不能为空，且必须选择有效的策略组")
		return
	}
	upd := map[string]any{"policy_group_id": in.PolicyGroupID, "word": in.Word, "note": in.Note}
	if in.Enabled != nil {
		upd["enabled"] = *in.Enabled
	}
	if err := s.db.Model(&w).Updates(upd).Error; err != nil {
		serverError(c, err)
		return
	}
	s.reloadCompliance()
	s.db.Preload("PolicyGroup").First(&w, id)
	c.JSON(200, wordView(&w))
}

func (s *Server) deleteWord(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.db.Delete(&model.SensitiveWord{}, id).Error; err != nil {
		serverError(c, err)
		return
	}
	s.reloadCompliance()
	c.JSON(200, gin.H{})
}

func (s *Server) batchWords(c *gin.Context) {
	var in struct {
		PolicyGroupID uint     `json:"policy_group_id"`
		Words         []string `json:"words"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if !s.policyExists(in.PolicyGroupID) {
		badRequest(c, "策略组不存在")
		return
	}
	seen := map[string]bool{}
	var rows []model.SensitiveWord
	for _, w := range in.Words {
		w = strings.TrimSpace(w)
		if w == "" || seen[w] || len(w) > 255 {
			continue
		}
		seen[w] = true
		rows = append(rows, model.SensitiveWord{PolicyGroupID: in.PolicyGroupID, Word: w, Enabled: true})
	}
	if len(rows) == 0 {
		badRequest(c, "没有有效的敏感词")
		return
	}
	if err := s.db.CreateInBatches(&rows, 500).Error; err != nil {
		serverError(c, err)
		return
	}
	s.reloadCompliance()
	c.JSON(200, gin.H{"created": len(rows)})
}

// ---- audit samples ----

func auditSampleView(x *model.AuditSample) gin.H {
	return gin.H{"id": x.ID, "policy_group_id": x.PolicyGroupID, "policy_group": policyRef(x.PolicyGroup), "text": x.Text,
		"note": x.Note, "enabled": x.Enabled, "vector_dim": x.VectorDim, "vectorized": x.VectorDim > 0, "created_at": x.CreatedAt}
}

func (s *Server) listAuditSamples(c *gin.Context) {
	pg := paging(c)
	q := s.db.Model(&model.AuditSample{}).Preload("PolicyGroup")
	if v := queryUint(c, "policy_group_id"); v > 0 {
		q = q.Where("policy_group_id = ?", v)
	}
	if v, ok := queryBool(c, "enabled"); ok {
		q = q.Where("enabled = ?", v)
	}
	if v, ok := queryBool(c, "vectorized"); ok {
		if v {
			q = q.Where("vector_dim > 0")
		} else {
			q = q.Where("vector_dim = 0")
		}
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("text LIKE ? OR note LIKE ?", likeEscape(v), likeEscape(v))
	}
	var total int64
	q.Count(&total)
	var rows []model.AuditSample
	if err := q.Order("id DESC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, auditSampleView(&rows[i]))
	}
	listResp(c, out, total)
}

func (s *Server) createAuditSample(c *gin.Context) {
	var in struct {
		PolicyGroupID uint   `json:"policy_group_id"`
		Text          string `json:"text"`
		Note          string `json:"note"`
		Enabled       *bool  `json:"enabled"`
		BuildVector   bool   `json:"build_vector"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || !s.policyExists(in.PolicyGroupID) {
		badRequest(c, "样本文本不能为空，且必须选择有效的策略组")
		return
	}
	x := model.AuditSample{PolicyGroupID: in.PolicyGroupID, Text: in.Text, Note: in.Note, Enabled: in.Enabled == nil || *in.Enabled}
	if err := s.db.Create(&x).Error; err != nil {
		serverError(c, err)
		return
	}
	if !x.Enabled {
		s.db.Model(&x).Update("enabled", false)
	}
	var buildErr string
	if in.BuildVector && s.eng.Compliance != nil {
		if _, _, err := s.eng.Compliance.BuildVectors(c.Request.Context(), []uint{x.ID}); err != nil {
			buildErr = err.Error()
		}
	} else {
		s.reloadCompliance()
	}
	s.db.Preload("PolicyGroup").First(&x, x.ID)
	v := auditSampleView(&x)
	if buildErr != "" {
		v["build_error"] = buildErr
	}
	c.JSON(200, v)
}

func (s *Server) updateAuditSample(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var x model.AuditSample
	if err := s.db.First(&x, id).Error; err != nil {
		notFound(c)
		return
	}
	var in struct {
		PolicyGroupID uint   `json:"policy_group_id"`
		Text          string `json:"text"`
		Note          string `json:"note"`
		Enabled       *bool  `json:"enabled"`
		BuildVector   bool   `json:"build_vector"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || !s.policyExists(in.PolicyGroupID) {
		badRequest(c, "样本文本不能为空，且必须选择有效的策略组")
		return
	}
	upd := map[string]any{"policy_group_id": in.PolicyGroupID, "text": in.Text, "note": in.Note}
	if in.Enabled != nil {
		upd["enabled"] = *in.Enabled
	}
	changed := in.Text != x.Text
	if changed {
		upd["vector"] = nil
		upd["vector_dim"] = 0
	}
	if err := s.db.Model(&x).Updates(upd).Error; err != nil {
		serverError(c, err)
		return
	}
	if s.eng.Compliance != nil && changed && in.BuildVector {
		_, _, _ = s.eng.Compliance.BuildVectors(c.Request.Context(), []uint{x.ID})
	} else {
		s.reloadCompliance()
	}
	s.db.Preload("PolicyGroup").First(&x, id)
	c.JSON(200, auditSampleView(&x))
}

func (s *Server) deleteAuditSample(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.db.Delete(&model.AuditSample{}, id).Error; err != nil {
		serverError(c, err)
		return
	}
	s.reloadCompliance()
	c.JSON(200, gin.H{})
}

func (s *Server) buildAuditVectors(c *gin.Context) {
	var in struct {
		IDs []uint `json:"ids"`
		All bool   `json:"all"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if s.eng.Compliance == nil {
		fail(c, 503, "unavailable", "合规引擎未启用")
		return
	}
	ids := in.IDs
	if in.All {
		ids = nil
	} else if len(ids) == 0 {
		badRequest(c, "请选择样本")
		return
	}
	built, failed, err := s.eng.Compliance.BuildVectors(c.Request.Context(), ids)
	resp := gin.H{"built": built, "failed": failed}
	if err != nil {
		resp["error"] = err.Error()
	}
	c.JSON(200, resp)
}

// ---- audit logs ----

func auditLogView(l *model.AuditLog) gin.H {
	var hits any = []any{}
	if len(l.Hits) > 0 {
		_ = json.Unmarshal(l.Hits, &hits)
	}
	return gin.H{"id": l.ID, "request_id": l.RequestID, "user_id": l.UserID, "username": l.Username, "request_model": l.RequestModel,
		"protocol": l.Protocol, "action": l.Action, "risk_level": l.RiskLevel, "detect_method": l.DetectMethod,
		"policy_group_id": l.PolicyGroupID, "policy_group": l.PolicyGroup, "evidence": l.Evidence, "confidence": l.Confidence,
		"status_code": l.StatusCode, "hits": hits, "snippet": l.Snippet, "created_at": l.CreatedAt}
}

func (s *Server) listAuditLogs(c *gin.Context) {
	pg := paging(c)
	from, to := timeRange(c)
	q := s.db.Model(&model.AuditLog{}).Where("created_at >= ? AND created_at <= ?", from, to)
	if v := c.Query("action"); v != "" {
		q = q.Where("action = ?", v)
	}
	if v := c.Query("risk_level"); v != "" {
		q = q.Where("risk_level = ?", v)
	}
	if v := c.Query("detect_method"); v != "" {
		q = q.Where("detect_method = ?", v)
	}
	if v := queryUint(c, "policy_group_id"); v > 0 {
		q = q.Where("policy_group_id = ?", v)
	}
	if v := queryUint(c, "user_id"); v > 0 {
		q = q.Where("user_id = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		l := likeEscape(v)
		q = q.Where("request_id LIKE ? OR request_model LIKE ? OR evidence LIKE ? OR username LIKE ?", l, l, l, l)
	}
	var total int64
	q.Count(&total)
	var rows []model.AuditLog
	if err := q.Order("id DESC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, auditLogView(&rows[i]))
	}
	listResp(c, out, total)
}

func (s *Server) getAuditLog(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var l model.AuditLog
	if err := s.db.First(&l, id).Error; err != nil {
		notFound(c)
		return
	}
	c.JSON(200, auditLogView(&l))
}

func (s *Server) complianceTest(c *gin.Context) {
	var in struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		badRequest(c, "请输入文本")
		return
	}
	if s.eng.Compliance == nil {
		fail(c, 503, "unavailable", "合规引擎未启用")
		return
	}
	v := s.eng.Compliance.Test(c.Request.Context(), in.Text)
	var hits any = []any{}
	if len(v.Hits) > 0 {
		_ = json.Unmarshal(v.Hits, &hits)
	}
	c.JSON(200, gin.H{"hit": v.Hit, "block": v.Block, "action": v.Action, "risk_level": v.RiskLevel, "detect_method": v.DetectMethod,
		"policy_group": v.PolicyGroup, "policy_group_id": v.PolicyID, "evidence": v.Evidence, "confidence": v.Confidence, "hits": hits})
}

var _ = gorm.ErrRecordNotFound
