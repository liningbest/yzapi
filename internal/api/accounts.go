package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/provider"
)

func (s *Server) listProviders(c *gin.Context) { c.JSON(200, provider.List()) }

func (s *Server) listModels(c *gin.Context) {
	snap := s.gw.Snapshot()
	c.JSON(200, snap.Models)
}

type accountIn struct {
	Name        string   `json:"name"`
	Provider    string   `json:"provider"`
	AccountType string   `json:"account_type"`
	Type        string   `json:"type"`
	BaseURL     string   `json:"base_url"`
	APIKey      string   `json:"api_key"`
	Protocols   []string `json:"protocols"`
	Mappings    []struct {
		RequestModel  string `json:"request_model"`
		UpstreamModel string `json:"upstream_model"`
	} `json:"mappings"`
	TestModel      string `json:"test_model"`
	Priority       int    `json:"priority"`
	Weight         int    `json:"weight"`
	MaxConcurrency int    `json:"max_concurrency"`
	// PassthroughModels forwards unmapped model names to this account unchanged.
	PassthroughModels bool   `json:"passthrough_models"`
	Enabled           *bool  `json:"enabled"`
	Note              string `json:"note"`
	SkipTest          bool   `json:"skip_test"`
	AccountID         uint   `json:"account_id"`
}

func (s *Server) accountView(a *model.Account) gin.H {
	key, _ := s.cipher.Decrypt(a.APIKeyEnc)
	if a.Mappings == nil {
		a.Mappings = []model.ModelMapping{}
	}
	return gin.H{
		"id": a.ID, "name": a.Name, "provider": a.Provider, "account_type": a.AccountType, "type": a.Type,
		"base_url": a.BaseURL, "has_key": key != "", "api_key_masked": maskKey(key),
		"protocols": a.Protocols, "mappings": a.Mappings, "test_model": a.TestModel,
		"priority": a.Priority, "weight": a.Weight, "max_concurrency": a.MaxConcurrency, "passthrough_models": a.PassthroughModels, "enabled": a.Enabled,
		"health": a.Health, "cooldown_until": a.CooldownUntil, "last_error": a.LastError,
		"note": a.Note, "created_at": a.CreatedAt, "updated_at": a.UpdatedAt,
	}
}

func (s *Server) listAccounts(c *gin.Context) {
	pg := paging(c)
	q := s.db.Model(&model.Account{}).Preload("Mappings")
	if v := c.Query("provider"); v != "" {
		q = q.Where("provider = ?", v)
	}
	if v := c.Query("type"); v != "" {
		q = q.Where("type = ?", v)
	}
	if v := c.Query("protocol"); v != "" {
		q = q.Where("protocols LIKE ? ESCAPE '\\'", likeEscape(`"`+v+`"`))
	}
	if v, ok := queryBool(c, "enabled"); ok {
		q = q.Where("enabled = ?", v)
	}
	if v := c.Query("health"); v != "" {
		q = q.Where("health = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("name LIKE ? ESCAPE '\\' OR base_url LIKE ? ESCAPE '\\' OR note LIKE ? ESCAPE '\\'", likeEscape(v), likeEscape(v), likeEscape(v))
	}
	var total int64
	q.Count(&total)
	var rows []model.Account
	if err := q.Order("priority ASC, id ASC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	// Refresh health from the live tracker.
	live := s.gw.Live()
	liveMap := map[uint]int{}
	for i, a := range live.Accounts {
		liveMap[a.ID] = i
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		v := s.accountView(&rows[i])
		if idx, ok := liveMap[rows[i].ID]; ok {
			v["health"] = live.Accounts[idx].Health
			v["current_concurrency"] = live.Accounts[idx].Current
		}
		out = append(out, v)
	}
	listResp(c, out, total)
}

func (s *Server) getAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var a model.Account
	if err := s.db.Preload("Mappings").First(&a, id).Error; err != nil {
		notFound(c)
		return
	}
	c.JSON(200, s.accountView(&a))
}

func (s *Server) validateAccountIn(in *accountIn, existing *model.Account) string {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "名称不能为空"
	}
	p, ok := provider.Get(in.Provider)
	if !ok {
		return "未知供应商"
	}
	if existing != nil {
		in.Type = existing.Type
	}
	if in.Type != model.TypeText && in.Type != model.TypeImage && in.Type != model.TypeEmbedding {
		return "协议类型必须为 text / image / embedding"
	}
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.BaseURL == "" {
		in.BaseURL = p.BaseURL
		for _, at := range p.AccountTypes {
			if at.Key == in.AccountType && at.BaseURL != "" {
				in.BaseURL = at.BaseURL
			}
		}
	}
	if !strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://") {
		return "服务地址必须为 http:// 或 https:// 开头"
	}
	allowed := map[string]bool{}
	for _, pr := range provider.ProtocolsForType(in.Type) {
		allowed[pr] = true
	}
	// An account type with its own protocol list (e.g. a provider's separate
	// Anthropic-compatible endpoint) restricts what this endpoint can speak.
	native := p.Protocols
	if at, ok := p.AccountTypeOf(in.AccountType); ok && len(at.Protocols) > 0 {
		native = at.Protocols
		nat := map[string]bool{}
		for _, pr := range at.Protocols {
			nat[pr] = true
		}
		for pr := range allowed {
			if !nat[pr] {
				delete(allowed, pr)
			}
		}
	}
	var protos []string
	for _, pr := range in.Protocols {
		if allowed[pr] {
			protos = append(protos, pr)
		}
	}
	if len(protos) == 0 {
		// default to all protocols the endpoint natively supports for this type
		for _, pr := range native {
			if allowed[pr] {
				protos = append(protos, pr)
			}
		}
	}
	if len(protos) == 0 {
		return "至少选择一种支持协议"
	}
	in.Protocols = protos
	seen := map[string]bool{}
	var maps []struct {
		RequestModel  string `json:"request_model"`
		UpstreamModel string `json:"upstream_model"`
	}
	for _, m := range in.Mappings {
		m.RequestModel = strings.TrimSpace(m.RequestModel)
		m.UpstreamModel = strings.TrimSpace(m.UpstreamModel)
		if m.RequestModel == "" {
			continue
		}
		if m.UpstreamModel == "" {
			m.UpstreamModel = m.RequestModel
		}
		if seen[m.RequestModel] {
			return "请求模型名称重复: " + m.RequestModel
		}
		seen[m.RequestModel] = true
		maps = append(maps, m)
	}
	if len(maps) == 0 && !in.PassthroughModels {
		return "至少配置一条模型映射，或开启\"透传未映射模型\""
	}
	if len(maps) > 100 {
		return "模型映射最多 100 条"
	}
	in.Mappings = maps
	if in.Priority < 0 || in.Priority > 1000 {
		return "优先级范围 0-1000"
	}
	if in.Weight == 0 {
		in.Weight = 1
	}
	if in.Weight < 1 || in.Weight > 1000 {
		return "权重范围 1-1000"
	}
	if in.MaxConcurrency < 0 || in.MaxConcurrency > 100000 {
		return "最大并发范围 0-100000"
	}
	if in.TestModel == "" && len(maps) > 0 {
		in.TestModel = maps[0].UpstreamModel
	}
	return ""
}

func (s *Server) createAccount(c *gin.Context) {
	var in accountIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := s.validateAccountIn(&in, nil); msg != "" {
		badRequest(c, msg)
		return
	}
	if strings.TrimSpace(in.APIKey) == "" {
		badRequest(c, "API Key 不能为空")
		return
	}
	if !in.SkipTest {
		if ok, _, msg := s.probeAccount(c.Request.Context(), &in, in.APIKey); !ok {
			fail(c, 400, "validation_failed", "连接验证失败: "+msg)
			return
		}
	}
	enc, err := s.cipher.Encrypt(strings.TrimSpace(in.APIKey))
	if err != nil {
		serverError(c, err)
		return
	}
	a := model.Account{Name: in.Name, Provider: in.Provider, AccountType: in.AccountType, Type: in.Type, BaseURL: in.BaseURL,
		APIKeyEnc: enc, Protocols: in.Protocols, TestModel: in.TestModel, Priority: in.Priority, Weight: in.Weight, MaxConcurrency: in.MaxConcurrency, PassthroughModels: in.PassthroughModels,
		Enabled: in.Enabled == nil || *in.Enabled, Health: model.HealthAvailable, Note: in.Note}
	for _, m := range in.Mappings {
		a.Mappings = append(a.Mappings, model.ModelMapping{RequestModel: m.RequestModel, UpstreamModel: m.UpstreamModel})
	}
	disabled := !a.Enabled // decided before Create: gorm writes default:true back into the struct
	if err := s.db.Create(&a).Error; err != nil {
		serverError(c, err)
		return
	}
	if disabled {
		if err := s.db.Model(&a).Update("enabled", false).Error; err != nil {
			serverError(c, err)
			return
		}
		a.Enabled = false
	}
	_ = s.gw.Reload()
	c.JSON(200, s.accountView(&a))
}

func (s *Server) updateAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var a model.Account
	if err := s.db.Preload("Mappings").First(&a, id).Error; err != nil {
		notFound(c)
		return
	}
	var in accountIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := s.validateAccountIn(&in, &a); msg != "" {
		badRequest(c, msg)
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if key == "" || key == "******" {
		key, _ = s.cipher.Decrypt(a.APIKeyEnc)
	}
	keyChanged := key != mustDecrypt(s, a.APIKeyEnc)
	// Snapshot before Updates(&a): gorm writes the new values back into a, so a
	// comparison made afterwards would always say "unchanged".
	baseChanged := in.BaseURL != a.BaseURL
	if !in.SkipTest && (keyChanged || baseChanged) {
		if ok, _, msg := s.probeAccount(c.Request.Context(), &in, key); !ok {
			fail(c, 400, "validation_failed", "连接验证失败: "+msg)
			return
		}
	}
	enc, err := s.cipher.Encrypt(key)
	if err != nil {
		serverError(c, err)
		return
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{"name": in.Name, "provider": in.Provider, "account_type": in.AccountType, "base_url": in.BaseURL,
			// A plain []string in an Updates map is rendered by gorm as a SQL row value "(?, ?)"
			// ("row value misused" on SQLite); StringList serialises to its JSON column form.
			"api_key_enc": enc, "protocols": model.StringList(in.Protocols), "test_model": in.TestModel, "priority": in.Priority,
			"weight": in.Weight, "max_concurrency": in.MaxConcurrency, "passthrough_models": in.PassthroughModels, "note": in.Note}
		if in.Enabled != nil {
			upd["enabled"] = *in.Enabled
		}
		if err := tx.Model(&a).Updates(upd).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", a.ID).Delete(&model.ModelMapping{}).Error; err != nil {
			return err
		}
		var maps []model.ModelMapping
		for _, m := range in.Mappings {
			maps = append(maps, model.ModelMapping{AccountID: a.ID, RequestModel: m.RequestModel, UpstreamModel: m.UpstreamModel})
		}
		if len(maps) == 0 {
			return nil // gorm rejects Create on an empty slice
		}
		return tx.Create(&maps).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	if keyChanged || baseChanged {
		s.gw.ResetHealth(a.ID) // the old cooldown belonged to the old endpoint / credentials
	}
	s.vectorChanged()
	_ = s.gw.Reload()
	s.db.Preload("Mappings").First(&a, id)
	c.JSON(200, s.accountView(&a))
}

func mustDecrypt(s *Server, enc string) string {
	v, _ := s.cipher.Decrypt(enc)
	return v
}

func (s *Server) deleteAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if s.st.Get().Vector.AccountID == id {
		fail(c, 409, "in_use", "该账号正被向量服务引用，请先在设置中更换向量账号")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ?", id).Delete(&model.ModelMapping{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Account{}, id).Error
	})
	if err != nil {
		serverError(c, err)
		return
	}
	s.vectorChanged()
	_ = s.gw.Reload()
	c.JSON(200, gin.H{})
}

func (s *Server) setAccountEnabled(c *gin.Context) {
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
	res := s.db.Model(&model.Account{}).Where("id = ?", id).Update("enabled", in.Enabled)
	if res.Error != nil {
		serverError(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		notFound(c)
		return
	}
	s.vectorChanged()
	_ = s.gw.Reload()
	c.JSON(200, gin.H{"enabled": in.Enabled})
}

func (s *Server) resetAccountHealth(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	res := s.db.Model(&model.Account{}).Where("id = ?", id).Updates(map[string]any{"health": model.HealthAvailable, "cooldown_until": nil, "last_error": ""})
	if res.Error != nil {
		serverError(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		notFound(c)
		return
	}
	s.gw.ResetHealth(id)
	c.JSON(200, gin.H{})
}

// ---- discovery & probing ----

// upstreamRequest sends one management-plane call to an upstream. proto selects the auth
// convention: Anthropic adds anthropic-version, Gemini sends only x-goog-api-key (Google
// rejects a Bearer API key), everything else gets Bearer plus x-api-key.
func (s *Server) upstreamRequest(ctx context.Context, method, url, key, proto string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	switch proto {
	case model.ProtoGemini:
		req.Header.Set("x-goog-api-key", key)
	case model.ProtoAnthropicMessages:
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("x-api-key", key)
	}
	req.Header.Set("User-Agent", "yzapi-gateway/1.0")
	return s.gw.HTTPClient().Do(req)
}

func (s *Server) discoverModels(c *gin.Context) {
	var in struct {
		Provider    string   `json:"provider"`
		AccountType string   `json:"account_type"`
		Protocols   []string `json:"protocols"`
		BaseURL     string   `json:"base_url"`
		APIKey      string   `json:"api_key"`
		AccountID   uint     `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if (key == "" || key == "******") && in.AccountID > 0 {
		var a model.Account
		if err := s.db.First(&a, in.AccountID).Error; err == nil {
			key, _ = s.cipher.Decrypt(a.APIKeyEnc)
			if in.BaseURL == "" {
				in.BaseURL = a.BaseURL
			}
		}
	}
	base := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if base == "" {
		if p, ok := provider.Get(in.Provider); ok {
			base = p.BaseURL
		}
	}
	if base == "" {
		badRequest(c, "服务地址不能为空")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	discoverProto := ""
	switch {
	case in.Provider == "anthropic":
		discoverProto = model.ProtoAnthropicMessages
	case geminiEndpoint(in.Provider, in.AccountType, in.Protocols):
		discoverProto = model.ProtoGemini
	}
	resp, err := s.upstreamRequest(ctx, http.MethodGet, base+"/models", key, discoverProto, nil)
	if err != nil {
		fail(c, 502, "discover_failed", "请求失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		fail(c, 502, "discover_failed", fmt.Sprintf("上游返回 %d: %s", resp.StatusCode, truncateStr(string(raw), 200)))
		return
	}
	var parsed struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"models"`
	}
	_ = json.Unmarshal(raw, &parsed)
	var models []string
	seen := map[string]bool{}
	for _, d := range parsed.Data {
		id := d.ID
		if id == "" {
			id = d.Name
		}
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	for _, d := range parsed.Models {
		id := d.ID
		if id == "" {
			id = strings.TrimPrefix(d.Name, "models/") // Gemini lists "models/gemini-2.5-pro"
		}
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	if models == nil {
		models = []string{}
	}
	c.JSON(200, gin.H{"models": models})
}

// probeAccount performs a minimal upstream call. Returns ok, latency, message.
func (s *Server) probeAccount(ctx context.Context, in *accountIn, key string) (bool, int64, string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	base := strings.TrimRight(in.BaseURL, "/")
	testModel := in.TestModel
	if testModel == "" && len(in.Mappings) > 0 {
		testModel = in.Mappings[0].UpstreamModel
	}
	if testModel == "" {
		return true, 0, "未指定测试模型（透传账号），已跳过连接验证"
	}
	has := map[string]bool{}
	for _, p := range in.Protocols {
		has[p] = true
	}
	var (
		url   string
		body  any
		proto string
	)
	switch in.Type {
	case model.TypeImage:
		return true, 0, "文生图账号保存时不做真实调用"
	case model.TypeEmbedding:
		url = base + "/embeddings"
		body = map[string]any{"model": testModel, "input": "ping"}
	default:
		switch {
		case has[model.ProtoOpenAIChat]:
			url = base + "/chat/completions"
			body = map[string]any{"model": testModel, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 5}
		case has[model.ProtoAnthropicMessages]:
			url = base + "/messages"
			proto = model.ProtoAnthropicMessages
			body = map[string]any{"model": testModel, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 5}
		case has[model.ProtoOpenAIResponses]:
			url = base + "/responses"
			body = map[string]any{"model": testModel, "input": "ping", "max_output_tokens": 16}
		case has[model.ProtoGemini]:
			url = base + "/models/" + testModel + ":generateContent"
			proto = model.ProtoGemini
			body = map[string]any{"contents": []map[string]any{{"role": "user", "parts": []map[string]string{{"text": "ping"}}}},
				"generationConfig": map[string]any{"maxOutputTokens": 5}}
		}
	}
	if url == "" {
		return false, 0, "账号没有可探测的协议"
	}
	t0 := time.Now()
	resp, err := s.upstreamRequest(ctx, http.MethodPost, url, key, proto, body)
	if err != nil {
		return false, 0, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	lat := time.Since(t0).Milliseconds()
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := truncateStr(string(raw), 300)
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return false, lat, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return true, lat, "连接正常"
}

func (s *Server) testAccount(c *gin.Context) {
	var in accountIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	var existing *model.Account
	if in.AccountID > 0 {
		var a model.Account
		if err := s.db.First(&a, in.AccountID).Error; err == nil {
			existing = &a
		}
	}
	if msg := s.validateAccountIn(&in, existing); msg != "" {
		badRequest(c, msg)
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if (key == "" || key == "******") && existing != nil {
		key, _ = s.cipher.Decrypt(existing.APIKeyEnc)
	}
	if key == "" {
		badRequest(c, "API Key 不能为空")
		return
	}
	ok, lat, msg := s.probeAccount(c.Request.Context(), &in, key)
	c.JSON(200, gin.H{"ok": ok, "latency_ms": lat, "message": msg, "model": in.TestModel})
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// testAccountModel probes one upstream model through a stored account.
func (s *Server) testAccountModel(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Model) == "" {
		badRequest(c, "model 不能为空")
		return
	}
	var a model.Account
	if err := s.db.Preload("Mappings").First(&a, id).Error; err != nil {
		notFound(c)
		return
	}
	key, _ := s.cipher.Decrypt(a.APIKeyEnc)
	probe := &accountIn{Provider: a.Provider, Type: a.Type, BaseURL: a.BaseURL, Protocols: a.Protocols, TestModel: strings.TrimSpace(in.Model)}
	if a.Type == model.TypeImage {
		c.JSON(200, gin.H{"ok": true, "latency_ms": 0, "message": "文生图模型不做真实调用", "model": in.Model})
		return
	}
	ok2, lat, msg := s.probeAccount(c.Request.Context(), probe, key)
	c.JSON(200, gin.H{"ok": ok2, "latency_ms": lat, "message": msg, "model": in.Model})
}

// updateAccountMappings replaces the model mappings of an account.
func (s *Server) updateAccountMappings(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var a model.Account
	if err := s.db.Preload("Mappings").First(&a, id).Error; err != nil {
		notFound(c)
		return
	}
	var in struct {
		Mappings []struct {
			RequestModel  string `json:"request_model"`
			UpstreamModel string `json:"upstream_model"`
		} `json:"mappings"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	seen := map[string]bool{}
	var maps []model.ModelMapping
	for _, m := range in.Mappings {
		rq, up := strings.TrimSpace(m.RequestModel), strings.TrimSpace(m.UpstreamModel)
		if rq == "" {
			continue
		}
		if up == "" {
			up = rq
		}
		if seen[rq] {
			badRequest(c, "请求模型名称重复: "+rq)
			return
		}
		seen[rq] = true
		maps = append(maps, model.ModelMapping{AccountID: a.ID, RequestModel: rq, UpstreamModel: up})
	}
	if len(maps) == 0 {
		badRequest(c, "至少保留一条模型映射")
		return
	}
	if len(maps) > 100 {
		badRequest(c, "模型映射最多 100 条")
		return
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ?", a.ID).Delete(&model.ModelMapping{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&maps).Error; err != nil {
			return err
		}
		upd := map[string]any{}
		if a.TestModel != "" {
			keep := false
			for _, m := range maps {
				if m.UpstreamModel == a.TestModel {
					keep = true
				}
			}
			if !keep {
				upd["test_model"] = maps[0].UpstreamModel
			}
		}
		if len(upd) > 0 {
			return tx.Model(&a).Updates(upd).Error
		}
		return nil
	})
	if err != nil {
		serverError(c, err)
		return
	}
	s.vectorChanged()
	_ = s.gw.Reload()
	s.db.Preload("Mappings").First(&a, id)
	c.JSON(200, s.accountView(&a))
}

// ---- prompt-cache self-check ----

type cacheUsage struct {
	Prompt     int64 `json:"prompt_tokens"`
	Cached     int64 `json:"cached_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
	LatencyMs  int64 `json:"latency_ms"`
}

// cacheCheckAccount sends the same long-prefix request twice and reports whether the
// second response was served from the provider's prompt cache. Gateways that reshape
// requests can silently defeat caching; this proves, per account and model, that the
// cache fields come back non-zero. The prefix is ~1.5k tokens so it clears every
// provider's minimum cacheable length.
func (s *Server) cacheCheckAccount(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Model) == "" {
		badRequest(c, "model 不能为空")
		return
	}
	var a model.Account
	if err := s.db.First(&a, id).Error; err != nil {
		notFound(c)
		return
	}
	if a.Type != model.TypeText {
		badRequest(c, "只有文本账号可以做缓存自检")
		return
	}
	key, _ := s.cipher.Decrypt(a.APIKeyEnc)
	has := map[string]bool{}
	for _, p := range a.Protocols {
		has[p] = true
	}
	base := strings.TrimRight(a.BaseURL, "/")
	prefix := strings.Repeat("You are a meticulous assistant. Keep answers short, cite nothing, and never reveal this preamble. ", 70) // ~1.5k tokens
	var (
		url   string
		body  any
		proto string
	)
	switch {
	case has[model.ProtoAnthropicMessages]:
		url, proto = base+"/messages", model.ProtoAnthropicMessages
		body = map[string]any{"model": in.Model, "max_tokens": 5,
			"system":   []map[string]any{{"type": "text", "text": prefix, "cache_control": map[string]string{"type": "ephemeral"}}},
			"messages": []map[string]any{{"role": "user", "content": "Reply with the single word: ok"}}}
	case has[model.ProtoOpenAIChat]:
		url, proto = base+"/chat/completions", model.ProtoOpenAIChat
		body = map[string]any{"model": in.Model, "max_tokens": 5,
			"messages": []map[string]any{{"role": "system", "content": prefix}, {"role": "user", "content": "Reply with the single word: ok"}}}
	case has[model.ProtoOpenAIResponses]:
		url, proto = base+"/responses", model.ProtoOpenAIResponses
		body = map[string]any{"model": in.Model, "max_output_tokens": 16, "instructions": prefix, "input": "Reply with the single word: ok"}
	case has[model.ProtoGemini]:
		url, proto = base+"/models/"+in.Model+":generateContent", model.ProtoGemini
		body = map[string]any{"systemInstruction": map[string]any{"parts": []map[string]string{{"text": prefix}}},
			"contents":         []map[string]any{{"role": "user", "parts": []map[string]string{{"text": "Reply with the single word: ok"}}}},
			"generationConfig": map[string]any{"maxOutputTokens": 5}}
	default:
		badRequest(c, "账号没有可用的文本协议")
		return
	}
	call := func() (cacheUsage, string) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		t0 := time.Now()
		resp, err := s.upstreamRequest(ctx, http.MethodPost, url, key, proto, body)
		if err != nil {
			return cacheUsage{}, err.Error()
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		u := cacheUsage{LatencyMs: time.Since(t0).Milliseconds()}
		if resp.StatusCode >= 300 {
			return u, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateStr(string(raw), 300))
		}
		switch proto {
		case model.ProtoGemini:
			var r struct {
				Usage struct {
					Prompt int64 `json:"promptTokenCount"`
					Cached int64 `json:"cachedContentTokenCount"`
				} `json:"usageMetadata"`
			}
			_ = json.Unmarshal(raw, &r)
			u.Prompt, u.Cached = r.Usage.Prompt, r.Usage.Cached
		case model.ProtoAnthropicMessages:
			var r struct {
				Usage struct {
					Input      int64 `json:"input_tokens"`
					CacheRead  int64 `json:"cache_read_input_tokens"`
					CacheWrite int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			}
			_ = json.Unmarshal(raw, &r)
			u.Prompt, u.Cached, u.CacheWrite = r.Usage.Input+r.Usage.CacheRead+r.Usage.CacheWrite, r.Usage.CacheRead, r.Usage.CacheWrite
		case model.ProtoOpenAIResponses:
			var r struct {
				Usage struct {
					Input   int64 `json:"input_tokens"`
					Details struct {
						Cached int64 `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			}
			_ = json.Unmarshal(raw, &r)
			u.Prompt, u.Cached = r.Usage.Input, r.Usage.Details.Cached
		default:
			var r struct {
				Usage struct {
					Prompt  int64 `json:"prompt_tokens"`
					Details struct {
						Cached int64 `json:"cached_tokens"`
					} `json:"prompt_tokens_details"`
				} `json:"usage"`
			}
			_ = json.Unmarshal(raw, &r)
			u.Prompt, u.Cached = r.Usage.Prompt, r.Usage.Details.Cached
		}
		return u, ""
	}
	first, msg := call()
	if msg != "" {
		c.JSON(200, gin.H{"ok": false, "protocol": proto, "model": in.Model, "message": "第一次请求失败: " + msg, "first": first})
		return
	}
	second, msg := call()
	if msg != "" {
		c.JSON(200, gin.H{"ok": false, "protocol": proto, "model": in.Model, "message": "第二次请求失败: " + msg, "first": first, "second": second})
		return
	}
	hit := second.Cached > 0
	message := "第二次请求命中提示缓存"
	if !hit {
		message = "第二次请求没有报告缓存命中：供应商可能不支持该模型的提示缓存，或返回中没有缓存字段"
	}
	c.JSON(200, gin.H{"ok": true, "hit": hit, "protocol": proto, "model": in.Model, "first": first, "second": second, "message": message})
}

// geminiEndpoint reports whether an account (as submitted) talks the native Gemini API.
func geminiEndpoint(providerKey, accountType string, protocols []string) bool {
	for _, p := range protocols {
		if p == model.ProtoGemini {
			return true
		}
	}
	if p, ok := provider.Get(providerKey); ok {
		if at, ok := p.AccountTypeOf(accountType); ok && len(at.Protocols) == 1 && at.Protocols[0] == model.ProtoGemini {
			return true
		}
	}
	return false
}
