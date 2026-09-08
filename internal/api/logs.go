package api

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
)

func applyLogFilters(c *gin.Context, q *gorm.DB, scopedUser uint) *gorm.DB {
	from, to := timeRange(c)
	q = q.Where("created_at >= ? AND created_at <= ?", from, to)
	if scopedUser > 0 {
		q = q.Where("user_id = ?", scopedUser)
	} else if v := queryUint(c, "user_id"); v > 0 {
		q = q.Where("user_id = ?", v)
	}
	if v := queryUint(c, "group_id"); v > 0 {
		q = q.Where("group_id = ?", v)
	}
	if v := queryUint(c, "account_id"); v > 0 {
		q = q.Where("account_id = ?", v)
	}
	if v := queryUint(c, "api_key_id"); v > 0 {
		q = q.Where("api_key_id = ?", v)
	}
	if v := c.Query("provider"); v != "" {
		q = q.Where("provider = ?", v)
	}
	if v := c.Query("api_type"); v != "" {
		q = q.Where("api_type = ?", v)
	}
	if v := c.Query("result"); v != "" {
		q = q.Where("result = ?", v)
	}
	if v := c.Query("status_code"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q = q.Where("status_code = ?", n)
		}
	}
	if v := c.Query("model"); v != "" {
		q = q.Where("request_model = ?", v)
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		l := likeEscape(v)
		q = q.Where("request_id LIKE ? OR request_model LIKE ? OR upstream_model LIKE ? OR error LIKE ?", l, l, l, l)
	}
	return q
}

func (s *Server) listLogs(c *gin.Context) {
	pg := paging(c)
	q := applyLogFilters(c, s.db.Model(&model.CallLog{}), 0)
	var total int64
	q.Count(&total)
	var rows []model.CallLog
	if err := q.Order("id DESC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	if rows == nil {
		rows = []model.CallLog{}
	}
	listResp(c, rows, total)
}

func (s *Server) getLog(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var l model.CallLog
	if err := s.db.First(&l, id).Error; err != nil {
		notFound(c)
		return
	}
	c.JSON(200, l)
}

func (s *Server) logFilters(c *gin.Context) {
	var users []struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
	}
	s.db.Model(&model.User{}).Select("id, username").Order("username").Scan(&users)
	var accounts []struct {
		ID       uint   `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}
	s.db.Model(&model.Account{}).Select("id, name, provider").Order("name").Scan(&accounts)
	var groups []struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	s.db.Model(&model.UserGroup{}).Select("id, name").Order("name").Scan(&groups)
	var providers []string
	s.db.Model(&model.Account{}).Distinct("provider").Order("provider").Pluck("provider", &providers)
	var models []string
	s.db.Model(&model.ModelMapping{}).Distinct("request_model").Order("request_model").Pluck("request_model", &models)
	if snap := s.gw.Snapshot(); snap.VirtualModel != "" {
		models = append([]string{snap.VirtualModel}, models...)
	}
	if users == nil {
		users = []struct {
			ID       uint   `json:"id"`
			Username string `json:"username"`
		}{}
	}
	c.JSON(200, gin.H{"users": users, "accounts": accounts, "groups": groups, "providers": orEmpty(providers), "models": orEmpty(models)})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- user console ----

func (s *Server) userLogs(c *gin.Context) {
	pg := paging(c)
	q := applyLogFilters(c, s.db.Model(&model.CallLog{}), cur(c).ID)
	var total int64
	q.Count(&total)
	var rows []model.CallLog
	if err := q.Order("id DESC").Offset(pg.Offset).Limit(pg.Size).Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, l := range rows {
		out = append(out, gin.H{
			"id": l.ID, "request_id": l.RequestID, "api_key_id": l.APIKeyID, "api_key_name": l.APIKeyName,
			"provider": l.Provider, "request_model": l.RequestModel, "upstream_model": l.UpstreamModel, "api_type": l.APIType,
			"client_protocol": l.ClientProtocol, "stream": l.Stream,
			"prompt_tokens": l.PromptTokens, "completion_tokens": l.CompletionTokens, "total_tokens": l.TotalTokens,
			"cached_tokens": l.CachedTokens, "tokens_known": l.TokensKnown, "result": l.Result, "status_code": l.StatusCode,
			"latency_ms": l.LatencyMs, "first_byte_ms": l.FirstByteMs, "error": l.Error, "route_label": l.RouteLabel, "created_at": l.CreatedAt,
		})
	}
	listResp(c, out, total)
}
