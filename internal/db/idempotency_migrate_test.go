package db

import (
	"testing"

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
