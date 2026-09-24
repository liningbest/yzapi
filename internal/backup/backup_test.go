package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newDataDir builds a data directory the way the gateway lays it out: a SQLite
// database with a users table, a journal with two files and the two secrets.
func newDataDir(t *testing.T) (string, *gorm.DB) {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"data/db", "data/journal", "data/security"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "data/db/yzapi.db")+"?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT); INSERT INTO users(username) VALUES ('admin'), ('alice')").Error; err != nil {
		t.Fatal(err)
	}
	must := func(p, content string) {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must("data/journal/calls.jsonl", `{"id":"a"}`+"\n")
	must("data/journal/calls.ckpt", "12")
	must("data/security/credential.key", "KEY-BYTES")
	must("data/security/jwt.secret", "JWT-BYTES")
	return dir, db
}

func TestBackupRoundTrip(t *testing.T) {
	src, db := newDataDir(t)
	var buf bytes.Buffer
	m, err := Create(context.Background(), db, src, "v1.0.52", &buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"data/db/yzapi.db", "data/journal/calls.jsonl", "data/journal/calls.ckpt", "data/security/credential.key", "data/security/jwt.secret"} {
		if _, ok := m.Files[want]; !ok {
			t.Fatalf("manifest lacks %s: %v", want, m.Files)
		}
	}
	// The live database keeps working after the VACUUM INTO snapshot.
	if err := db.Exec("INSERT INTO users(username) VALUES ('bob')").Error; err != nil {
		t.Fatal(err)
	}

	// Restore into a fresh data dir, offline.
	dst := t.TempDir()
	archive := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreFile(dst, archive); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"data/journal/calls.jsonl": `{"id":"a"}` + "\n", "data/journal/calls.ckpt": "12", "data/security/credential.key": "KEY-BYTES", "data/security/jwt.secret": "JWT-BYTES"} {
		b, err := os.ReadFile(filepath.Join(dst, p))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", p, b, err)
		}
	}
	rdb, err := gorm.Open(sqlite.Open(filepath.Join(dst, "data/db/yzapi.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := rdb.Raw("SELECT count(*) FROM users").Scan(&n).Error; err != nil || n != 2 {
		t.Fatalf("restored users=%d err=%v (snapshot must predate bob)", n, err)
	}
	if _, pending := Pending(dst); pending {
		t.Fatal("staging left behind")
	}
	if _, err := os.Stat(filepath.Join(dst, "data/restore-staging")); !os.IsNotExist(err) {
		t.Fatal("staging dir not removed")
	}
}

// Staging on a live directory leaves everything in place until ApplyPending; the
// replaced directories are kept aside.
func TestStageThenApplyKeepsPrevious(t *testing.T) {
	src, db := newDataDir(t)
	var buf bytes.Buffer
	if _, err := Create(context.Background(), db, src, "v1", &buf); err != nil {
		t.Fatal(err)
	}
	live, _ := newDataDir(t)
	if err := os.WriteFile(filepath.Join(live, "data/security/credential.key"), []byte("OLD-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(live, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "data/security/credential.key")); string(b) != "OLD-KEY" {
		t.Fatalf("stage touched live files: %q", b)
	}
	if _, pending := Pending(live); !pending {
		t.Fatal("not pending")
	}
	applied, err := ApplyPending(live)
	if err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
	if b, _ := os.ReadFile(filepath.Join(live, "data/security/credential.key")); string(b) != "KEY-BYTES" {
		t.Fatalf("restored key %q", b)
	}
	kept, _ := filepath.Glob(filepath.Join(live, "data", "pre-restore-*", "security", "credential.key"))
	if len(kept) != 1 {
		t.Fatalf("previous files not kept: %v", kept)
	}
	if b, _ := os.ReadFile(kept[0]); string(b) != "OLD-KEY" {
		t.Fatalf("kept key %q", b)
	}
	if applied, err := ApplyPending(live); err != nil || applied {
		t.Fatalf("second apply: %v %v", applied, err)
	}
}

func pack(t *testing.T, m *Manifest, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if m != nil {
		b, _ := json.Marshal(m)
		_ = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(b)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(b)
	}
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = io_WriteString(tw, content)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func io_WriteString(w *tar.Writer, s string) (int, error) { return w.Write([]byte(s)) }

// Rejections: tampered bytes, a path outside the data directories, a listed file that
// is missing, an unlisted extra file, no manifest, a non-yzapi database, and garbage.
func TestStageRejects(t *testing.T) {
	src, db := newDataDir(t)
	var good bytes.Buffer
	m, err := Create(context.Background(), db, src, "v1", &good)
	if err != nil {
		t.Fatal(err)
	}
	live, _ := newDataDir(t)
	expectReject := func(name string, archive []byte, want string) {
		t.Helper()
		_, err := Stage(live, bytes.NewReader(archive))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err=%v want %q", name, err, want)
		}
		if _, pending := Pending(live); pending {
			t.Fatalf("%s: rejected archive left a pending restore", name)
		}
	}
	// Tamper: the manifest's checksum for the journal no longer matches its bytes.
	m2 := m
	m2.Files = map[string]string{"data/journal/calls.jsonl": m.Files["data/journal/calls.jsonl"]}
	tampered := pack(t, &m2, map[string]string{"data/journal/calls.jsonl": "changed"})
	expectReject("tamper", tampered, "checksum mismatch")
	expectReject("traversal", pack(t, &m2, map[string]string{"data/../etc/passwd": "x"}), "not allowed")
	expectReject("outside", pack(t, &m2, map[string]string{"data/other/x": "x"}), "not allowed")
	expectReject("no manifest", pack(t, nil, map[string]string{"data/journal/calls.jsonl": "x"}), "no manifest")
	extra := m2
	extra.Files = map[string]string{"data/journal/calls.jsonl": m.Files["data/journal/calls.jsonl"]}
	expectReject("unlisted file", pack(t, &extra, map[string]string{"data/journal/calls.jsonl": `{"id":"a"}` + "\n", "data/journal/other.txt": "x"}), "does not list")
	expectReject("missing db", pack(t, &extra, map[string]string{"data/journal/calls.jsonl": `{"id":"a"}` + "\n"}), "incomplete")
	expectReject("garbage", []byte("not an archive at all"), "gzip")
	// A checksum-valid archive whose "database" is not a yzapi database.
	fake := Manifest{Format: FormatVersion, DBDriver: "sqlite", Files: map[string]string{}}
	files := map[string]string{"data/db/yzapi.db": "not sqlite", "data/security/credential.key": "k"}
	for k, v := range files {
		sum, _ := sha256Hex([]byte(v))
		fake.Files[k] = sum
	}
	expectReject("fake db", pack(t, &fake, files), "database in backup")
	// The good archive still stages after all the rejections.
	if _, err := Stage(live, bytes.NewReader(good.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(b []byte) (string, error) {
	p := filepath.Join(os.TempDir(), "yzapi-sha-"+strings.ReplaceAll(string(b[:min(len(b), 6)]), "/", "_"))
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	defer os.Remove(p)
	return fileSHA256(p)
}

func TestLocalBackupsListPruneAndNames(t *testing.T) {
	dir, db := newDataDir(t)
	for i := 0; i < 3; i++ {
		if _, err := CreateLocal(context.Background(), db, dir, "v1.0.52-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	list, err := List(dir)
	if err != nil || len(list) != 3 {
		t.Fatalf("list %v %v", list, err)
	}
	for _, it := range list {
		if !ValidName(it.Name) || it.Size == 0 {
			t.Fatalf("bad entry %+v", it)
		}
	}
	if n, _ := Prune(dir, 2); n != 1 {
		t.Fatalf("pruned %d", n)
	}
	if list, _ = List(dir); len(list) != 2 {
		t.Fatalf("after prune %d", len(list))
	}
	if _, err := Path(dir, "../etc/passwd"); err == nil {
		t.Fatal("traversal name accepted")
	}
	if ValidName("yzapi-backup-20260924-101010-v1.tar.gz/../x") || ValidName("x.tar.gz") {
		t.Fatal("invalid names accepted")
	}
	if err := Delete(dir, list[0].Name); err != nil {
		t.Fatal(err)
	}
	if list, _ = List(dir); len(list) != 1 {
		t.Fatalf("after delete %d", len(list))
	}
}
