package db

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
)

// A database from before the create idempotency keys: duplicate account names are
// renamed (never deleted), duplicate words and samples are collapsed to the oldest,
// sample hashes are filled, and AutoMigrate then adds the unique indexes.
func TestMigrateIdempotencyKeysUpgradesOldRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE accounts (id integer primary key autoincrement, name text, provider text, type text, base_url text, api_key_enc text, protocols text, enabled numeric)`,
		`INSERT INTO accounts (name, provider, type, base_url, api_key_enc, protocols, enabled) VALUES ('dup','openai','text','http://a','k1','[]',1), ('dup','openai','text','http://b','k2','[]',1), ('solo','openai','text','http://c','k3','[]',1)`,
		`CREATE TABLE policy_groups (id integer primary key autoincrement, name text, action text, risk_level text, enabled numeric)`,
		`INSERT INTO policy_groups (name, action, risk_level, enabled) VALUES ('pg','audit','low',1)`,
		`CREATE TABLE sensitive_words (id integer primary key autoincrement, policy_group_id integer, word text, note text, enabled numeric)`,
		`INSERT INTO sensitive_words (policy_group_id, word, note, enabled) VALUES (1,'w','first',1), (1,'w','second',1), (1,'v','',1)`,
		`CREATE TABLE audit_samples (id integer primary key autoincrement, policy_group_id integer, text text, note text, enabled numeric, vector blob, vector_dim integer, vector_model text)`,
		`INSERT INTO audit_samples (policy_group_id, text, note, enabled) VALUES (1,'same','a',1), (1,'same','b',1), (1,'other','',1)`,
		`CREATE TABLE route_samples (id integer primary key autoincrement, label text, text text, threshold real, note text, vector blob, vector_dim integer, vector_model text)`,
		`INSERT INTO route_samples (label, text, threshold, note) VALUES ('simple','same',0,''), ('simple','same',0,''), ('complex','same',0,'')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(stmt, err)
		}
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Account{}, &model.PolicyGroup{}, &model.SensitiveWord{}, &model.AuditSample{}, &model.RouteSample{}); err != nil {
		t.Fatalf("AutoMigrate after the upgrade must succeed: %v", err)
	}
	var accounts []model.Account
	db.Order("id").Find(&accounts)
	if len(accounts) != 3 || accounts[0].Name != "dup" || accounts[1].Name != "dup #2" || accounts[2].Name != "solo" {
		t.Fatalf("duplicate account names must be renamed, not deleted: %+v", accounts)
	}
	var words []model.SensitiveWord
	db.Order("id").Find(&words)
	if len(words) != 2 || words[0].Note != "first" {
		t.Fatalf("duplicate words collapse to the oldest: %+v", words)
	}
	var samples []model.AuditSample
	db.Order("id").Find(&samples)
	if len(samples) != 2 || samples[0].Note != "a" || samples[0].TextHash != model.TextKey("same") || samples[1].TextHash != model.TextKey("other") {
		t.Fatalf("audit samples: %+v", samples)
	}
	var routes []model.RouteSample
	db.Order("id").Find(&routes)
	if len(routes) != 2 || routes[1].Label != "complex" {
		t.Fatalf("route samples keep one per (label, text): %+v", routes)
	}
	if err := db.Create(&model.RouteSample{Label: "simple", Text: "same"}).Error; err == nil {
		t.Fatal("unique index must be in place after the upgrade")
	}
	// Running the upgrade again is a no-op.
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
}

// R134-01: generated account names are checked against every name in use and stay
// valid UTF-8 within the column; a re-run is a no-op.
func TestR134DuplicateAccountRenameCannotCollide(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("账", 30) // 90 bytes: the suffix must fit and the cut must land on a character boundary
	for _, stmt := range []string{
		`CREATE TABLE accounts (id integer primary key autoincrement, name text, provider text, type text, base_url text, api_key_enc text, protocols text, enabled numeric)`,
		`INSERT INTO accounts (id,name,provider,type,base_url,api_key_enc,protocols,enabled) VALUES (1,'dup','openai','text','http://a','k1','[]',1),(2,'dup','openai','text','http://b','k2','[]',1),(3,'dup #2','openai','text','http://c','k3','[]',1),(4,'` + long + `','openai','text','http://d','k4','[]',1),(5,'` + long + `','openai','text','http://e','k5','[]',1)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
	var names []string
	db.Raw("SELECT name FROM accounts ORDER BY id").Scan(&names)
	seen := map[string]bool{}
	for i, n := range names {
		renamed := i == 1 || i == 4 // the oldest copy keeps its original name untouched, even an over-long one
		if seen[n] || !utf8.ValidString(n) || (renamed && len(n) > 64) {
			t.Fatalf("names must be distinct and valid UTF-8, renamed ones within 64 bytes: %q", names)
		}
		seen[n] = true
	}
	if names[1] != "dup #2-1" || names[2] != "dup #2" || !strings.HasSuffix(names[4], " #5") {
		t.Fatalf("renames: %q", names)
	}
	if err := db.AutoMigrate(&model.Account{}); err != nil {
		t.Fatalf("unique index must install after the rename: %v", err)
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
}

// R134-02: collapsing duplicates keeps the live state: the enabled word survives, the
// vectorised / enabled sample survives, and what the dropped copies had (vector, note,
// threshold) is merged rather than lost.
func TestR134MigrationPreservesEffectiveDuplicateRules(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE sensitive_words (id integer primary key autoincrement, policy_group_id integer, word text, note text, enabled numeric)`,
		`INSERT INTO sensitive_words (policy_group_id,word,note,enabled) VALUES (1,'blocked','old disabled',0),(1,'blocked','',1)`,
		`CREATE TABLE audit_samples (id integer primary key autoincrement, policy_group_id integer, text text, note text, enabled numeric, vector blob, vector_dim integer, vector_model text)`,
		`INSERT INTO audit_samples (policy_group_id,text,note,enabled,vector,vector_dim,vector_model) VALUES (1,'danger','old disabled',0,NULL,0,''),(1,'danger','new active vector',1,x'010203',3,'embed-v2'),(1,'vec-only','has vector',0,x'0405',2,'embed-v1'),(1,'vec-only','enabled no vector',1,NULL,0,'')`,
		`CREATE TABLE route_samples (id integer primary key autoincrement, label text, text text, threshold real, note text, vector blob, vector_dim integer, vector_model text)`,
		`INSERT INTO route_samples (label,text,threshold,note,vector,vector_dim,vector_model) VALUES ('complex','hard task',0.2,'old no vector',NULL,0,''),('complex','hard task',0,'',x'010203',3,'embed-v2')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
	var word model.SensitiveWord
	db.First(&word)
	if !word.Enabled || word.Note != "old disabled" {
		t.Fatalf("enabled copy survives and the note is merged: %+v", word)
	}
	var audits []model.AuditSample
	db.Order("id").Find(&audits)
	if len(audits) != 2 || !audits[0].Enabled || audits[0].VectorDim != 3 || audits[0].Note != "new active vector" {
		t.Fatalf("danger: %+v", audits)
	}
	if !audits[1].Enabled || audits[1].VectorDim != 2 || audits[1].VectorModel != "embed-v1" {
		t.Fatalf("vec-only: the vectorised copy survives and takes enabled from the other: %+v", audits[1])
	}
	var route model.RouteSample
	db.First(&route)
	if route.VectorDim != 3 || route.Threshold != 0.2 || route.Note != "old no vector" {
		t.Fatalf("route: vectorised copy survives with the other's threshold and note merged: %+v", route)
	}
	if err := db.AutoMigrate(&model.SensitiveWord{}, &model.AuditSample{}, &model.RouteSample{}); err != nil {
		t.Fatal(err)
	}
}
