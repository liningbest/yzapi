package api

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/provider"
)

type usageRow struct {
	Hour             time.Time
	UserID           uint
	GroupID          uint
	APIKeyID         uint
	AccountID        uint
	Provider         string
	RequestModel     string
	ModelGroup       string
	APIType          string
	Requests         int64
	Attempts         int64
	Success          int64
	Failed           int64
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	CachedTokens     int64
	UnknownUsage     int64
	LatencyMs        int64
}

type dist struct {
	Key              string `json:"key"`
	Name             string `json:"name"`
	Requests         int64  `json:"requests"`
	Attempts         int64  `json:"attempts"` // upstream attempts booked on this dimension (tokens follow attempts)
	TotalTokens      int64  `json:"total_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	UnknownUsage     int64  `json:"unknown_usage"`
}

type trendPoint struct {
	Time             time.Time        `json:"time"`
	Requests         int64            `json:"requests"`
	Success          int64            `json:"success"`
	Failed           int64            `json:"failed"`
	TotalTokens      int64            `json:"total_tokens"`
	PromptTokens     int64            `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
	CachedTokens     int64            `json:"cached_tokens"`
	Series           map[string]int64 `json:"series"`
}

// names resolves display names for ids.
type nameMaps struct {
	users, groups, accounts, keys map[uint]string
}

func (s *Server) loadNames() nameMaps {
	nm := nameMaps{users: map[uint]string{}, groups: map[uint]string{}, accounts: map[uint]string{}, keys: map[uint]string{}}
	var us []struct {
		ID   uint
		Name string
	}
	s.db.Model(&model.User{}).Select("id, username AS name").Scan(&us)
	for _, u := range us {
		nm.users[u.ID] = u.Name
	}
	us = nil
	s.db.Model(&model.UserGroup{}).Select("id, name").Scan(&us)
	for _, u := range us {
		nm.groups[u.ID] = u.Name
	}
	us = nil
	s.db.Model(&model.Account{}).Select("id, name").Scan(&us)
	for _, u := range us {
		nm.accounts[u.ID] = u.Name
	}
	us = nil
	s.db.Model(&model.APIKey{}).Select("id, name").Scan(&us)
	for _, u := range us {
		nm.keys[u.ID] = u.Name
	}
	return nm
}

func nameOr(m map[uint]string, id uint, deleted string) string {
	if id == 0 {
		return "-"
	}
	if n, ok := m[id]; ok {
		return n
	}
	return "#" + strconv.FormatUint(uint64(id), 10) + " " + deleted
}

func applyUsageFilters(c *gin.Context, q *gorm.DB, scopedUser uint) (*gorm.DB, time.Time, time.Time) {
	from, to := timeRange(c)
	q = q.Where("hour >= ? AND hour <= ?", from.Truncate(time.Hour), to)
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
	if v := c.Query("model"); v != "" {
		q = q.Where("request_model = ?", v)
	}
	return q, from, to
}

func (s *Server) usageReport(c *gin.Context, scopedUser uint) gin.H {
	q, from, to := applyUsageFilters(c, s.db.Model(&model.UsageHourly{}), scopedUser)
	var rows []usageRow
	q.Find(&rows)
	nm := s.loadNames()
	groupBy := c.DefaultQuery("group_by", "model")
	byDay := to.Sub(from) > 48*time.Hour

	summary := dist{}
	trend := map[int64]*trendPoint{}
	agg := map[string]map[string]*dist{
		"provider": {}, "model": {}, "model_group": {}, "account": {}, "group": {}, "user": {}, "api_key": {},
	}
	add := func(kind, key, name string, r *usageRow) {
		d, ok := agg[kind][key]
		if !ok {
			d = &dist{Key: key, Name: name}
			agg[kind][key] = d
		}
		d.Requests += r.Requests
		d.Attempts += r.Attempts
		d.TotalTokens += r.TotalTokens
		d.CachedTokens += r.CachedTokens
		d.PromptTokens += r.PromptTokens
		d.CompletionTokens += r.CompletionTokens
		d.UnknownUsage += r.UnknownUsage
	}
	for i := range rows {
		r := &rows[i]
		summary.Requests += r.Requests
		summary.TotalTokens += r.TotalTokens
		summary.CachedTokens += r.CachedTokens
		summary.PromptTokens += r.PromptTokens
		summary.CompletionTokens += r.CompletionTokens
		summary.UnknownUsage += r.UnknownUsage

		bucket := r.Hour
		if byDay {
			y, m, d := r.Hour.Local().Date()
			bucket = time.Date(y, m, d, 0, 0, 0, 0, time.Local)
		}
		tp, ok := trend[bucket.Unix()]
		if !ok {
			tp = &trendPoint{Time: bucket, Series: map[string]int64{}}
			trend[bucket.Unix()] = tp
		}
		tp.Requests += r.Requests
		tp.Success += r.Success
		tp.Failed += r.Failed
		tp.TotalTokens += r.TotalTokens
		tp.PromptTokens += r.PromptTokens
		tp.CompletionTokens += r.CompletionTokens
		tp.CachedTokens += r.CachedTokens
		sk := r.RequestModel
		if groupBy == "api_key" {
			sk = nameOr(nm.keys, r.APIKeyID, "(已删除)")
		}
		tp.Series[sk] += r.TotalTokens

		pname := r.Provider
		if p, ok := provider.Get(r.Provider); ok {
			pname = p.Name
		}
		add("provider", r.Provider, pname, r)
		add("model", r.RequestModel, r.RequestModel, r)
		mg := r.ModelGroup
		if mg == "" {
			mg = "-"
		}
		add("model_group", mg, mg, r)
		add("account", strconv.FormatUint(uint64(r.AccountID), 10), nameOr(nm.accounts, r.AccountID, "(已删除)"), r)
		add("group", strconv.FormatUint(uint64(r.GroupID), 10), nameOr(nm.groups, r.GroupID, "(已删除)"), r)
		add("user", strconv.FormatUint(uint64(r.UserID), 10), nameOr(nm.users, r.UserID, "(已删除)"), r)
		add("api_key", strconv.FormatUint(uint64(r.APIKeyID), 10), nameOr(nm.keys, r.APIKeyID, "(已删除)"), r)
	}
	// Fill empty buckets so charts are continuous.
	step := time.Hour
	start := from.Truncate(time.Hour)
	if byDay {
		step = 24 * time.Hour
		y, m, d := from.Local().Date()
		start = time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	}
	for t := start; !t.After(to); t = t.Add(step) {
		if _, ok := trend[t.Unix()]; !ok {
			trend[t.Unix()] = &trendPoint{Time: t, Series: map[string]int64{}}
		}
	}
	tl := make([]*trendPoint, 0, len(trend))
	for _, tp := range trend {
		tl = append(tl, tp)
	}
	sort.Slice(tl, func(i, j int) bool { return tl[i].Time.Before(tl[j].Time) })

	toList := func(kind string) []*dist {
		out := make([]*dist, 0, len(agg[kind]))
		for _, d := range agg[kind] {
			out = append(out, d)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].TotalTokens > out[j].TotalTokens })
		return out
	}
	var success, failed int64
	for _, r := range rows {
		success += r.Success
		failed += r.Failed
	}
	return gin.H{
		"summary": gin.H{"requests": summary.Requests, "success": success, "failed": failed, "prompt_tokens": summary.PromptTokens,
			"completion_tokens": summary.CompletionTokens, "total_tokens": summary.TotalTokens, "cached_tokens": summary.CachedTokens,
			"unknown_usage": summary.UnknownUsage},
		"trend":          tl,
		"by_provider":    toList("provider"),
		"by_model":       toList("model"),
		"by_model_group": toList("model_group"),
		"by_account":     toList("account"),
		"by_group":       toList("group"),
		"by_user":        toList("user"),
		"by_api_key":     toList("api_key"),
	}
}

func (s *Server) adminUsage(c *gin.Context) { c.JSON(200, s.usageReport(c, 0)) }

func (s *Server) userUsage(c *gin.Context) {
	rep := s.usageReport(c, cur(c).ID)
	delete(rep, "by_account")
	delete(rep, "by_group")
	delete(rep, "by_user")
	delete(rep, "by_model_group")
	c.JSON(200, rep)
}

// ---- overview ----

func (s *Server) overviewLive(c *gin.Context) { c.JSON(200, s.gw.Live()) }

func (s *Server) overviewUsage(c *gin.Context) {
	if c.Query("range") == "" {
		c.Request.URL.RawQuery += "&range=24h"
	}
	rep := s.usageReport(c, 0)
	sum := rep["summary"].(gin.H)
	from, _ := timeRange(c)
	var activeUsers, activeKeys int64
	s.db.Model(&model.UsageHourly{}).Where("hour >= ? AND user_id > 0", from.Truncate(time.Hour)).Distinct("user_id").Count(&activeUsers)
	s.db.Model(&model.UsageHourly{}).Where("hour >= ? AND api_key_id > 0", from.Truncate(time.Hour)).Distinct("api_key_id").Count(&activeKeys)
	total := sum["total_tokens"].(int64)
	cached := sum["cached_tokens"].(int64)
	reqs := sum["requests"].(int64)
	failed := sum["failed"].(int64)
	var cacheRate, failRate float64
	if total > 0 {
		cacheRate = float64(cached) / float64(total)
	}
	if reqs > 0 {
		failRate = float64(failed) / float64(reqs)
	}
	c.JSON(200, gin.H{
		"tokens":       gin.H{"total": total, "prompt": sum["prompt_tokens"], "completion": sum["completion_tokens"], "cached": cached, "cache_rate": cacheRate},
		"requests":     gin.H{"total": reqs, "success": sum["success"], "failed": failed, "fail_rate": failRate},
		"active_users": activeUsers,
		"active_keys":  activeKeys,
		"trend":        rep["trend"],
	})
}

// ---- metering maintenance ----

// rebuildUsage recomputes the hourly rollup from raw call logs for a time range.
func (s *Server) rebuildUsage(c *gin.Context) {
	var in struct {
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.From.IsZero() {
		badRequest(c, "from/to (RFC3339) are required")
		return
	}
	if in.To.IsZero() {
		in.To = time.Now()
	}
	if in.To.Sub(in.From) > 92*24*time.Hour {
		badRequest(c, "最多一次重建 92 天")
		return
	}
	retention := s.st.Get().Basic.LogRetentionDays
	if !logstore.RebuildAllowed(in.From, retention, time.Now()) {
		fail(c, 400, "outside_retention", fmt.Sprintf("起始时间早于调用日志保留期（%d 天），原始明细已不完整，拒绝重建以免抹掉历史聚合", retention))
		return
	}
	hours, rows, err := logstore.Rebuild(s.db, in.From, in.To, retention)
	if errors.Is(err, logstore.ErrRebuildOutsideRetention) {
		msg := "该区间的原始明细已清理，无法重建；调大保留期不会恢复已删除的明细"
		if pb, ok, perr := logstore.PurgedBefore(s.db); perr == nil && ok {
			msg = fmt.Sprintf("该区间的原始明细已于 %s 之前清理，无法重建；调大保留期不会恢复已删除的明细", pb.Local().Format("2006-01-02 15:04"))
		}
		fail(c, 400, "outside_retention", msg)
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"hours": hours, "rows": rows, "from": in.From, "to": in.To})
}

// reconcileUsage reports hours where the rollup and the raw logs disagree.
func (s *Server) reconcileUsage(c *gin.Context) {
	from, to := timeRange(c)
	mm, err := logstore.Reconcile(s.db, from, to)
	if err != nil {
		serverError(c, err)
		return
	}
	if mm == nil {
		mm = []logstore.Mismatch{}
	}
	c.JSON(200, gin.H{"from": from, "to": to, "mismatches": mm, "consistent": len(mm) == 0})
}

// meteringStatus exposes journal health.
func (s *Server) meteringStatus(c *gin.Context) {
	if s.eng.Logs == nil {
		c.JSON(200, logstore.Stats{})
		return
	}
	c.JSON(200, s.eng.Logs.Stats())
}
