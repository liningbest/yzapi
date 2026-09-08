// Package logstore persists call logs asynchronously and maintains hourly rollups.
package logstore

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"yzapi/internal/model"
)

type Store struct {
	db      *gorm.DB
	ch      chan *model.CallLog
	wg      sync.WaitGroup
	stop    chan struct{}
	retDays func() int
}

func New(db *gorm.DB, retentionDays func() int) *Store {
	s := &Store{db: db, ch: make(chan *model.CallLog, 8192), stop: make(chan struct{}), retDays: retentionDays}
	s.wg.Add(2)
	go s.writer()
	go s.janitor()
	return s
}

// Record enqueues a call log without blocking the request path.
func (s *Store) Record(l *model.CallLog) {
	select {
	case s.ch <- l:
	default:
		slog.Warn("call log queue full, dropping record", "request_id", l.RequestID)
	}
}

func (s *Store) writer() {
	defer s.wg.Done()
	batch := make([]*model.CallLog, 0, 256)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.db.CreateInBatches(batch, 256).Error; err != nil {
			slog.Error("write call logs", "err", err, "n", len(batch))
		}
		s.rollup(batch)
		batch = batch[:0]
	}
	for {
		select {
		case l := <-s.ch:
			batch = append(batch, l)
			if len(batch) >= 256 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stop:
			for {
				select {
				case l := <-s.ch:
					batch = append(batch, l)
				default:
					flush()
					return
				}
			}
		}
	}
}

type dimKey struct {
	hour                            int64
	user, group, key, account       uint
	provider, reqModel, mg, apiType string
}

func (s *Store) rollup(batch []*model.CallLog) {
	agg := map[dimKey]*model.UsageHourly{}
	for _, l := range batch {
		h := l.CreatedAt.Truncate(time.Hour)
		k := dimKey{h.Unix(), l.UserID, l.GroupID, l.APIKeyID, l.AccountID, l.Provider, l.RequestModel, l.ModelGroup, l.APIType}
		u, ok := agg[k]
		if !ok {
			u = &model.UsageHourly{Hour: h, UserID: l.UserID, GroupID: l.GroupID, APIKeyID: l.APIKeyID, AccountID: l.AccountID,
				Provider: l.Provider, RequestModel: l.RequestModel, ModelGroup: l.ModelGroup, APIType: l.APIType}
			agg[k] = u
		}
		u.Requests++
		if l.Result == "success" {
			u.Success++
		} else {
			u.Failed++
		}
		u.PromptTokens += l.PromptTokens
		u.CompletionTokens += l.CompletionTokens
		u.TotalTokens += l.TotalTokens
		u.CachedTokens += l.CachedTokens
		u.LatencyMs += l.LatencyMs
	}
	for _, u := range agg {
		err := s.db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "hour"}, {Name: "user_id"}, {Name: "group_id"}, {Name: "api_key_id"}, {Name: "account_id"},
				{Name: "provider"}, {Name: "request_model"}, {Name: "model_group"}, {Name: "api_type"}},
			DoUpdates: clause.Assignments(map[string]any{
				"requests":          gorm.Expr("usage_hourlies.requests + ?", u.Requests),
				"success":           gorm.Expr("usage_hourlies.success + ?", u.Success),
				"failed":            gorm.Expr("usage_hourlies.failed + ?", u.Failed),
				"prompt_tokens":     gorm.Expr("usage_hourlies.prompt_tokens + ?", u.PromptTokens),
				"completion_tokens": gorm.Expr("usage_hourlies.completion_tokens + ?", u.CompletionTokens),
				"total_tokens":      gorm.Expr("usage_hourlies.total_tokens + ?", u.TotalTokens),
				"cached_tokens":     gorm.Expr("usage_hourlies.cached_tokens + ?", u.CachedTokens),
				"latency_ms":        gorm.Expr("usage_hourlies.latency_ms + ?", u.LatencyMs),
			}),
		}).Create(u).Error
		if err != nil {
			slog.Error("usage rollup", "err", err)
		}
	}
}

func (s *Store) janitor() {
	defer s.wg.Done()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	s.cleanup()
	for {
		select {
		case <-t.C:
			s.cleanup()
		case <-s.stop:
			return
		}
	}
}

func (s *Store) cleanup() {
	days := s.retDays()
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, tbl := range []any{&model.CallLog{}, &model.RouteDecision{}, &model.AuditLog{}} {
		// Delete in bounded batches so SQLite does not hold a long write lock.
		for i := 0; i < 100; i++ {
			res := s.db.Where("created_at < ?", cutoff).Limit(5000).Delete(tbl)
			if res.Error != nil || res.RowsAffected == 0 {
				break
			}
		}
	}
	s.db.Where("hour < ?", cutoff.AddDate(0, 0, -365)).Delete(&model.UsageHourly{})
}

func (s *Store) Close(ctx context.Context) {
	close(s.stop)
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
