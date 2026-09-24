package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/logstore"
	"yzapi/internal/model"
)

// hookWriter runs fn once, on the first write of the archive stream.
type hookWriter struct {
	bytes.Buffer
	fn   func()
	done bool
}

func (w *hookWriter) Write(p []byte) (int, error) {
	if !w.done {
		w.done = true
		w.fn()
	}
	return w.Buffer.Write(p)
}

// R152-01 (a): journal appends, commits and a rotation while the archive is being
// written never change what was archived; the archive always stages.
func TestR152ArchiveIsImmutableWhileJournalChanges(t *testing.T) {
	for _, mutate := range []struct {
		name string
		fn   func(dir string) error
	}{
		{"append", func(dir string) error {
			f, err := os.OpenFile(filepath.Join(dir, "data/journal/calls.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, err = f.WriteString(`{"id":"b"}` + "\n")
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			return err
		}},
		{"checkpoint advance", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "data/journal/calls.ckpt"), []byte("999"), 0o600)
		}},
		{"rotation", func(dir string) error {
			if err := os.Truncate(filepath.Join(dir, "data/journal/calls.jsonl"), 0); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "data/journal/calls.ckpt"), []byte("0"), 0o600)
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			dir, db := newDataDir(t)
			w := &hookWriter{fn: func() {
				if err := mutate.fn(dir); err != nil {
					t.Fatal(err)
				}
			}}
			if _, err := Create(context.Background(), db, dir, "v", w); err != nil {
				t.Fatal(err)
			}
			if !w.done {
				t.Fatal("hook not run")
			}
			dst := t.TempDir()
			if _, err := Stage(dst, bytes.NewReader(w.Bytes())); err != nil {
				t.Fatalf("archive does not stage: %v", err)
			}
			b, _ := os.ReadFile(filepath.Join(dst, stagingDir, "data/journal/calls.jsonl"))
			if string(b) != `{"id":"a"}`+"\n" {
				t.Fatalf("archived journal %q", b)
			}
		})
	}
}

// R152-01 (b): a record durable in the journal before the backup is restored exactly
// once, whether the source committed it before, during or after the snapshot.
func TestR152PreexistingJournalRecordRestoredOnce(t *testing.T) {
	for _, when := range []string{"never", "before", "after-snapshot"} {
		t.Run(when, func(t *testing.T) {
			dir, db := newDataDir(t)
			if err := db.AutoMigrate(model.All()...); err != nil {
				t.Fatal(err)
			}
			rec := model.CallLog{RequestID: "r152-" + when, UserID: 1, GroupID: 1, Provider: "mock", RequestModel: "m", APIType: "text",
				Result: "success", StatusCode: 200, PromptTokens: 17, TotalTokens: 17, UsageStatus: model.UsageConfirmed, CreatedAt: time.Now()}
			line, _ := json.Marshal(rec)
			if err := os.WriteFile(filepath.Join(dir, "data/journal/calls.jsonl"), append(line, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "data/journal/calls.ckpt"), []byte("0"), 0o600); err != nil {
				t.Fatal(err)
			}
			commit := func() {
				st, err := logstore.New(db, dir, func() int { return 30 })
				if err != nil {
					t.Fatal(err)
				}
				st.Close(context.Background())
			}
			if when == "before" {
				commit()
			}
			name := "r152:" + when
			if when == "after-snapshot" {
				fired := false
				if err := db.Callback().Raw().After("gorm:raw").Register(name, func(tx *gorm.DB) {
					if !fired && strings.HasPrefix(tx.Statement.SQL.String(), "VACUUM INTO") {
						fired = true
						commit()
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer db.Callback().Raw().Remove(name)
			}
			var buf bytes.Buffer
			if _, err := Create(context.Background(), db, dir, "v", &buf); err != nil {
				t.Fatal(err)
			}
			dst := t.TempDir()
			if _, err := Stage(dst, bytes.NewReader(buf.Bytes())); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyPending(dst); err != nil {
				t.Fatal(err)
			}
			rdb, err := gorm.Open(sqlite.Open(filepath.Join(dst, "data/db/yzapi.db")), &gorm.Config{Logger: logger.Discard})
			if err != nil {
				t.Fatal(err)
			}
			replay, err := logstore.New(rdb, dst, func() int { return 30 })
			if err != nil {
				t.Fatal(err)
			}
			replay.Close(context.Background())
			var n int64
			rdb.Model(&model.CallLog{}).Where("request_id = ?", rec.RequestID).Count(&n)
			var tokens int64
			rdb.Model(&model.CallLog{}).Where("request_id = ?", rec.RequestID).Select("COALESCE(SUM(prompt_tokens),0)").Scan(&tokens)
			if n != 1 || tokens != 17 {
				t.Fatalf("restored count=%d tokens=%d, want exactly one record with 17 tokens", n, tokens)
			}
		})
	}
}

// R152-02: interrupting ApplyPending after any number of its moves and running it again
// always ends with the complete new instance in place and the complete old one set aside.
func TestR152ApplyResumesAfterEveryMove(t *testing.T) {
	src, db := newDataDir(t)
	var buf bytes.Buffer
	if _, err := Create(context.Background(), db, src, "v", &buf); err != nil {
		t.Fatal(err)
	}
	steps := []struct{ from, to string }{} // filled per run: aside + install for db, journal, security
	for k := 0; k <= 6; k++ {
		live, _ := newDataDir(t)
		if err := os.WriteFile(filepath.Join(live, "data/security/credential.key"), []byte("OLD-KEY"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Stage(live, bytes.NewReader(buf.Bytes())); err != nil {
			t.Fatal(err)
		}
		keep, err := asideDir(live)
		if err != nil {
			t.Fatal(err)
		}
		steps = steps[:0]
		for _, rel := range []string{"data/db", "data/journal", "data/security"} {
			steps = append(steps,
				struct{ from, to string }{filepath.Join(live, rel), filepath.Join(keep, filepath.Base(rel))},
				struct{ from, to string }{filepath.Join(live, stagingDir, rel), filepath.Join(live, rel)})
		}
		_ = os.MkdirAll(keep, 0o750)
		for i := 0; i < k; i++ {
			if err := os.Rename(steps[i].from, steps[i].to); err != nil {
				t.Fatalf("k=%d step %d: %v", k, i, err)
			}
		}
		applied, err := ApplyPending(live)
		if err != nil || !applied {
			t.Fatalf("k=%d: applied=%v err=%v", k, applied, err)
		}
		if _, pending := Pending(live); pending {
			t.Fatalf("k=%d: still pending", k)
		}
		if b, _ := os.ReadFile(filepath.Join(live, "data/security/credential.key")); string(b) != "KEY-BYTES" {
			t.Fatalf("k=%d: live key %q", k, b)
		}
		if b, _ := os.ReadFile(filepath.Join(keep, "security/credential.key")); string(b) != "OLD-KEY" {
			t.Fatalf("k=%d: kept key %q", k, b)
		}
		rdb, err := gorm.Open(sqlite.Open(filepath.Join(live, "data/db/yzapi.db")+"?mode=ro"), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		var users int64
		rdb.Raw("SELECT count(*) FROM users").Scan(&users)
		if sqlDB, _ := rdb.DB(); sqlDB != nil {
			sqlDB.Close()
		}
		if users != 2 {
			t.Fatalf("k=%d: live database users=%d", k, users)
		}
		for _, d := range []string{"db", "journal", "security"} {
			if _, err := os.Stat(filepath.Join(keep, d)); err != nil {
				t.Fatalf("k=%d: old %s not kept: %v", k, d, err)
			}
		}
		if others, _ := filepath.Glob(filepath.Join(live, "data", "pre-restore-*")); len(others) != 1 {
			t.Fatalf("k=%d: set-aside directories %v", k, others)
		}
	}
}

// R152-02: a required directory that is neither staged nor live stops the start
// instead of reporting success.
func TestR152ApplyRefusesIncompleteResult(t *testing.T) {
	src, db := newDataDir(t)
	var buf bytes.Buffer
	if _, err := Create(context.Background(), db, src, "v", &buf); err != nil {
		t.Fatal(err)
	}
	live, _ := newDataDir(t)
	if _, err := Stage(live, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(live, stagingDir, "data/db")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(live, "data/db")); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPending(live); !errors.Is(err, ErrIncompleteRestore) {
		t.Fatalf("err=%v", err)
	}
	if _, pending := Pending(live); !pending {
		t.Fatal("pending marker dropped after an incomplete apply")
	}
}

// R152-03: a rejected upload keeps an accepted one; a second valid upload is refused;
// concurrent uploads never mix and exactly one wins.
func TestR152StagingIsolation(t *testing.T) {
	src, db := newDataDir(t)
	var a, b bytes.Buffer
	if _, err := Create(context.Background(), db, src, "a", &a); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), db, src, "b", &b); err != nil {
		t.Fatal(err)
	}
	live, _ := newDataDir(t)
	if _, err := Stage(live, bytes.NewReader(a.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(live, strings.NewReader("not a backup")); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := Stage(live, bytes.NewReader(b.Bytes())); !errors.Is(err, ErrRestorePending) {
		t.Fatalf("second valid upload: %v", err)
	}
	m, ok := Pending(live)
	if !ok || m.AppVersion != "a" {
		t.Fatalf("pending %+v %v", m, ok)
	}
	if incoming, _ := filepath.Glob(filepath.Join(live, "data", "restore-incoming-*")); len(incoming) != 0 {
		t.Fatalf("private upload dirs left behind: %v", incoming)
	}

	// Concurrent: eight uploads of two different archives.
	race, _ := newDataDir(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, refused := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		data := a.Bytes()
		if i%2 == 1 {
			data = b.Bytes()
		}
		go func(data []byte) {
			defer wg.Done()
			_, err := Stage(race, bytes.NewReader(data))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrRestorePending):
				refused++
			default:
				t.Errorf("unexpected: %v", err)
			}
		}(data)
	}
	wg.Wait()
	if wins != 1 || refused != 7 {
		t.Fatalf("wins=%d refused=%d", wins, refused)
	}
	pm, _ := Pending(race)
	// The staged files must all belong to the winning archive.
	for rel, sum := range pm.Files {
		got, err := fileSHA256(filepath.Join(race, stagingDir, filepath.FromSlash(rel)))
		if err != nil || got != sum {
			t.Fatalf("%s mixed or missing: %v", rel, err)
		}
	}
}

// R152-04: only the default SQLite path is supported.
func TestR152UnsupportedConfigurations(t *testing.T) {
	for _, tc := range []struct {
		driver, dsn string
		ok          bool
	}{{"sqlite", "", true}, {"", "", true}, {"sqlite", "/tmp/custom.sqlite", false}, {"postgres", "host=x", false}, {"postgres", "", false}} {
		if got := Unsupported(tc.driver, tc.dsn) == ""; got != tc.ok {
			t.Fatalf("Unsupported(%q,%q) ok=%v want %v", tc.driver, tc.dsn, got, tc.ok)
		}
	}
}
