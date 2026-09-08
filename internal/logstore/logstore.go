// Package logstore persists call logs durably and maintains hourly rollups.
//
// Metering must not be lost, so records are appended to an on-disk journal in the
// request path (cheap, sequential write) and a background writer moves them into the
// database in transactions that insert the raw logs and update the hourly rollup
// together. A checkpoint records how far the journal has been committed; on restart the
// uncommitted tail is replayed. call_logs.request_id is unique, so replaying a batch that
// was already committed is a no-op.
package logstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"yzapi/internal/model"
)

const (
	journalFile     = "calls.jsonl"
	checkpointFile  = "calls.ckpt"
	rotateAfter     = 32 << 20 // truncate the journal once fully committed and larger than this
	batchMax        = 500
	overflowMax     = 32768
	syncInterval    = time.Second
	pollInterval    = 500 * time.Millisecond
	maxRetryBackoff = 5 * time.Second
)

type Store struct {
	db      *gorm.DB
	retDays func() int

	mu       sync.Mutex // guards journal append state
	path     string
	ckptPath string
	f        *os.File
	w        *bufio.Writer
	size     int64 // logical end of journal (bytes appended)
	dirty    bool  // bytes written since last fsync
	overflow []*model.CallLog

	ckpt     atomic.Int64 // committed offset
	notify   chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup
	dropped  atomic.Int64
	replayed atomic.Int64
	failures atomic.Int64
	lastOK   atomic.Int64 // unix seconds of last successful commit
}

// New opens (or creates) the journal under dataDir/data/journal and starts the writer.
// The uncommitted journal tail is replayed before New returns so restarts never lose
// records that reached the journal.
func New(db *gorm.DB, dataDir string, retentionDays func() int) (*Store, error) {
	dir := filepath.Join(dataDir, "data", "journal")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &Store{db: db, retDays: retentionDays, path: filepath.Join(dir, journalFile),
		ckptPath: filepath.Join(dir, checkpointFile), notify: make(chan struct{}, 1), stop: make(chan struct{})}
	if err := s.openJournal(); err != nil {
		return nil, err
	}
	if n, err := s.drain(); err != nil {
		slog.Warn("journal replay incomplete; will keep retrying in background", "err", err, "replayed", n)
	} else if n > 0 {
		slog.Info("replayed journal records into the database", "records", n)
	}
	s.wg.Add(2)
	go s.writer()
	go s.janitor()
	return s, nil
}

func (s *Store) openJournal() error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	size := st.Size()
	// Drop a partially written last line (crash mid-append).
	if size > 0 {
		buf := make([]byte, min(size, 1<<20))
		if _, err := f.ReadAt(buf, size-int64(len(buf))); err == nil && buf[len(buf)-1] != '\n' {
			if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
				size = size - int64(len(buf)) + int64(i) + 1
			} else {
				size = 0
			}
			_ = f.Truncate(size)
		}
	}
	if _, err := f.Seek(size, io.SeekStart); err != nil {
		f.Close()
		return err
	}
	ck := int64(0)
	if b, err := os.ReadFile(s.ckptPath); err == nil {
		if v, err := strconv.ParseInt(string(bytes.TrimSpace(b)), 10, 64); err == nil && v >= 0 && v <= size {
			ck = v
		}
	}
	s.f, s.w, s.size = f, bufio.NewWriterSize(f, 256<<10), size
	s.ckpt.Store(ck)
	return nil
}

// Record appends a call log to the journal. It never blocks on the database.
func (s *Store) Record(l *model.CallLog) {
	b, err := json.Marshal(l)
	if err != nil {
		s.dropped.Add(1)
		return
	}
	b = append(b, '\n')
	s.mu.Lock()
	if s.w != nil {
		if _, err = s.w.Write(b); err == nil {
			s.size += int64(len(b))
			s.dirty = true
		}
	} else {
		err = errors.New("journal closed")
	}
	if err != nil {
		// Disk problem: keep the record in memory so a transient error still loses nothing.
		if len(s.overflow) < overflowMax {
			s.overflow = append(s.overflow, l)
		} else if n := s.dropped.Add(1); n == 1 || n%1000 == 0 {
			slog.Error("journal unavailable and overflow full, dropping records", "err", err, "dropped_total", n)
		}
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Stats for /metrics and the settings page.
type Stats struct {
	PendingBytes int64      `json:"pending_bytes"`
	Dropped      int64      `json:"dropped"`
	Replayed     int64      `json:"replayed"`
	Failures     int64      `json:"write_failures"`
	LastCommit   *time.Time `json:"last_commit_at"`
}

func (s *Store) Stats() Stats {
	s.mu.Lock()
	size := s.size
	s.mu.Unlock()
	st := Stats{PendingBytes: size - s.ckpt.Load(), Dropped: s.dropped.Load(), Replayed: s.replayed.Load(), Failures: s.failures.Load()}
	if t := s.lastOK.Load(); t > 0 {
		tt := time.Unix(t, 0)
		st.LastCommit = &tt
	}
	return st
}

// Dropped reports records lost for good (journal unwritable and overflow full).
func (s *Store) Dropped() int64 { return s.dropped.Load() }

func (s *Store) writer() {
	defer s.wg.Done()
	poll := time.NewTicker(pollInterval)
	syncT := time.NewTicker(syncInterval)
	defer poll.Stop()
	defer syncT.Stop()
	backoff := time.Duration(0)
	for {
		select {
		case <-s.notify:
		case <-poll.C:
		case <-syncT.C:
			s.fsync()
			continue
		case <-s.stop:
			for i := 0; i < 3; i++ {
				if _, err := s.drain(); err == nil {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			s.fsync()
			return
		}
		if backoff > 0 {
			time.Sleep(backoff)
		}
		if _, err := s.drain(); err != nil {
			if n := s.failures.Add(1); n == 1 || n%50 == 0 {
				slog.Error("committing journal to database failed; will retry", "err", err, "failures", n)
			}
			backoff = min(max(backoff*2, 200*time.Millisecond), maxRetryBackoff)
		} else {
			backoff = 0
		}
	}
}

func (s *Store) fsync() {
	s.mu.Lock()
	if s.dirty && s.w != nil {
		_ = s.w.Flush()
		_ = s.f.Sync()
		s.dirty = false
	}
	s.mu.Unlock()
}

// drain commits every uncommitted journal record (and overflow records) to the database.
func (s *Store) drain() (int, error) {
	total := 0
	for {
		batch, next, err := s.readBatch()
		if err != nil {
			return total, err
		}
		s.mu.Lock()
		if len(batch) < batchMax && len(s.overflow) > 0 {
			take := min(batchMax-len(batch), len(s.overflow))
			batch = append(batch, s.overflow[:take]...)
			s.overflow = append(s.overflow[:0], s.overflow[take:]...)
		}
		s.mu.Unlock()
		if len(batch) == 0 {
			s.maybeRotate()
			return total, nil
		}
		if err := s.commit(batch); err != nil {
			return total, err
		}
		s.ckpt.Store(next)
		_ = os.WriteFile(s.ckptPath, []byte(strconv.FormatInt(next, 10)), 0o640)
		s.lastOK.Store(time.Now().Unix())
		total += len(batch)
	}
}

// readBatch parses up to batchMax journal lines starting at the checkpoint.
func (s *Store) readBatch() ([]*model.CallLog, int64, error) {
	s.mu.Lock()
	if s.w != nil {
		_ = s.w.Flush()
	}
	end := s.size
	s.mu.Unlock()
	start := s.ckpt.Load()
	if start >= end {
		return nil, start, nil
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, start, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(io.NewSectionReader(f, start, end-start), 256<<10)
	var batch []*model.CallLog
	off := start
	for len(batch) < batchMax {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF or a partial line: stop before it
		}
		off += int64(len(line))
		var l model.CallLog
		if json.Unmarshal(line, &l) != nil || l.RequestID == "" {
			continue // corrupt line: skip it rather than block the journal forever
		}
		l.ID = 0
		batch = append(batch, &l)
	}
	return batch, off, nil
}

// commit inserts the batch and updates the hourly rollup in one transaction, skipping
// request ids that already exist so replays are idempotent.
func (s *Store) commit(batch []*model.CallLog) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		ids := make([]string, 0, len(batch))
		for _, l := range batch {
			ids = append(ids, l.RequestID)
		}
		var existing []string
		if err := tx.Model(&model.CallLog{}).Where("request_id IN ?", ids).Pluck("request_id", &existing).Error; err != nil {
			return err
		}
		skip := make(map[string]bool, len(existing))
		for _, id := range existing {
			skip[id] = true
		}
		fresh := make([]*model.CallLog, 0, len(batch))
		seen := map[string]bool{}
		for _, l := range batch {
			if skip[l.RequestID] || seen[l.RequestID] {
				s.replayed.Add(1)
				continue
			}
			seen[l.RequestID] = true
			fresh = append(fresh, l)
		}
		if len(fresh) == 0 {
			return nil
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).
			CreateInBatches(fresh, 256).Error; err != nil {
			return err
		}
		return applyRollup(tx, fresh)
	})
}

type dimKey struct {
	hour                            int64
	user, group, key, account       uint
	provider, reqModel, mg, apiType string
}

// Aggregate folds call logs into hourly rows (exported for the rebuild tool).
func Aggregate(batch []*model.CallLog) []*model.UsageHourly {
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
		if l.UsageStatus == model.UsagePartial || l.UsageStatus == model.UsageUnknown {
			u.UnknownUsage++
		}
	}
	out := make([]*model.UsageHourly, 0, len(agg))
	for _, u := range agg {
		out = append(out, u)
	}
	return out
}

func applyRollup(tx *gorm.DB, batch []*model.CallLog) error {
	for _, u := range Aggregate(batch) {
		err := tx.Clauses(clause.OnConflict{
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
				"unknown_usage":     gorm.Expr("usage_hourlies.unknown_usage + ?", u.UnknownUsage),
				"latency_ms":        gorm.Expr("usage_hourlies.latency_ms + ?", u.LatencyMs),
			}),
		}).Create(u).Error
		if err != nil {
			return err
		}
	}
	return nil
}

// Rebuild recomputes the hourly rollup for [from, to] from the raw call logs.
func Rebuild(db *gorm.DB, from, to time.Time) (hours int, rows int, err error) {
	from = from.Truncate(time.Hour)
	to = to.Truncate(time.Hour).Add(time.Hour)
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("hour >= ? AND hour < ?", from, to).Delete(&model.UsageHourly{}).Error; err != nil {
			return err
		}
		for h := from; h.Before(to); h = h.Add(time.Hour) {
			var logs []*model.CallLog
			if err := tx.Where("created_at >= ? AND created_at < ?", h, h.Add(time.Hour)).Find(&logs).Error; err != nil {
				return err
			}
			if len(logs) == 0 {
				continue
			}
			hours++
			agg := Aggregate(logs)
			rows += len(agg)
			if err := tx.CreateInBatches(agg, 256).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return hours, rows, err
}

// Mismatch is one hour whose rollup differs from the raw logs.
type Mismatch struct {
	Hour           time.Time `json:"hour"`
	LogRequests    int64     `json:"log_requests"`
	RollupRequests int64     `json:"rollup_requests"`
	LogTokens      int64     `json:"log_tokens"`
	RollupTokens   int64     `json:"rollup_tokens"`
}

// Reconcile compares raw logs against the rollup per hour and returns the differences.
func Reconcile(db *gorm.DB, from, to time.Time) ([]Mismatch, error) {
	type hourSum struct {
		Hour     time.Time
		Requests int64
		Tokens   int64
	}
	// Bucket raw logs in Go so the query stays portable across SQLite and PostgreSQL.
	var raw []struct {
		CreatedAt   time.Time
		TotalTokens int64
	}
	if err := db.Model(&model.CallLog{}).Select("created_at, total_tokens").Where("created_at >= ? AND created_at <= ?", from, to).Scan(&raw).Error; err != nil {
		return nil, err
	}
	byHour := map[int64]*hourSum{}
	for _, r := range raw {
		h := r.CreatedAt.Truncate(time.Hour)
		x, ok := byHour[h.Unix()]
		if !ok {
			x = &hourSum{Hour: h}
			byHour[h.Unix()] = x
		}
		x.Requests++
		x.Tokens += r.TotalTokens
	}
	var roll []struct {
		Hour     time.Time
		Requests int64
		Tokens   int64
	}
	if err := db.Model(&model.UsageHourly{}).Select("hour, SUM(requests) AS requests, SUM(total_tokens) AS tokens").
		Where("hour >= ? AND hour <= ?", from.Truncate(time.Hour), to).Group("hour").Scan(&roll).Error; err != nil {
		return nil, err
	}
	rollMap := map[int64]struct{ r, t int64 }{}
	for _, r := range roll {
		rollMap[r.Hour.Unix()] = struct{ r, t int64 }{r.Requests, r.Tokens}
	}
	var out []Mismatch
	for k, l := range byHour {
		r := rollMap[k]
		if r.r != l.Requests || r.t != l.Tokens {
			out = append(out, Mismatch{Hour: l.Hour, LogRequests: l.Requests, RollupRequests: r.r, LogTokens: l.Tokens, RollupTokens: r.t})
		}
	}
	for k, r := range rollMap {
		if _, ok := byHour[k]; !ok && (r.r != 0 || r.t != 0) {
			out = append(out, Mismatch{Hour: time.Unix(k, 0), RollupRequests: r.r, RollupTokens: r.t})
		}
	}
	return out, nil
}

// maybeRotate truncates a fully committed, large journal so it does not grow forever.
func (s *Store) maybeRotate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil || s.size < rotateAfter || s.ckpt.Load() != s.size {
		return
	}
	_ = s.w.Flush()
	if err := s.f.Truncate(0); err != nil {
		return
	}
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return
	}
	s.w.Reset(s.f)
	s.size = 0
	s.ckpt.Store(0)
	_ = os.WriteFile(s.ckptPath, []byte("0"), 0o640)
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

// Close flushes the journal and commits what it can within ctx.
func (s *Store) Close(ctx context.Context) {
	close(s.stop)
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	s.mu.Lock()
	if s.w != nil {
		_ = s.w.Flush()
		_ = s.f.Sync()
		_ = s.f.Close()
		s.w, s.f = nil, nil
	}
	s.mu.Unlock()
}
