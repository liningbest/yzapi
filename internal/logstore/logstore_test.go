package logstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	return db
}

func sample(id string, tokens int64) *model.CallLog {
	return &model.CallLog{RequestID: id, UserID: 1, GroupID: 1, Provider: "p", RequestModel: "m", APIType: "text",
		Result: "success", StatusCode: 200, PromptTokens: tokens, TotalTokens: tokens, UsageStatus: model.UsageConfirmed,
		CreatedAt: time.Date(2026, 9, 8, 10, 30, 0, 0, time.UTC)}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// Records reach the database with a consistent rollup, and a restart replays the
// uncommitted journal tail without duplicating anything.
func TestJournalCommitReplayAndIdempotency(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()

	s, err := New(db, dir, func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.Record(sample("r"+string(rune('a'+i)), 10))
	}
	waitFor(t, func() bool {
		var n int64
		db.Model(&model.CallLog{}).Count(&n)
		return n == 3
	})
	var roll model.UsageHourly
	if err := db.First(&roll).Error; err != nil || roll.Requests != 3 || roll.TotalTokens != 30 {
		t.Fatalf("rollup = %+v err=%v", roll, err)
	}
	s.Close(context.Background())

	// Simulate a crash after journaling but before commit: append lines and rewind the checkpoint.
	path := filepath.Join(dir, "data", "journal", journalFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ra", "rd"} { // "ra" already exists -> must not be double counted
		b, _ := json.Marshal(sample(id, 10))
		f.Write(append(b, '\n'))
	}
	f.Write([]byte(`{"request_id":"partial`)) // torn write at the end
	f.Close()

	s2, err := New(db, dir, func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close(context.Background())
	var n int64
	db.Model(&model.CallLog{}).Count(&n)
	if n != 4 {
		t.Fatalf("expected 4 logs after replay, got %d", n)
	}
	if err := db.First(&roll).Error; err != nil || roll.Requests != 4 || roll.TotalTokens != 40 {
		t.Fatalf("rollup after replay = %+v", roll)
	}
	if st := s2.Stats(); st.Replayed != 1 || st.Dropped != 0 {
		t.Fatalf("stats = %+v", st)
	}
	mm, err := Reconcile(db, time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC))
	if err != nil || len(mm) != 0 {
		t.Fatalf("reconcile: %v %+v", err, mm)
	}
}

// A rollup that drifted can be rebuilt from the raw logs.
func TestRebuild(t *testing.T) {
	db := testDB(t)
	logs := []*model.CallLog{sample("x1", 5), sample("x2", 7)}
	logs[1].UsageStatus = model.UsageUnknown
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&model.UsageHourly{Hour: logs[0].CreatedAt.Truncate(time.Hour), UserID: 1, GroupID: 1, Provider: "p", RequestModel: "m", APIType: "text", Requests: 99, TotalTokens: 999})
	from := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	mm, _ := Reconcile(db, from, from.Add(24*time.Hour))
	if len(mm) != 1 {
		t.Fatalf("expected drift to be detected, got %+v", mm)
	}
	hours, rows, err := Rebuild(db, from, from.Add(24*time.Hour), 3650)
	if err != nil || hours != 1 || rows != 1 {
		t.Fatalf("rebuild hours=%d rows=%d err=%v", hours, rows, err)
	}
	var roll model.UsageHourly
	db.First(&roll)
	if roll.Requests != 2 || roll.TotalTokens != 12 || roll.UnknownUsage != 1 {
		t.Fatalf("rebuilt rollup = %+v", roll)
	}
	mm, _ = Reconcile(db, from, from.Add(24*time.Hour))
	if len(mm) != 0 {
		t.Fatalf("expected consistent after rebuild, got %+v", mm)
	}
}

// Record must hand the bytes to the OS before returning (no user-space buffering).
func TestRecordIsVisibleOnDiskImmediately(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	s, err := New(db, dir, func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	s.Record(sample("d1", 1))
	st, err := os.Stat(filepath.Join(dir, "data", "journal", journalFile))
	if err != nil || st.Size() == 0 {
		t.Fatalf("journal must contain the record right after Record returns (size=%d err=%v)", st.Size(), err)
	}
}

// Overflow records survive a failed database commit and are stored once it recovers.
func TestOverflowSurvivesCommitFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately no tables yet: every commit fails.
	dir := t.TempDir()
	s := &Store{db: db, retDays: func() int { return 30 }, path: filepath.Join(dir, journalFile),
		ckptPath: filepath.Join(dir, checkpointFile), notify: make(chan struct{}, 1), stop: make(chan struct{})}
	// No journal file (s.f == nil) forces the overflow path.
	s.Record(sample("o1", 1))
	s.Record(sample("o2", 1))
	if _, err := s.drain(); err == nil {
		t.Fatal("expected commit to fail without tables")
	}
	s.mu.Lock()
	n, d := len(s.overflow), s.dropped.Load()
	s.mu.Unlock()
	if n != 2 || d != 0 {
		t.Fatalf("overflow must be kept after a failed commit: overflow=%d dropped=%d", n, d)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	if _, err := s.drain(); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&model.CallLog{}).Count(&cnt)
	s.mu.Lock()
	n = len(s.overflow)
	s.mu.Unlock()
	if cnt != 2 || n != 0 {
		t.Fatalf("after recovery: rows=%d overflow=%d", cnt, n)
	}
}

// A rebuild may not reach into hours whose raw logs may already be purged.
func TestRebuildAllowed(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if RebuildAllowed(now.AddDate(0, 0, -60), 30, now) {
		t.Fatal("60 days back must be refused with 30-day retention")
	}
	if !RebuildAllowed(now.AddDate(0, 0, -7), 30, now) {
		t.Fatal("7 days back must be allowed")
	}
	db := testDB(t)
	db.Create(&model.UsageHourly{Hour: now.AddDate(0, 0, -60), Requests: 1, TotalTokens: 5, Provider: "p", RequestModel: "m", APIType: "text"})
	if _, _, err := Rebuild(db, now.AddDate(0, 0, -60), now, 30); err == nil {
		t.Fatal("Rebuild must refuse ranges outside retention")
	}
	var cnt int64
	db.Model(&model.UsageHourly{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("historical rollup must be untouched, got %d rows", cnt)
	}
}

// A range that starts and ends mid-hour must not report spurious mismatches.
func TestReconcileMidHourRange(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	s, err := New(db, dir, func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	s.Record(sample("m1", 4)) // created 10:30
	waitFor(t, func() bool { var n int64; db.Model(&model.CallLog{}).Count(&n); return n == 1 })
	s.Close(context.Background())
	from := time.Date(2026, 9, 8, 10, 15, 0, 0, time.UTC)
	to := time.Date(2026, 9, 8, 10, 45, 0, 0, time.UTC)
	mm, err := Reconcile(db, from, to)
	if err != nil || len(mm) != 0 {
		t.Fatalf("expected consistent, got %v %+v", err, mm)
	}
}

// A4: raising the retention window later must not allow a rebuild over hours whose raw
// logs were already purged under the old window (that would zero real history).
func TestExtendedRetentionMustNotEraseHistory(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -60).Truncate(time.Hour)
	l := sample("old1", 5)
	l.CreatedAt = old.Add(10 * time.Minute)
	db.Create(l)
	db.Create(&model.UsageHourly{Hour: old, UserID: 1, GroupID: 1, Provider: "p", RequestModel: "m", APIType: "text", Requests: 1, TotalTokens: 5})

	s, err := New(db, dir, func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	s.cleanup() // the janitor does this at startup too; run it synchronously here
	s.Close(context.Background())
	var n int64
	db.Model(&model.CallLog{}).Count(&n)
	if n != 0 {
		t.Fatalf("raw log should be purged, %d left", n)
	}
	pb, ok, _ := PurgedBefore(db)
	if !ok || pb.Before(now.AddDate(0, 0, -31)) {
		t.Fatalf("purge boundary not persisted: %v %v", pb, ok)
	}

	// Operator raises retention to 90 days: the purged hour must still be refused.
	if _, _, err := Rebuild(db, old, now, 90); err != ErrRebuildOutsideRetention {
		t.Fatalf("rebuild over purged hours must be refused, got %v", err)
	}
	var roll model.UsageHourly
	if err := db.First(&roll).Error; err != nil || roll.Requests != 1 || roll.TotalTokens != 5 {
		t.Fatalf("history rollup must be untouched: %+v %v", roll, err)
	}
	// Hours inside the complete window can still be corrected.
	recent := now.AddDate(0, 0, -7).Truncate(time.Hour)
	r := sample("new1", 7)
	r.CreatedAt = recent.Add(5 * time.Minute)
	db.Create(r)
	db.Create(&model.UsageHourly{Hour: recent, UserID: 1, GroupID: 1, Provider: "p", RequestModel: "m", APIType: "text", Requests: 9, TotalTokens: 99})
	if _, _, err := Rebuild(db, recent, now, 90); err != nil {
		t.Fatalf("rebuild inside the complete window: %v", err)
	}
	var fixed model.UsageHourly
	db.Where("hour = ?", recent).First(&fixed)
	if fixed.Requests != 1 || fixed.TotalTokens != 7 {
		t.Fatalf("recent rollup not corrected: %+v", fixed)
	}
}

// Databases created before the purge marker existed get a conservative boundary at the
// current retention cutoff, so a later retention increase cannot rebuild over it.
func TestLegacyDBGetsConservativePurgeBoundary(t *testing.T) {
	db := testDB(t)
	if _, ok, _ := PurgedBefore(db); ok {
		t.Fatal("fresh db must not have a boundary yet")
	}
	s, err := New(db, t.TempDir(), func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	s.Close(context.Background())
	pb, ok, _ := PurgedBefore(db)
	if !ok || time.Since(pb) > 31*24*time.Hour || time.Since(pb) < 29*24*time.Hour {
		t.Fatalf("boundary = %v ok=%v", pb, ok)
	}
	// The boundary never moves backwards.
	if err := setPurgedBefore(db, pb.AddDate(0, 0, -10)); err != nil {
		t.Fatal(err)
	}
	if pb2, _, _ := PurgedBefore(db); !pb2.Equal(pb) {
		t.Fatalf("boundary moved backwards: %v -> %v", pb, pb2)
	}
}

// Stats expose what is not yet durable: overflow-only records and a failed fsync keep
// the store visibly dirty instead of pretending the data is on disk.
func TestStatsExposeNonDurableState(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	s := &Store{db: db, retDays: func() int { return 30 }, path: filepath.Join(dir, journalFile),
		ckptPath: filepath.Join(dir, checkpointFile), notify: make(chan struct{}, 1), stop: make(chan struct{})}
	s.Record(sample("nd1", 1)) // no journal file: overflow only
	if st := s.Stats(); st.OverflowRecords != 1 {
		t.Fatalf("overflow must be visible: %+v", st)
	}
	// A closed file descriptor makes fsync fail: the failure is counted and dirty stays set.
	f, err := os.Create(filepath.Join(dir, journalFile))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	s.f, s.dirty, s.syncEach = f, true, true
	s.fsync()
	if st := s.Stats(); st.SyncFailures != 1 || !st.Dirty {
		t.Fatalf("failed fsync must stay dirty and be counted: %+v", st)
	}
}

// B3: when the purge boundary cannot be read or is malformed, a rebuild must refuse and
// leave the existing rollup untouched; a Store must not start without a boundary.
func TestPurgeBoundaryFailuresRejectRebuild(t *testing.T) {
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -60).Truncate(time.Hour)
	setup := func(t *testing.T) *gorm.DB {
		db := testDB(t)
		if err := setPurgedBefore(db, now.AddDate(0, 0, -30)); err != nil {
			t.Fatal(err)
		}
		db.Create(&model.UsageHourly{Hour: old, Requests: 1, TotalTokens: 42, Provider: "p", RequestModel: "m", APIType: "text"})
		return db
	}
	rollupIntact := func(t *testing.T, db *gorm.DB) {
		t.Helper()
		var n int64
		db.Model(&model.UsageHourly{}).Count(&n)
		if n != 1 {
			t.Fatalf("historical rollup must be untouched, rows=%d", n)
		}
	}
	t.Run("read error", func(t *testing.T) {
		db := setup(t)
		db.Callback().Query().Before("gorm:query").Register("test_marker_fail", func(tx *gorm.DB) {
			if tx.Statement.Table == "settings" {
				tx.AddError(errors.New("injected"))
			}
		})
		if _, _, err := Rebuild(db, old, old, 90); err == nil {
			t.Fatal("rebuild must fail when the boundary cannot be read")
		}
		rollupIntact(t, db)
		if _, err := New(db, t.TempDir(), func() int { return 30 }); err == nil {
			t.Fatal("store must not start when the boundary cannot be read")
		}
	})
	t.Run("malformed", func(t *testing.T) {
		db := setup(t)
		db.Model(&model.Setting{}).Where("key = ?", purgedKey).Update("value", "not-a-time")
		if _, _, err := Rebuild(db, old, old, 90); err == nil {
			t.Fatal("rebuild must fail on a malformed boundary")
		}
		rollupIntact(t, db)
	})
	t.Run("boundary ok", func(t *testing.T) {
		db := setup(t)
		if _, _, err := Rebuild(db, old, old, 90); err != ErrRebuildOutsideRetention {
			t.Fatalf("purged hour must be refused: %v", err)
		}
		rollupIntact(t, db)
		recent := now.AddDate(0, 0, -7).Truncate(time.Hour)
		if _, _, err := Rebuild(db, recent, now, 90); err != nil {
			t.Fatalf("complete window must rebuild: %v", err)
		}
	})
}

// Rollups split tokens by attempt account while counting the request once; logs from
// before attempts carried usage keep their totals on the answering account.
func TestAggregateAttemptAttribution(t *testing.T) {
	l := sample("att1", 0)
	l.AccountID, l.Provider = 2, "openai"
	l.PromptTokens, l.CompletionTokens, l.TotalTokens = 30, 8, 38
	l.Attempts = model.JSON(`[{"account_id":1,"provider":"deepseek","status_code":500,"usage_status":"confirmed","prompt_tokens":10,"completion_tokens":3},` +
		`{"account_id":2,"provider":"openai","status_code":200,"usage_status":"confirmed","prompt_tokens":20,"completion_tokens":5}]`)
	legacy := sample("legacy", 9)
	legacy.AccountID, legacy.Provider = 2, "openai"
	legacy.Attempts = model.JSON(`[{"account_id":2,"provider":"openai","status_code":200,"usage_status":""}]`)
	rows := Aggregate([]*model.CallLog{l, legacy})
	byAcc := map[uint]*model.UsageHourly{}
	for _, r := range rows {
		byAcc[r.AccountID] = r
	}
	if a := byAcc[1]; a == nil || a.TotalTokens != 13 || a.Requests != 0 || a.Attempts != 1 || a.Provider != "deepseek" {
		t.Fatalf("account 1 = %+v", a)
	}
	if b := byAcc[2]; b == nil || b.TotalTokens != 25+9 || b.Requests != 2 || b.Attempts != 2 {
		t.Fatalf("account 2 = %+v", b)
	}
}

func legacyLog(id string, at time.Time) *model.CallLog {
	l := sample(id, 0)
	l.CreatedAt = at
	l.AccountID, l.Provider = 2, "openai"
	return l
}

// C1: a request written by a pre-round-8 gateway (only the final attempt's usage on the
// request, both attempts in the record) is corrected from its attempt evidence during
// Rebuild, so logs, rollup and reconcile agree; rows without evidence stay untouched.
func TestRebuildCorrectsLegacyUnderreportedLogs(t *testing.T) {
	db := testDB(t)
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour).Add(time.Minute)
	retry := legacyLog("legacy-retry", at)
	retry.PromptTokens, retry.CompletionTokens, retry.TotalTokens, retry.UsageStatus, retry.TokensKnown = 20, 5, 25, model.UsageConfirmed, true
	retry.Attempts = model.JSON(`[{"account_id":1,"provider":"openai","status_code":500,"usage_status":"confirmed","prompt_tokens":10,"completion_tokens":3},` +
		`{"account_id":2,"provider":"openai","status_code":200,"usage_status":"confirmed","prompt_tokens":20,"completion_tokens":5}]`)
	rejected := legacyLog("legacy-400", at)
	rejected.Result, rejected.StatusCode, rejected.UsageStatus = "client_error", 400, model.UsageNone
	rejected.Attempts = model.JSON(`[{"account_id":2,"provider":"openai","status_code":400,"usage_status":"confirmed","prompt_tokens":10,"completion_tokens":3}]`)
	unknownFirst := legacyLog("legacy-unknown", at)
	unknownFirst.PromptTokens, unknownFirst.CompletionTokens, unknownFirst.TotalTokens, unknownFirst.UsageStatus, unknownFirst.TokensKnown = 20, 5, 25, model.UsageConfirmed, true
	unknownFirst.Attempts = model.JSON(`[{"account_id":1,"provider":"openai","status_code":500,"usage_status":"unknown"},` +
		`{"account_id":2,"provider":"openai","status_code":200,"usage_status":"confirmed","prompt_tokens":20,"completion_tokens":5}]`)
	ancient := legacyLog("legacy-noevidence", at) // attempts without usage fields at all
	ancient.PromptTokens, ancient.TotalTokens, ancient.UsageStatus, ancient.TokensKnown = 7, 7, model.UsageConfirmed, true
	ancient.Attempts = model.JSON(`[{"account_id":2,"provider":"openai","status_code":200}]`)
	for _, l := range []*model.CallLog{retry, rejected, unknownFirst, ancient} {
		if err := db.Create(l).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Rebuild(db, at, at, 30); err != nil {
		t.Fatal(err)
	}
	var got model.CallLog
	db.Where("request_id = ?", "legacy-retry").First(&got)
	if got.TotalTokens != 38 || got.PromptTokens != 30 || got.UsageStatus != model.UsageConfirmed || !got.TokensKnown || !got.UsageCorrected {
		t.Fatalf("retry log not corrected: %+v", got)
	}
	got = model.CallLog{}
	db.Where("request_id = ?", "legacy-400").First(&got)
	if got.TotalTokens != 13 || got.UsageStatus != model.UsageConfirmed || got.StatusCode != 400 || !got.UsageCorrected {
		t.Fatalf("400 log not corrected: %+v", got)
	}
	got = model.CallLog{}
	db.Where("request_id = ?", "legacy-unknown").First(&got)
	if got.TotalTokens != 25 || got.UsageStatus != model.UsageUnknown || got.TokensKnown || !got.UsageCorrected {
		t.Fatalf("unknown-first log not corrected: %+v", got)
	}
	got = model.CallLog{}
	db.Where("request_id = ?", "legacy-noevidence").First(&got)
	if got.TotalTokens != 7 || got.UsageStatus != model.UsageConfirmed || got.UsageCorrected {
		t.Fatalf("log without evidence must stay untouched: %+v", got)
	}
	var rows []model.UsageHourly
	db.Find(&rows)
	byAcc := map[uint]int64{}
	var reqs, attempts, tokens int64
	for _, r := range rows {
		byAcc[r.AccountID] += r.TotalTokens
		reqs += r.Requests
		attempts += r.Attempts
		tokens += r.TotalTokens
	}
	if byAcc[1] != 13 || byAcc[2] != 25+13+25+7 || reqs != 4 || attempts != 6 || tokens != 38+13+25+7 {
		t.Fatalf("rollup after correction: byAcc=%v reqs=%d attempts=%d tokens=%d", byAcc, reqs, attempts, tokens)
	}
	mm, err := Reconcile(db, at, at)
	if err != nil || len(mm) != 0 {
		t.Fatalf("reconcile after correction: %v %+v", err, mm)
	}
	// A second rebuild is a no-op for the corrected rows.
	if _, _, err := Rebuild(db, at, at, 30); err != nil {
		t.Fatal(err)
	}
	got = model.CallLog{}
	db.Where("request_id = ?", "legacy-retry").First(&got)
	if got.TotalTokens != 38 {
		t.Fatalf("repeat rebuild changed the corrected row: %+v", got)
	}
	if mm, _ := Reconcile(db, at, at); len(mm) != 0 {
		t.Fatalf("reconcile after second rebuild: %+v", mm)
	}
}

// A journal record left behind by an older binary is normalised when it is committed.
func TestCommitNormalizesLegacyJournalRecord(t *testing.T) {
	db := testDB(t)
	l := legacyLog("journal-legacy", time.Now().UTC().Truncate(time.Hour).Add(time.Minute))
	l.PromptTokens, l.CompletionTokens, l.TotalTokens, l.UsageStatus, l.TokensKnown = 20, 5, 25, model.UsageConfirmed, true
	l.Attempts = model.JSON(`[{"account_id":1,"provider":"openai","status_code":500,"usage_status":"confirmed","prompt_tokens":10,"completion_tokens":3},` +
		`{"account_id":2,"provider":"openai","status_code":200,"usage_status":"confirmed","prompt_tokens":20,"completion_tokens":5}]`)
	s := &Store{db: db}
	if err := s.commit([]*model.CallLog{l}); err != nil {
		t.Fatal(err)
	}
	var got model.CallLog
	db.First(&got)
	if got.TotalTokens != 38 || !got.UsageCorrected {
		t.Fatalf("journal record not normalised: %+v", got)
	}
	if mm, _ := Reconcile(db, l.CreatedAt, l.CreatedAt); len(mm) != 0 {
		t.Fatalf("reconcile: %+v", mm)
	}
}

// C2: rollup rows that predate the attempts column (NULL or missing) still accumulate
// attempts once the schema is upgraded, and the one-time startup upgrade rebuilds the
// window with real counts exactly once.
func TestUpgradeHourlyAttemptsAfterSchemaChange(t *testing.T) {
	db := testDB(t)
	hour := time.Now().UTC().Truncate(time.Hour)
	l := sample("new", 9)
	l.CreatedAt = hour.Add(time.Minute)
	l.AccountID, l.Provider = 2, "openai"
	l.Attempts = model.JSON(`[{"account_id":2,"provider":"openai","usage_status":"confirmed","prompt_tokens":9}]`)

	t.Run("column added by AutoMigrate", func(t *testing.T) {
		if err := db.Exec("ALTER TABLE usage_hourlies DROP COLUMN attempts").Error; err != nil {
			t.Fatal(err)
		}
		r := Aggregate([]*model.CallLog{l})[0]
		if err := db.Omit("Attempts").Create(r).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.AutoMigrate(model.All()...); err != nil {
			t.Fatal(err)
		}
		s := &Store{db: db}
		if err := s.commit([]*model.CallLog{l}); err != nil {
			t.Fatal(err)
		}
		var got sql.NullInt64
		if err := db.Raw("SELECT attempts FROM usage_hourlies WHERE id = ?", r.ID).Row().Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Valid || got.Int64 != 1 {
			t.Fatalf("attempts after upgrade + one commit = %+v", got)
		}
	})
	t.Run("nullable column with NULL rows", func(t *testing.T) {
		db.Exec("DELETE FROM usage_hourlies")
		db.Exec("DELETE FROM call_logs")
		if err := db.Exec("ALTER TABLE usage_hourlies DROP COLUMN attempts").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("ALTER TABLE usage_hourlies ADD COLUMN attempts bigint").Error; err != nil { // nullable, as the first 4526658 upgrade left it
			t.Fatal(err)
		}
		r := Aggregate([]*model.CallLog{l})[0]
		db.Omit("Attempts").Create(r)
		db.Exec("UPDATE usage_hourlies SET attempts = NULL")
		s := &Store{db: db}
		l2 := *l
		l2.RequestID = "new2"
		if err := s.commit([]*model.CallLog{&l2}); err != nil {
			t.Fatal(err)
		}
		var got sql.NullInt64
		db.Raw("SELECT attempts FROM usage_hourlies WHERE id = ?", r.ID).Row().Scan(&got)
		if !got.Valid || got.Int64 != 1 {
			t.Fatalf("NULL row must still accumulate: %+v", got)
		}
	})
	t.Run("startup upgrade rebuilds once", func(t *testing.T) {
		db.Exec("DELETE FROM usage_hourlies")
		db.Exec("DELETE FROM call_logs")
		db.Exec("DELETE FROM settings WHERE key = ?", usageUpgradeKey)
		db.Create(l) // raw log present; stale rollup row without attempts and wrong tokens
		db.Exec("INSERT INTO usage_hourlies (hour, user_id, group_id, api_key_id, account_id, provider, request_model, model_group, api_type, requests, success, failed, prompt_tokens, completion_tokens, total_tokens, cached_tokens, unknown_usage, latency_ms) VALUES (?, 1, 1, 0, 2, 'openai', 'm', '', 'text', 1, 1, 0, 0, 0, 0, 0, 0, 0)", hour)
		db.Exec("UPDATE usage_hourlies SET attempts = NULL")
		s, err := New(db, t.TempDir(), func() int { return 30 })
		if err != nil {
			t.Fatal(err)
		}
		st := s.Stats()
		s.Close(context.Background())
		var row model.UsageHourly
		db.Where("account_id = ?", 2).First(&row)
		if row.Attempts != 1 || row.TotalTokens != 9 || row.Requests != 1 {
			t.Fatalf("startup upgrade did not rebuild: %+v", row)
		}
		if st.AttemptsSince == nil || st.AttemptsSince.After(hour) {
			t.Fatalf("attempts_since = %v", st.AttemptsSince)
		}
		u1, _ := readUsageUpgrade(db)
		// Second start: marker present, nothing re-run.
		db.Exec("UPDATE usage_hourlies SET attempts = 5")
		s2, err := New(db, t.TempDir(), func() int { return 30 })
		if err != nil {
			t.Fatal(err)
		}
		s2.Close(context.Background())
		u2, _ := readUsageUpgrade(db)
		db.Where("account_id = ?", 2).First(&row)
		if row.Attempts != 5 || !u1.At.Equal(u2.At) {
			t.Fatalf("upgrade must run once: attempts=%d at1=%v at2=%v", row.Attempts, u1.At, u2.At)
		}
	})
}

// D1: a provider-reported total beyond prompt + completion that normalizeLegacyUsage
// keeps on the request must also reach the rollup, on both the rebuild and the journal
// path, without touching the attempts' own 13 / 25 and without double counting.
func TestExtraTotalTokensReachRollup(t *testing.T) {
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour).Add(time.Minute)
	mk := func(id string, total int64) *model.CallLog {
		l := legacyLog(id, at)
		l.PromptTokens, l.CompletionTokens, l.TotalTokens, l.UsageStatus, l.TokensKnown = 20, 5, total, model.UsageConfirmed, true
		l.Attempts = model.JSON(`[{"account_id":1,"provider":"openai","status_code":500,"usage_status":"confirmed","prompt_tokens":10,"completion_tokens":3},` +
			`{"account_id":2,"provider":"openai","status_code":200,"usage_status":"confirmed","prompt_tokens":20,"completion_tokens":5}]`)
		return l
	}
	check := func(t *testing.T, db *gorm.DB, wantTotal int64, extra int64) {
		t.Helper()
		var got model.CallLog
		db.Where("request_id = ?", "extra").First(&got)
		if got.TotalTokens != wantTotal || got.PromptTokens != 30 || got.CompletionTokens != 8 {
			t.Fatalf("log = %d/%d/%d", got.PromptTokens, got.CompletionTokens, got.TotalTokens)
		}
		var rows []model.UsageHourly
		db.Find(&rows)
		byAcc := map[uint]*model.UsageHourly{}
		var total, prompt, completion int64
		for i := range rows {
			byAcc[rows[i].AccountID] = &rows[i]
			total += rows[i].TotalTokens
			prompt += rows[i].PromptTokens
			completion += rows[i].CompletionTokens
		}
		if byAcc[1] == nil || byAcc[1].TotalTokens != 13 || byAcc[2] == nil || byAcc[2].TotalTokens != 25+extra {
			t.Fatalf("attribution: acc1=%+v acc2=%+v", byAcc[1], byAcc[2])
		}
		if total != wantTotal || prompt != 30 || completion != 8 {
			t.Fatalf("rollup sums = %d/%d/%d, want 30/8/%d", prompt, completion, total, wantTotal)
		}
		if mm, err := Reconcile(db, at, at); err != nil || len(mm) != 0 {
			t.Fatalf("reconcile: %v %+v", err, mm)
		}
	}
	for _, tc := range []struct {
		name  string
		total int64
		want  int64
		extra int64
	}{{"extra 5", 30, 43, 5}, {"no extra", 25, 38, 0}} {
		t.Run("rebuild/"+tc.name, func(t *testing.T) {
			db := testDB(t)
			db.Create(mk("extra", tc.total))
			if _, _, err := Rebuild(db, at, at, 30); err != nil {
				t.Fatal(err)
			}
			check(t, db, tc.want, tc.extra)
			if _, _, err := Rebuild(db, at, at, 30); err != nil {
				t.Fatal(err)
			}
			check(t, db, tc.want, tc.extra) // second rebuild: unchanged
		})
		t.Run("journal/"+tc.name, func(t *testing.T) {
			db := testDB(t)
			s := &Store{db: db}
			if err := s.commit([]*model.CallLog{mk("extra", tc.total)}); err != nil {
				t.Fatal(err)
			}
			check(t, db, tc.want, tc.extra)
			if err := s.commit([]*model.CallLog{mk("extra", tc.total)}); err != nil { // replay: idempotent
				t.Fatal(err)
			}
			check(t, db, tc.want, tc.extra)
		})
	}
}

// A database upgraded under an older rollup version rebuilds its complete window once
// more; a marker at the current version is left alone; a failed marker write retries.
func TestUpgradeVersionBumpRebuildsOnce(t *testing.T) {
	db := testDB(t)
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour).Add(time.Minute)
	l := legacyLog("v2", at)
	l.PromptTokens, l.CompletionTokens, l.TotalTokens, l.UsageStatus = 20, 5, 30, model.UsageConfirmed
	l.Attempts = model.JSON(`[{"account_id":1,"provider":"openai","usage_status":"confirmed","prompt_tokens":10,"completion_tokens":3},` +
		`{"account_id":2,"provider":"openai","usage_status":"confirmed","prompt_tokens":20,"completion_tokens":5}]`)
	db.Create(l)
	// Rollup as the version-2 code left it (38, extra lost) plus a version-less marker.
	db.Create(&model.UsageHourly{Hour: at.Truncate(time.Hour), UserID: 1, GroupID: 1, AccountID: 2, Provider: "openai", RequestModel: "m", APIType: "text", Requests: 1, Attempts: 2, TotalTokens: 38})
	db.Create(&model.Setting{Key: usageUpgradeKey, Value: `{"at":"2026-09-09T00:00:00Z","attempts_since":"2026-08-01T00:00:00Z","hours":2}`})
	s := &Store{db: db, retDays: func() int { return 30 }}
	if err := s.upgradeUsageRollup(); err != nil {
		t.Fatal(err)
	}
	u, _ := readUsageUpgrade(db)
	if u == nil || u.Version != usageUpgradeVersion || !u.AttemptsSince.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("marker after re-upgrade: %+v", u)
	}
	if mm, _ := Reconcile(db, at, at); len(mm) != 0 {
		t.Fatalf("still inconsistent after versioned re-upgrade: %+v", mm)
	}
	var rows []model.UsageHourly
	db.Find(&rows)
	var total int64
	for _, r := range rows {
		total += r.TotalTokens
	}
	if total != 43 {
		t.Fatalf("rollup total after re-upgrade = %d", total)
	}
	db.Exec("UPDATE usage_hourlies SET total_tokens = 1")
	if err := s.upgradeUsageRollup(); err != nil {
		t.Fatal(err)
	}
	db.Find(&rows)
	if rows[0].TotalTokens != 1 {
		t.Fatal("current-version marker must not trigger another rebuild")
	}
}
