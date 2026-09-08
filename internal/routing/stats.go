package routing

import (
	"strconv"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

// SourcePreview marks decisions produced by the admin preview endpoint; they
// are counted as decisions but not as requests.
const SourcePreview = "preview"

// Bucket is one row of a grouped distribution.
type Bucket struct {
	Key    string `json:"key"`
	Count  int64  `json:"count"`
	Tokens int64  `json:"tokens,omitempty"`
}

// StatsResult matches the docs/api.md "GET /stats" response.
type StatsResult struct {
	Decisions     int64    `json:"decisions"`
	Requests      int64    `json:"requests"`
	Failed        int64    `json:"failed"`
	TotalTokens   int64    `json:"total_tokens"`
	AvgTokens     int64    `json:"avg_tokens"`
	LatencyMs     int64    `json:"latency_ms"`
	ByLabel       []Bucket `json:"by_label"`
	BySource      []Bucket `json:"by_source"`
	ByModel       []Bucket `json:"by_model"`
	ByTokenBucket []Bucket `json:"by_token_bucket"`
}

// Token bucket boundaries, in display order.
var tokenBuckets = []struct {
	Key   string
	Upper int64 // exclusive; 0 = unbounded
}{
	{"0-500", 500},
	{"500-2k", 2000},
	{"2k-8k", 8000},
	{"8k-32k", 32000},
	{"32k+", 0},
}

// Stats aggregates RouteDecision rows created in [from, to).
func Stats(db *gorm.DB, from, to time.Time) (StatsResult, error) {
	var out StatsResult
	base := func() *gorm.DB {
		return db.Model(&model.RouteDecision{}).Where("created_at >= ? AND created_at < ?", from, to)
	}

	var tot struct {
		Decisions   int64
		Requests    int64
		Failed      int64
		TotalTokens int64
		LatencyMs   int64
	}
	err := base().Select(
		"COUNT(*) AS decisions, " +
			"COALESCE(SUM(CASE WHEN source <> '" + SourcePreview + "' THEN 1 ELSE 0 END), 0) AS requests, " +
			"COALESCE(SUM(CASE WHEN failed THEN 1 ELSE 0 END), 0) AS failed, " +
			"COALESCE(SUM(total_tokens), 0) AS total_tokens, " +
			"COALESCE(SUM(latency_ms), 0) AS latency_ms",
	).Scan(&tot).Error
	if err != nil {
		return out, err
	}
	out.Decisions, out.Requests, out.Failed = tot.Decisions, tot.Requests, tot.Failed
	out.TotalTokens, out.LatencyMs = tot.TotalTokens, tot.LatencyMs
	if out.Requests > 0 {
		out.AvgTokens = out.TotalTokens / out.Requests
	}

	group := func(col string) ([]Bucket, error) {
		var rows []Bucket
		err := base().Select(col + " AS key, COUNT(*) AS count, COALESCE(SUM(total_tokens), 0) AS tokens").
			Group(col).Order("count DESC, key ASC").Scan(&rows).Error
		if rows == nil {
			rows = []Bucket{}
		}
		return rows, err
	}
	if out.ByLabel, err = group("label"); err != nil {
		return out, err
	}
	if out.BySource, err = group("source"); err != nil {
		return out, err
	}
	if out.ByModel, err = group("selected_model"); err != nil {
		return out, err
	}

	// Token buckets: computed with a CASE expression so it works on sqlite and postgres.
	caseExpr := "CASE"
	for _, b := range tokenBuckets {
		if b.Upper == 0 {
			caseExpr += " ELSE '" + b.Key + "'"
		} else {
			caseExpr += " WHEN total_tokens < " + strconv.FormatInt(b.Upper, 10) + " THEN '" + b.Key + "'"
		}
	}
	caseExpr += " END"
	var raw []Bucket
	if err = base().Select(caseExpr + " AS key, COUNT(*) AS count").Group("key").Scan(&raw).Error; err != nil {
		return out, err
	}
	counts := map[string]int64{}
	for _, r := range raw {
		counts[r.Key] = r.Count
	}
	out.ByTokenBucket = make([]Bucket, 0, len(tokenBuckets))
	for _, b := range tokenBuckets {
		out.ByTokenBucket = append(out.ByTokenBucket, Bucket{Key: b.Key, Count: counts[b.Key]})
	}
	return out, nil
}
