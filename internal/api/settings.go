package api

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"yzapi/internal/essink"
	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

const masked = "******"

func (s *Server) getSettings(c *gin.Context) {
	all := s.st.Get()
	es := all.Elasticsearch
	if es.APIKey != "" {
		es.APIKey = masked
	}
	if es.Password != "" {
		es.Password = masked
	}
	c.JSON(200, gin.H{"basic": all.Basic, "performance": all.Performance, "vector": all.Vector,
		"smart_route": all.SmartRoute, "compliance": all.Compliance, "elasticsearch": es})
}

func (s *Server) putBasic(c *gin.Context) {
	var in settings.Basic
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.BaseURL == "" || (!strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://")) {
		badRequest(c, "接入地址必须为 http(s):// 开头")
		return
	}
	if !strings.HasSuffix(in.BaseURL, "/v1") {
		badRequest(c, "接入地址需以 /v1 结尾")
		return
	}
	if in.LogRetentionDays < 1 || in.LogRetentionDays > 365 {
		badRequest(c, "日志保留天数范围 1-365")
		return
	}
	if strings.TrimSpace(in.SiteName) == "" {
		in.SiteName = "YZ AI Gateway"
	}
	if err := s.st.SetBasic(in); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, in)
}

func (s *Server) putPerformance(c *gin.Context) {
	var in settings.Performance
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.MaxConcurrency < 0 || in.QueueSize < 0 || in.QueueTimeoutSec < 0 || in.RequestTimeoutSec < 0 ||
		in.StreamIdleTimeout < 0 || in.MaxBodyKB < 0 || in.CooldownSec < 0 || in.MaxRetries < 0 || in.UpstreamConnTimeout < 0 {
		badRequest(c, "数值不能为负数")
		return
	}
	if in.MaxRetries == 0 {
		in.MaxRetries = 3
	}
	if in.MaxBodyKB == 0 {
		in.MaxBodyKB = 20480
	}
	if err := s.st.SetPerformance(in); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, in)
}

func (s *Server) vectorClient(accountID uint, mdl string) (*vector.Client, string) {
	var a model.Account
	if err := s.db.First(&a, accountID).Error; err != nil {
		return nil, "向量账号不存在"
	}
	if a.Type != model.TypeEmbedding {
		return nil, "所选账号不是向量类型"
	}
	if !a.Enabled {
		return nil, "所选账号已禁用"
	}
	key, _ := s.cipher.Decrypt(a.APIKeyEnc)
	if mdl == "" {
		mdl = a.TestModel
	}
	// map request model to upstream name if a mapping exists
	var mm model.ModelMapping
	if err := s.db.Where("account_id = ? AND request_model = ?", a.ID, mdl).First(&mm).Error; err == nil && mm.UpstreamModel != "" {
		mdl = mm.UpstreamModel
	}
	return vector.New(a.BaseURL, key, mdl, s.gw.HTTPClient()), ""
}

// VectorEmbedFunc returns an embedding function bound to the current vector settings.
func (s *Server) VectorEmbedFunc() func(ctx context.Context, inputs []string) ([][]float32, error) {
	return func(ctx context.Context, inputs []string) ([][]float32, error) {
		v := s.st.Get().Vector
		cl, msg := s.vectorClient(v.AccountID, v.Model)
		if cl == nil {
			return nil, errVector(msg)
		}
		return cl.Embed(ctx, inputs)
	}
}

type errVector string

func (e errVector) Error() string { return "向量服务不可用: " + string(e) }

func (s *Server) putVector(c *gin.Context) {
	var in settings.Vector
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.AccountID > 0 {
		if _, msg := s.vectorClient(in.AccountID, in.Model); msg != "" {
			badRequest(c, msg)
			return
		}
	}
	if err := s.st.SetVector(in); err != nil {
		serverError(c, err)
		return
	}
	if s.eng.Route != nil {
		_ = s.eng.Route.Reload()
	}
	if s.eng.Compliance != nil {
		_ = s.eng.Compliance.Reload()
	}
	c.JSON(200, in)
}

func (s *Server) testVector(c *gin.Context) {
	var in settings.Vector
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	cl, msg := s.vectorClient(in.AccountID, in.Model)
	if cl == nil {
		c.JSON(200, gin.H{"ok": false, "message": msg})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	t0 := time.Now()
	vs, err := cl.Embed(ctx, []string{"你好，世界"})
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "message": err.Error(), "latency_ms": time.Since(t0).Milliseconds()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "dim": len(vs[0]), "latency_ms": time.Since(t0).Milliseconds(), "message": "连接正常", "model": cl.Model})
}

func (s *Server) putSmartRoute(c *gin.Context) {
	var in settings.SmartRoute
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.VirtualModel = strings.TrimSpace(in.VirtualModel)
	if in.Enabled {
		if in.VirtualModel == "" {
			badRequest(c, "虚拟模型名称不能为空")
			return
		}
		if in.SimpleGroupID == 0 || in.ComplexGroupID == 0 {
			badRequest(c, "请选择简单与复杂请求模型组")
			return
		}
		var n int64
		s.db.Model(&model.ModelGroup{}).Where("id IN ? AND type = ?", []uint{in.SimpleGroupID, in.ComplexGroupID}, model.TypeText).Count(&n)
		if (in.SimpleGroupID == in.ComplexGroupID && n != 1) || (in.SimpleGroupID != in.ComplexGroupID && n != 2) {
			badRequest(c, "模型组不存在或不是文本类型")
			return
		}
		if s.st.Get().Vector.AccountID == 0 {
			badRequest(c, "请先在「向量服务」中配置向量账号")
			return
		}
	}
	if in.Threshold < 0 || in.Threshold > 1 || in.ConfidenceGap < 0 || in.ConfidenceGap > 1 {
		badRequest(c, "阈值范围 0-1")
		return
	}
	if in.TopK <= 0 {
		in.TopK = 5
	}
	if in.TopK > 50 {
		in.TopK = 50
	}
	if err := s.st.SetSmartRoute(in); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, in)
}

func (s *Server) putCompliance(c *gin.Context) {
	var in settings.Compliance
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.SemanticThreshold < 0 || in.SemanticThreshold > 1 {
		badRequest(c, "语义阈值范围 0-1")
		return
	}
	if err := s.st.SetCompliance(in); err != nil {
		serverError(c, err)
		return
	}
	if s.eng.Compliance != nil {
		_ = s.eng.Compliance.Reload()
	}
	c.JSON(200, in)
}

func (s *Server) putElasticsearch(c *gin.Context) {
	var in settings.Elasticsearch
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	curES := s.st.Get().Elasticsearch
	if in.APIKey == masked {
		in.APIKey = curES.APIKey
	}
	if in.Password == masked {
		in.Password = curES.Password
	}
	in.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
	if in.Enabled && in.URL == "" {
		badRequest(c, "Elasticsearch 地址不能为空")
		return
	}
	if in.IndexPrefix == "" {
		in.IndexPrefix = "yzapi"
	}
	if err := s.st.SetElasticsearch(in); err != nil {
		serverError(c, err)
		return
	}
	out := in
	if out.APIKey != "" {
		out.APIKey = masked
	}
	if out.Password != "" {
		out.Password = masked
	}
	c.JSON(200, out)
}

func (s *Server) testElasticsearch(c *gin.Context) {
	var in settings.Elasticsearch
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	curES := s.st.Get().Elasticsearch
	if in.APIKey == masked {
		in.APIKey = curES.APIKey
	}
	if in.Password == masked {
		in.Password = curES.Password
	}
	if s.eng.ES == nil {
		c.JSON(200, gin.H{"ok": false, "message": "Elasticsearch 模块未启用"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	ver, err := s.eng.ES.Test(ctx, in)
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "version": ver, "message": "连接正常"})
}

func (s *Server) esStatus(c *gin.Context) {
	if s.eng.ES == nil {
		c.JSON(200, essink.Status{})
		return
	}
	c.JSON(200, s.eng.ES.Status())
}
