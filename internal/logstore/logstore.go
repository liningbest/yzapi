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
	size     int64 // logical end of journal (bytes appended)
	dirty    bool  // bytes written since last fsync
	syncEach bool  // fsync on every Record (YZAPI_JOURNAL_FSYNC=always)
	overflow []*model.CallLog

	ckpt         atomic.Int64 // committed offset
	notify       chan struct{}
	stop         chan struct{}
	wg           sync.WaitGroup
	dropped      atomic.Int64
	replayed     atomic.Int64
	failures     atomic.Int64
	syncFailures atomic.Int64
	lastOK       atomic.Int64 // unix seconds of last successful commit
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
		ckptPath: filepath.Join(dir, checkpointFile), notify: make(chan struct{}, 1), stop: make(chan struct{}),
		syncEach: os.Getenv("YZAPI_JOURNAL_FSYNC") == "always"}
	if err := s.openJournal(); err != nil {
		return nil, err
	}
	if n, err := s.drain(); err != nil {
		slog.Warn("journal replay incomplete; will keep retrying in background", "err", err, "replayed", n)
	} else if n > 0 {
		slog.Info("replayed journal records into the database", "records", n)
	}
	// Databases from before the purge marker existed: assume everything older than the
	// current retention window may already be gone (conservative for rebuilds).
	if _, ok := PurgedBefore(db); !ok {
		_ = setPurgedBefore(db, s.cutoff())
	}
	s.wg.Add(3)
	go s.writer()
	go s.syncer()
	go s.janitor()
	return s, nil
}

// purgedKey is the settings row that records how far raw call logs have been purged.
// Rebuilding the rollup for hours before it would replace real history with zeros.
const purgedKey = "logs_purged_before"

// PurgedBefore returns the persisted purge boundary, if any.
func PurgedBefore(db *gorm.DB) (time.Time, bool) {
	var row model.Setting
	if err := db.Where("key = ?", purgedKey).First(&row).Error; err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, row.Value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// setPurgedBefore advances the persisted purge boundary (it never moves backwards).
func setPurgedBefore(db *gorm.DB, t time.Time) error {
	if cur, ok := PurgedBefore(db); ok && !t.After(cur) {
		return nil
	}
	return db.Save(&model.Setting{Key: purgedKey, Value: t.UTC().Format(time.RFC3339), UpdatedAt: time.Now()}).Error
}

func (s *Store) cutoff() time.Time {
	days := s.retDays()
	if days <= 0 {
		days = 30
	}
	return time.Now().AddDate(0, 0, -days)
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
	s.f, s.size = f, size
	s.ckpt.Store(ck)
	return nil
}

// Record appends a call log to the journal. It never blocks on the database.
//
// Durability boundary, stated precisely:
//   - When write(2) succeeds, the record is in the OS page cache before Record returns:
//     it survives a crash of this process, not a power loss.
//   - The syncer fsyncs dirty bytes about once per second; with YZAPI_JOURNAL_FSYNC=always
//     Record fsyncs before returning. A failed fsync leaves the bytes marked dirty (retried
//     on the next tick) and is counted in Stats.SyncFailures / yzapi_metering_sync_failures_total,
//     so "always" cannot promise stable storage when the device itself fails.
//   - If write(2) fails, the record is kept in memory only (Stats.OverflowRecords) and is
//     lost if the process dies before the journal becomes writable again; once the overflow
//     is full further records are dropped and counted.
//
// Callers needing a hard guarantee should alert on overflow_records, dirty and sync_failures.
func (s *Store) Record(l *model.CallLog) {
	b, err := json.Marshal(l)
	if err != nil {
		s.dropped.Add(1)
		return
	}
	b = append(b, '\n')
	s.mu.Lock()
	if s.f != nil {
		var n int
		if n, err = s.f.Write(b); err == nil {
			s.size += int64(len(b))
			s.dirty = true
			if s.syncEach {
				if serr := s.f.Sync(); serr != nil {
					// Keep dirty so the periodic syncer retries; count it for /metrics.
					s.syncFailures.Add(1)
				} else {
					s.dirty = false
				}
			}
		} else if n > 0 {
			// A short write left a torn line; drop it so the next record starts clean.
			_ = s.f.Truncate(s.size)
			_, _ = s.f.Seek(s.size, io.SeekStart)
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
	PendingBytes    int64      `json:"pending_bytes"`
	OverflowRecords int        `json:"overflow_records"` // held only in memory (journal unwritable)
	Dirty           bool       `json:"dirty"`            // bytes written but not yet fsynced
	Dropped         int64      `json:"dropped"`
	Replayed        int64      `json:"replayed"`
	Failures        int64      `json:"write_failures"`
	SyncFailures    int64      `json:"sync_failures"`
	LastCommit      *time.Time `json:"last_commit_at"`
	PurgedBefore    *time.Time `json:"purged_before"` // raw logs older than this are gone; rebuilds refuse earlier hours
}

func (s *Store) Stats() Stats {
	s.mu.Lock()
	size, overflow, dirty := s.size, len(s.overflow), s.dirty
	s.mu.Unlock()
	st := Stats{PendingBytes: size - s.ckpt.Load(), OverflowRecords: overflow, Dirty: dirty, Dropped: s.dropped.Load(),
		Replayed: s.replayed.Load(), Failures: s.failures.Load(), SyncFailures: s.syncFailures.Load()}
	if pb, ok := PurgedBefore(s.db); ok {
		st.PurgedBefore = &pb
	}
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
	defer poll.Stop()
	backoff := time.Duration(0)
	for {
		select {
		case <-s.notify:
		case <-poll.C:
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

// syncer flushes the journal to stable storage on its own schedule so a stalled database
// commit or retry backoff cannot delay it.
func (s *Store) syncer() {
	defer s.wg.Done()
	t := time.NewTicker(syncInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.fsync()
		case <-s.stop:
			return
		}
	}
}

// fsync flushes dirty journal bytes. On failure the data stays marked dirty so the next
// tick retries, and the failure is counted so operators can see it.
func (s *Store) fsync() {
	s.mu.Lock()
	if s.dirty && s.f != nil {
		if err := s.f.Sync(); err != nil {
			s.syncFailures.Add(1)
			if n := s.syncFailures.Load(); n == 1 || n%100 == 0 {
				slog.Warn("journal fsync failed; retrying", "err", err, "failures", n)
			}
		} else {
			s.dirty = false
		}
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
		// Overflow records are copied into the batch but only removed after the commit
		// succeeds, so a database failure keeps them for the next attempt.
		s.mu.Lock()
		took := 0
		if len(batch) < batchMax && len(s.overflow) > 0 {
			took = min(batchMax-len(batch), len(s.overflow))
			batch = append(batch, s.overflow[:took]...)
		}
		s.mu.Unlock()
		if len(batch) == 0 {
			s.maybeRotate()
			return total, nil
		}
		if err := s.commit(batch); err != nil {
			return total, err
		}
		if took > 0 {
			s.mu.Lock()
			s.overflow = append(s.overflow[:0], s.overflow[took:]...)
			s.mu.Unlock()
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

// ErrRebuildOutsideRetention is returned when a rebuild range reaches into hours whose
// raw logs may already have been purged: rebuilding there would erase real history.
var ErrRebuildOutsideRetention = errors.New("range starts before the log retention window; raw logs are no longer complete")

// RebuildAllowed reports whether [from, ...] lies entirely inside the retention window.
// A one-hour margin protects against the janitor running while the rebuild executes.
func RebuildAllowed(from time.Time, retentionDays int, now time.Time) bool {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	oldest := now.AddDate(0, 0, -retentionDays).Add(time.Hour)
	return !from.Truncate(time.Hour).Before(oldest.Truncate(time.Hour))
}

// Rebuild recomputes the hourly rollup for [from, to] from the raw call logs. Callers
// must check RebuildAllowed first (Rebuild enforces it too).
func Rebuild(db *gorm.DB, from, to time.Time, retentionDays int) (hours int, rows int, err error) {
	if !RebuildAllowed(from, retentionDays, time.Now()) {
		return 0, 0, ErrRebuildOutsideRetention
	}
	// The persisted purge boundary wins over the current retention setting: raising the
	// retention later does not bring purged raw logs back.
	if pb, ok := PurgedBefore(db); ok && from.Truncate(time.Hour).Before(pb.Add(time.Hour).Truncate(time.Hour)) {
		return 0, 0, ErrRebuildOutsideRetention
	}
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
// Both sides are evaluated on the same whole-hour window [from, to] so a range that
// starts or ends mid-hour cannot produce spurious mismatches.
func Reconcile(db *gorm.DB, from, to time.Time) ([]Mismatch, error) {
	from = from.Truncate(time.Hour)
	toExcl := to.Truncate(time.Hour).Add(time.Hour)
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
	if err := db.Model(&model.CallLog{}).Select("created_at, total_tokens").Where("created_at >= ? AND created_at < ?", from, toExcl).Scan(&raw).Error; err != nil {
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
		Where("hour >= ? AND hour < ?", from, toExcl).Group("hour").Scan(&roll).Error; err != nil {
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
	if s.f == nil || s.size < rotateAfter || s.ckpt.Load() != s.size {
		return
	}
	if err := s.f.Truncate(0); err != nil {
		return
	}
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return
	}
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
	cutoff := s.cutoff()
	// Persist the boundary first: if we crash mid-delete, rebuilds still refuse the range.
	if err := setPurgedBefore(s.db, cutoff); err != nil {
		slog.Warn("could not record purge boundary; skipping cleanup this round", "err", err)
		return
	}
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
	if s.f != nil {
		_ = s.f.Sync()
		_ = s.f.Close()
		s.f = nil
	}
	s.mu.Unlock()
}
