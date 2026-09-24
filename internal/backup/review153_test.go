package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/logstore"
	"yzapi/internal/model"
)

type rollupTotals struct{ Requests, Tokens int64 }

func rollups(t *testing.T, db *gorm.DB) rollupTotals {
	t.Helper()
	var r rollupTotals
	if err := db.Model(&model.UsageHourly{}).Select("COALESCE(SUM(requests),0) AS requests, COALESCE(SUM(prompt_tokens + completion_tokens),0) AS tokens").Scan(&r).Error; err != nil {
		t.Fatal(err)
	}
	return r
}

// sourceWithRecord builds a data dir whose journal holds one record of the given age,
// committed through the real journal writer (which also purges raw rows older than the
// 30-day retention while keeping the hourly rollup).
func sourceWithRecord(t *testing.T, age time.Duration, id string) (string, *gorm.DB) {
	t.Helper()
	dir, db := newDataDir(t)
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	rec := model.CallLog{RequestID: id, UserID: 1, GroupID: 1, Provider: "mock", RequestModel: "m", APIType: "text", Result: "success",
		StatusCode: 200, PromptTokens: 17, TotalTokens: 17, UsageStatus: model.UsageConfirmed, CreatedAt: time.Now().Add(-age)}
	line, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, "data/journal/calls.jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data/journal/calls.ckpt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := logstore.New(db, dir, func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	st.Close(context.Background())
	return dir, db
}

func restoreInto(t *testing.T, archive []byte) *gorm.DB {
	t.Helper()
	dst := t.TempDir()
	if _, err := Stage(dst, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPending(dst); err != nil {
		t.Fatal(err)
	}
	rdb, err := gorm.Open(sqlite.Open(filepath.Join(dst, "data/db/yzapi.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st, err := logstore.New(rdb, dst, func() int { return 30 })
	if err != nil {
		t.Fatal(err)
	}
	st.Close(context.Background())
	return rdb
}

// R153-01: a journal record committed long ago, whose raw row retention already
// purged, is not added to the rollup again by a restore; restoring the same archive
// twice gives the same totals; a recent record is unaffected.
func TestR153CommittedRecordsNotReplayedAfterPurge(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
		raw  int64
	}{{"recent", time.Hour, 1}, {"expired", 45 * 24 * time.Hour, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			dir, db := sourceWithRecord(t, tc.age, "r153-"+tc.name)
			var raw int64
			db.Model(&model.CallLog{}).Count(&raw)
			before := rollups(t, db)
			if raw != tc.raw || before != (rollupTotals{1, 17}) {
				t.Fatalf("source raw=%d rollup=%+v", raw, before)
			}
			var buf bytes.Buffer
			if _, err := Create(context.Background(), db, dir, "v", &buf); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if got := rollups(t, restoreInto(t, buf.Bytes())); got != before {
					t.Fatalf("restore %d: rollup %+v, want %+v", i+1, got, before)
				}
			}
		})
	}
}

// A rotation between the journal copy and the snapshot (generation moved) means the
// copy was fully committed: nothing is replayed. Simulated by bumping the recorded
// generation right after VACUUM INTO, as maybeRotate does before truncating.
func TestR153GenerationMovedMeansCopyCommitted(t *testing.T) {
	dir, db := sourceWithRecord(t, 45*24*time.Hour, "r153-rotate")
	gen, _, _, err := logstore.JournalState(db)
	if err != nil {
		t.Fatal(err)
	}
	fired := false
	if err := db.Callback().Raw().After("gorm:raw").Register("r153:rotate", func(tx *gorm.DB) {
		if !fired && strings.HasPrefix(tx.Statement.SQL.String(), "VACUUM INTO") {
			fired = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Raw().Remove("r153:rotate")
	// Bump the generation before the snapshot, after the copy: register on the VACUUM
	// statement's Before hook instead.
	if err := db.Callback().Raw().Before("gorm:raw").Register("r153:rotate-before", func(tx *gorm.DB) {
		if strings.HasPrefix(tx.Statement.SQL.String(), "VACUUM INTO") {
			if err := db.Session(&gorm.Session{NewDB: true, SkipHooks: true}).Exec("UPDATE settings SET value = ? WHERE key = 'journal_state'", itoa64(gen+1)+":0").Error; err != nil {
				t.Error(err)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Raw().Remove("r153:rotate-before")
	var buf bytes.Buffer
	if _, err := Create(context.Background(), db, dir, "v", &buf); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("snapshot hook not run")
	}
	if got := rollups(t, restoreInto(t, buf.Bytes())); got != (rollupTotals{1, 17}) {
		t.Fatalf("rollup %+v", got)
	}
}

// The archived checkpoint never points into a torn last line, and a database without a
// recorded boundary (written before the marker existed) archives checkpoint 0.
func TestR153CheckpointClampAndLegacy(t *testing.T) {
	dir, db := sourceWithRecord(t, time.Hour, "r153-torn")
	f, err := os.OpenFile(filepath.Join(dir, "data/journal/calls.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"request_id":"partial`)
	f.Close()
	_, off, ok, err := logstore.JournalState(db)
	if err != nil || !ok {
		t.Fatalf("state ok=%v err=%v", ok, err)
	}
	var buf bytes.Buffer
	if _, err := Create(context.Background(), db, dir, "v", &buf); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if _, err := Stage(dst, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	ck, _ := os.ReadFile(filepath.Join(dst, stagingDir, "data/journal/calls.ckpt"))
	if string(ck) != itoa64(off) {
		t.Fatalf("archived checkpoint %q, want %d (the committed boundary, before the torn tail)", ck, off)
	}
	// Legacy: remove the recorded boundary.
	if err := db.Exec("DELETE FROM settings WHERE key = 'journal_state'").Error; err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if _, err := Create(context.Background(), db, dir, "v", &buf); err != nil {
		t.Fatal(err)
	}
	dst = t.TempDir()
	if _, err := Stage(dst, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	if ck, _ = os.ReadFile(filepath.Join(dst, stagingDir, "data/journal/calls.ckpt")); string(ck) != "0" {
		t.Fatalf("legacy checkpoint %q", ck)
	}
}

func itoa64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Leftover private upload directories are removed at startup.
func TestR153CleanupIncoming(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"data/restore-incoming-1", "data/restore-incoming-2/data/db"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	CleanupIncoming(dir)
	if left, _ := filepath.Glob(filepath.Join(dir, "data", "restore-incoming-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}
