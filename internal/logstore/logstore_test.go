package logstore

import (
	"context"
	"encoding/json"
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
	hours, rows, err := Rebuild(db, from, from.Add(24*time.Hour))
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
