package logstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
)

func r154DB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "t.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	return db, dir
}

func r154Line(t *testing.T, id string, age time.Duration) []byte {
	t.Helper()
	b, err := json.Marshal(model.CallLog{RequestID: id, UserID: 1, GroupID: 1, Provider: "mock", RequestModel: "m", APIType: "text",
		Result: "success", StatusCode: 200, PromptTokens: 17, TotalTokens: 17, UsageStatus: model.UsageConfirmed, CreatedAt: time.Now().Add(-age)})
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func r154Rollup(t *testing.T, db *gorm.DB) (req, tok int64) {
	t.Helper()
	var r struct{ Requests, Tokens int64 }
	db.Model(&model.UsageHourly{}).Select("COALESCE(SUM(requests),0) AS requests, COALESCE(SUM(prompt_tokens+completion_tokens),0) AS tokens").Scan(&r)
	return r.Requests, r.Tokens
}

// R154-01: a crash after the commit transaction (which records the database boundary)
// but before the file checkpoint write leaves the file behind. On restart the database
// boundary wins, so a committed record whose raw row retention already purged is not
// added to the rollup again, while a record after the boundary is still replayed once.
func TestR154StartupKeepsNewerDatabaseBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
	}{{"recent", time.Hour}, {"expired", 45 * 24 * time.Hour}} {
		t.Run(tc.name, func(t *testing.T) {
			db, dir := r154DB(t)
			jdir := filepath.Join(dir, "data", "journal")
			if err := os.MkdirAll(jdir, 0o750); err != nil {
				t.Fatal(err)
			}
			first := r154Line(t, "r154-"+tc.name, tc.age)
			if err := os.WriteFile(filepath.Join(jdir, journalFile), first, 0o600); err != nil {
				t.Fatal(err)
			}
			st, err := New(db, dir, func() int { return 30 })
			if err != nil {
				t.Fatal(err)
			}
			st.Close(context.Background())
			if r, k := r154Rollup(t, db); r != 1 || k != 17 {
				t.Fatalf("after first commit rollup %d/%d", r, k)
			}
			_, off, ok, err := JournalState(db)
			if err != nil || !ok || off != int64(len(first)) {
				t.Fatalf("boundary off=%d ok=%v err=%v", off, ok, err)
			}
			// Crash window: the file checkpoint never reached the committed offset.
			if err := os.WriteFile(filepath.Join(jdir, checkpointFile), []byte("0"), 0o600); err != nil {
				t.Fatal(err)
			}
			// A new record after the boundary, not yet committed.
			f, err := os.OpenFile(filepath.Join(jdir, journalFile), os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			second := r154Line(t, "r154-next-"+tc.name, time.Minute)
			_, _ = f.Write(second)
			f.Close()
			st, err = New(db, dir, func() int { return 30 })
			if err != nil {
				t.Fatal(err)
			}
			st.Close(context.Background())
			if r, k := r154Rollup(t, db); r != 2 || k != 34 {
				t.Fatalf("rollup %d/%d, want 2/34 (the committed record once, the new one once)", r, k)
			}
			ck, _ := os.ReadFile(filepath.Join(jdir, checkpointFile))
			if string(ck) != strconv.Itoa(len(first)+len(second)) {
				t.Fatalf("file checkpoint %q", ck)
			}
		})
	}
}

// The database boundary is ignored when it is not a valid checkpoint for the current
// file: beyond its end, or not on a line boundary.
func TestR154InvalidDatabaseBoundaryIgnored(t *testing.T) {
	for _, bad := range []int64{1 << 20, 5} {
		db, dir := r154DB(t)
		jdir := filepath.Join(dir, "data", "journal")
		_ = os.MkdirAll(jdir, 0o750)
		line := r154Line(t, "r154-bad-"+strconv.FormatInt(bad, 10), time.Minute)
		if err := os.WriteFile(filepath.Join(jdir, journalFile), line, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := saveJournalState(db, 0, bad); err != nil {
			t.Fatal(err)
		}
		st, err := New(db, dir, func() int { return 30 })
		if err != nil {
			t.Fatal(err)
		}
		st.Close(context.Background())
		var n int64
		db.Model(&model.CallLog{}).Count(&n)
		if n != 1 {
			t.Fatalf("boundary %d: replayed %d records, want the uncommitted line replayed once", bad, n)
		}
	}
}
