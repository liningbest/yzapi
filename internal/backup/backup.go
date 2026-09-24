// Package backup creates and restores full-instance backups: a consistent copy of the
// SQLite database, the metering journal and the two secret files, packed as a tar.gz
// with a manifest of SHA-256 checksums. A restore is staged while the gateway runs and
// applied on the next start (before the database is opened), so the running process
// never swaps files under its own feet.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// FormatVersion is bumped when the archive layout changes incompatibly.
const FormatVersion = 1

// Manifest describes an archive. Files maps archive paths (e.g. "data/db/yzapi.db") to
// SHA-256 hex digests.
type Manifest struct {
	Format     int               `json:"format"`
	AppVersion string            `json:"app_version"`
	CreatedAt  time.Time         `json:"created_at"`
	DBDriver   string            `json:"db_driver"`
	Files      map[string]string `json:"files"`
}

const (
	manifestName = "manifest.json"
	dbRel        = "data/db/yzapi.db"
	journalDir   = "data/journal"
	securityDir  = "data/security"
	stagingDir   = "data/restore-staging"
	pendingName  = "pending.json"
	backupsDir   = "backups"
)

// allowedPrefixes are the only archive paths a manifest may list.
var allowedPrefixes = []string{dbRel, journalDir + "/", securityDir + "/"}

// ErrUnsupportedDriver: backups cover the embedded SQLite database only.
var ErrUnsupportedDriver = errors.New("backup supports the embedded SQLite database only; use pg_dump for PostgreSQL")

// Create writes a backup archive of dataDir to w. The database is copied with
// VACUUM INTO on the live connection, which yields a consistent snapshot without
// stopping writers; journal and secret files are copied as they are (the journal is
// replayed idempotently after a restore).
func Create(ctx context.Context, db *gorm.DB, dataDir, appVersion string, w io.Writer) (Manifest, error) {
	m := Manifest{Format: FormatVersion, AppVersion: appVersion, CreatedAt: time.Now(), Files: map[string]string{}}
	if db == nil || db.Dialector.Name() != "sqlite" {
		return m, ErrUnsupportedDriver
	}
	m.DBDriver = "sqlite"
	tmp, err := os.MkdirTemp(dataDir, ".backup-*")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)
	snap := filepath.Join(tmp, "yzapi.db")
	if err := db.WithContext(ctx).Exec("VACUUM INTO ?", snap).Error; err != nil {
		return m, fmt.Errorf("database snapshot: %w", err)
	}
	type entry struct{ rel, src string }
	entries := []entry{{dbRel, snap}}
	for _, dir := range []string{journalDir, securityDir} {
		names, _ := os.ReadDir(filepath.Join(dataDir, dir))
		for _, n := range names {
			if n.Type().IsRegular() {
				entries = append(entries, entry{dir + "/" + n.Name(), filepath.Join(dataDir, dir, n.Name())})
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, securityDir, "credential.key")); err != nil {
		return m, errors.New("credential.key is missing; refusing to write a backup that could not decrypt its own accounts")
	}
	for _, e := range entries {
		sum, err := fileSHA256(e.src)
		if err != nil {
			return m, err
		}
		m.Files[e.rel] = sum
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := writeTarBytes(tw, manifestName, mb, m.CreatedAt); err != nil {
		return m, err
	}
	for _, e := range entries {
		if err := writeTarFile(tw, e.rel, e.src); err != nil {
			return m, err
		}
	}
	if err := tw.Close(); err != nil {
		return m, err
	}
	return m, gz.Close()
}

// Stage unpacks and validates an archive into dataDir/data/restore-staging and marks it
// pending. Nothing live is touched; ApplyPending swaps the files in on the next start.
// Every listed file must be present with a matching checksum, paths are confined to the
// three data directories, and the database must open and contain the users table.
func Stage(dataDir string, r io.Reader) (Manifest, error) {
	var m Manifest
	staging := filepath.Join(dataDir, stagingDir)
	if err := os.RemoveAll(staging); err != nil {
		return m, err
	}
	if err := os.MkdirAll(staging, 0o750); err != nil {
		return m, err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return m, fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(gz)
	seen := map[string]string{}
	haveManifest := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("reading archive: %w", err)
		}
		name := filepath.ToSlash(h.Name)
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if name == manifestName {
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return m, err
			}
			if err := json.Unmarshal(b, &m); err != nil {
				return m, fmt.Errorf("manifest: %w", err)
			}
			haveManifest = true
			continue
		}
		if !allowedPath(name) {
			return m, fmt.Errorf("archive path not allowed: %s", name)
		}
		dst := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return m, err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return m, err
		}
		hsh := sha256.New()
		if _, err := io.Copy(io.MultiWriter(f, hsh), tr); err != nil {
			f.Close()
			return m, err
		}
		if err := f.Close(); err != nil {
			return m, err
		}
		seen[name] = hex.EncodeToString(hsh.Sum(nil))
	}
	if !haveManifest {
		return m, errors.New("archive has no manifest.json; not a yzapi backup")
	}
	if m.Format != FormatVersion {
		return m, fmt.Errorf("unsupported backup format %d (this build reads %d)", m.Format, FormatVersion)
	}
	if m.DBDriver != "sqlite" {
		return m, ErrUnsupportedDriver
	}
	for rel, want := range m.Files {
		if !allowedPath(rel) {
			return m, fmt.Errorf("manifest path not allowed: %s", rel)
		}
		got, ok := seen[rel]
		if !ok {
			return m, fmt.Errorf("file listed in manifest is missing from the archive: %s", rel)
		}
		if got != want {
			return m, fmt.Errorf("checksum mismatch for %s", rel)
		}
	}
	for rel := range seen {
		if _, ok := m.Files[rel]; !ok {
			return m, fmt.Errorf("archive contains a file the manifest does not list: %s", rel)
		}
	}
	for _, must := range []string{dbRel, securityDir + "/credential.key"} {
		if _, ok := m.Files[must]; !ok {
			return m, fmt.Errorf("backup is incomplete: %s is missing", must)
		}
	}
	if err := checkDatabase(filepath.Join(staging, filepath.FromSlash(dbRel))); err != nil {
		return m, fmt.Errorf("database in backup: %w", err)
	}
	pb, _ := json.Marshal(m)
	tmp := filepath.Join(staging, pendingName+".tmp")
	if err := os.WriteFile(tmp, pb, 0o600); err != nil {
		return m, err
	}
	return m, os.Rename(tmp, filepath.Join(staging, pendingName))
}

// Pending reports whether a staged restore is waiting for the next start.
func Pending(dataDir string) (*Manifest, bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, stagingDir, pendingName))
	if err != nil {
		return nil, false
	}
	var m Manifest
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	return &m, true
}

// ApplyPending swaps a staged restore into place. Must run before the database is
// opened. The replaced directories are kept under data/pre-restore-<timestamp>/ so a
// bad restore can be undone by hand. Returns false when nothing was pending.
func ApplyPending(dataDir string) (bool, error) {
	staging := filepath.Join(dataDir, stagingDir)
	if _, ok := Pending(dataDir); !ok {
		return false, nil
	}
	keep := filepath.Join(dataDir, "data", "pre-restore-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(keep, 0o750); err != nil {
		return false, err
	}
	for _, rel := range []string{"data/db", journalDir, securityDir} {
		cur := filepath.Join(dataDir, filepath.FromSlash(rel))
		if _, err := os.Stat(cur); err == nil {
			if err := os.Rename(cur, filepath.Join(keep, filepath.Base(rel))); err != nil {
				return false, fmt.Errorf("set aside %s: %w", rel, err)
			}
		}
	}
	for _, rel := range []string{"data/db", journalDir, securityDir} {
		src := filepath.Join(staging, filepath.FromSlash(rel))
		dst := filepath.Join(dataDir, filepath.FromSlash(rel))
		if _, err := os.Stat(src); err != nil {
			continue // a backup without a journal directory restores an empty journal
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return false, err
		}
		if err := os.Rename(src, dst); err != nil {
			return false, fmt.Errorf("install %s: %w", rel, err)
		}
	}
	return true, os.RemoveAll(staging)
}

// RestoreFile stages an archive from disk and applies it immediately (offline use:
// `yzapi -restore backup.tar.gz` on a stopped instance or a fresh server).
func RestoreFile(dataDir, path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	m, err := Stage(dataDir, f)
	if err != nil {
		return m, err
	}
	if _, err := ApplyPending(dataDir); err != nil {
		return m, err
	}
	return m, nil
}

// Info is one archive kept under dataDir/backups.
type Info struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

var nameRE = regexp.MustCompile(`^yzapi-backup-[0-9]{8}-[0-9]{6}-[A-Za-z0-9._-]+\.tar\.gz$`)

// ValidName guards the download / delete endpoints against path tricks.
func ValidName(name string) bool { return nameRE.MatchString(name) && !strings.Contains(name, "/") }

// Dir returns the local backup directory (created on demand).
func Dir(dataDir string) (string, error) {
	d := filepath.Join(dataDir, backupsDir)
	return d, os.MkdirAll(d, 0o750)
}

// CreateLocal writes a backup into dataDir/backups and returns its Info.
func CreateLocal(ctx context.Context, db *gorm.DB, dataDir, appVersion string) (Info, error) {
	dir, err := Dir(dataDir)
	if err != nil {
		return Info{}, err
	}
	ver := strings.TrimPrefix(appVersion, "v")
	if ver == "" {
		ver = "dev"
	}
	ver = regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(ver, "_")
	name := fmt.Sprintf("yzapi-backup-%s-%s.tar.gz", time.Now().Format("20060102-150405"), ver)
	tmp := filepath.Join(dir, "."+name+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Info{}, err
	}
	if _, err := Create(ctx, db, dataDir, appVersion, f); err != nil {
		f.Close()
		os.Remove(tmp)
		return Info{}, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return Info{}, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return Info{}, err
	}
	st, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return Info{}, err
	}
	return Info{Name: name, Size: st.Size(), CreatedAt: st.ModTime()}, nil
}

// List returns local backups, newest first.
func List(dataDir string) ([]Info, error) {
	dir, err := Dir(dataDir)
	if err != nil {
		return nil, err
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []Info{}
	for _, n := range names {
		if !n.Type().IsRegular() || !ValidName(n.Name()) {
			continue
		}
		st, err := n.Info()
		if err != nil {
			continue
		}
		out = append(out, Info{Name: n.Name(), Size: st.Size(), CreatedAt: st.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Path resolves a validated backup name to its file.
func Path(dataDir, name string) (string, error) {
	if !ValidName(name) {
		return "", errors.New("invalid backup name")
	}
	dir, err := Dir(dataDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// Delete removes one local backup.
func Delete(dataDir, name string) error {
	p, err := Path(dataDir, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Prune keeps the newest keep backups and deletes the rest. keep <= 0 keeps everything.
func Prune(dataDir string, keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	list, err := List(dataDir)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := keep; i < len(list); i++ {
		if err := Delete(dataDir, list[i].Name); err == nil {
			n++
		}
	}
	return n, nil
}

func allowedPath(name string) bool {
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	for _, p := range allowedPrefixes {
		if name == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(name, p) && !strings.Contains(strings.TrimPrefix(name, p), "/")) {
			return true
		}
	}
	return false
}

// checkDatabase opens the staged SQLite file read-only and requires the users table:
// a random file with a valid checksum must not replace the live database.
func checkDatabase(path string) error {
	db, err := gorm.Open(sqlite.Open(path+"?mode=ro&_pragma=query_only(1)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var n int64
	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&n).Error; err != nil {
		return err
	}
	if n != 1 {
		return errors.New("no users table; not a yzapi database")
	}
	var ok string
	if err := db.Raw("PRAGMA integrity_check").Scan(&ok).Error; err != nil {
		return err
	}
	if ok != "ok" {
		return fmt.Errorf("integrity check: %s", ok)
	}
	return nil
}

var _ = sql.ErrNoRows

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeTarBytes(tw *tar.Writer, name string, b []byte, mod time.Time) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: mod, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func writeTarFile(tw *tar.Writer, name, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
