package gateway

import (
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

// quotaTracker tracks per-group token usage for the current calendar month.
type quotaTracker struct {
	db    *gorm.DB
	mu    sync.RWMutex
	used  map[uint]int64
	month time.Time
}

func newQuotaTracker(db *gorm.DB) *quotaTracker {
	q := &quotaTracker{db: db, used: map[uint]int64{}}
	q.refresh()
	return q
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func (q *quotaTracker) refresh() {
	start := monthStart(time.Now())
	type row struct {
		GroupID uint
		Total   int64
	}
	var rows []row
	if err := q.db.Model(&model.UsageHourly{}).Select("group_id, SUM(total_tokens) AS total").
		Where("hour >= ?", start).Group("group_id").Scan(&rows).Error; err != nil {
		slog.Warn("quota refresh query failed, keeping in-memory counters", "err", err)
		return
	}
	m := make(map[uint]int64, len(rows))
	for _, r := range rows {
		m[r.GroupID] = r.Total
	}
	q.mu.Lock()
	if q.month == start {
		// Logs are persisted asynchronously, so the DB may lag the in-memory counter by a
		// few seconds. Never let a refresh lower a counter below what we already observed.
		for gid, v := range q.used {
			if v > m[gid] {
				m[gid] = v
			}
		}
	}
	q.used = m
	q.month = start
	q.mu.Unlock()
}

func (q *quotaTracker) add(groupID uint, tokens int64) {
	if tokens <= 0 {
		return
	}
	q.mu.Lock()
	if monthStart(time.Now()) != q.month {
		q.used = map[uint]int64{}
		q.month = monthStart(time.Now())
	}
	q.used[groupID] += tokens
	q.mu.Unlock()
}

func (q *quotaTracker) usage(groupID uint) int64 {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.used[groupID]
}

func (q *quotaTracker) exceeded(groupID uint, quota int64) bool {
	if quota <= 0 {
		return false
	}
	return q.usage(groupID) >= quota
}
