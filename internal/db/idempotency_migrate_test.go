package db

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
	"yzapi/internal/routing"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
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

// R135-01: the migration returns with the unique indexes in force (created inside its
// transaction), so no writer can recreate a duplicate before AutoMigrate.
func TestR135MigrationIncludesUniqueIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE accounts (id integer primary key autoincrement, name text, provider text, type text, base_url text, api_key_enc text, protocols text, enabled numeric)`,
		`INSERT INTO accounts (name,provider,type,base_url,api_key_enc,protocols,enabled) VALUES ('only','openai','text','http://a','k','[]',1)`,
		`CREATE TABLE sensitive_words (id integer primary key autoincrement, policy_group_id integer, word text, note text, enabled numeric)`,
		`CREATE TABLE audit_samples (id integer primary key autoincrement, policy_group_id integer, text text, note text, enabled numeric, vector blob, vector_dim integer, vector_model text)`,
		`CREATE TABLE route_samples (id integer primary key autoincrement, label text, text text, threshold real, note text, vector blob, vector_dim integer, vector_model text)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
	m := db.Migrator()
	for mdl, idx := range map[any]string{&model.Account{}: "uq_accounts_name", &model.SensitiveWord{}: "uq_sensitive_words_key", &model.AuditSample{}: "uq_audit_samples_key", &model.RouteSample{}: "uq_route_samples_key"} {
		if !m.HasIndex(mdl, idx) {
			t.Fatalf("%s must exist when the migration returns", idx)
		}
	}
	if err := db.Exec(`INSERT INTO accounts (name,provider,type,base_url,api_key_enc,protocols,enabled) VALUES ('only','openai','text','http://b','k','[]',1)`).Error; err == nil {
		t.Fatal("a duplicate must be refused right after the migration")
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
}

// R135-02: when both duplicates carry vectors, the one the configured runtime can use
// survives: the identity from the settings table when it is configured, otherwise the
// newest vector; the dropped identity is logged, never the only usable copy deleted.
func TestR135MigrationKeepsUsableVectorIdentity(t *testing.T) {
	newDB := func(t *testing.T, withSettings string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatal(err)
		}
		stmts := []string{
			`CREATE TABLE route_samples (id integer primary key autoincrement, label text, text text, threshold real, note text, vector blob, vector_dim integer, vector_model text)`,
			`INSERT INTO route_samples (id,label,text,threshold,note,vector,vector_dim,vector_model) VALUES (1,'simple','same',0,'old',x'0100','2','old-identity'),(2,'simple','same',0,'current',x'0001',2,'7:embed-v2')`,
			`CREATE TABLE audit_samples (id integer primary key autoincrement, policy_group_id integer, text text, note text, enabled numeric, vector blob, vector_dim integer, vector_model text)`,
			`INSERT INTO audit_samples (id,policy_group_id,text,note,enabled,vector,vector_dim,vector_model) VALUES (1,1,'same','current',1,x'0001',2,'7:embed-v2'),(2,1,'same','old',1,x'0100',2,'old-identity')`,
		}
		if withSettings != "" {
			stmts = append(stmts, `CREATE TABLE settings (key text primary key, value text, updated_at datetime)`, `INSERT INTO settings (key, value) VALUES ('vector', '`+withSettings+`')`)
		}
		for _, stmt := range stmts {
			if err := db.Exec(stmt).Error; err != nil {
				t.Fatal(err)
			}
		}
		return db
	}
	t.Run("configured identity wins regardless of age", func(t *testing.T) {
		db := newDB(t, `{"account_id":7,"model":"embed-v2"}`)
		if err := migrateIdempotencyKeys(db); err != nil {
			t.Fatal(err)
		}
		var r model.RouteSample
		db.First(&r)
		var a model.AuditSample
		db.First(&a)
		if r.VectorModel != "7:embed-v2" || a.VectorModel != "7:embed-v2" || r.ID != 2 || a.ID != 1 {
			t.Fatalf("configured identity must survive: route=%+v audit=%+v", r, a)
		}
	})
	t.Run("old identity configured keeps the old vector", func(t *testing.T) {
		db := newDB(t, `{"account_id":3,"model":"x"}`)
		db.Exec(`UPDATE route_samples SET vector_model = '3:x' WHERE id = 1`)
		if err := migrateIdempotencyKeys(db); err != nil {
			t.Fatal(err)
		}
		var r model.RouteSample
		db.First(&r)
		if r.ID != 1 || r.VectorModel != "3:x" {
			t.Fatalf("the vector matching the configured identity must survive even when older: %+v", r)
		}
	})
	t.Run("unconfigured keeps the newest vector", func(t *testing.T) {
		db := newDB(t, "")
		if err := migrateIdempotencyKeys(db); err != nil {
			t.Fatal(err)
		}
		var r model.RouteSample
		db.First(&r)
		var a model.AuditSample
		db.First(&a)
		if r.ID != 2 || a.ID != 2 {
			t.Fatalf("without a configured identity the newest vector survives: route=%+v audit=%+v", r, a)
		}
	})
}

func r136LoadRoute(t *testing.T, db *gorm.DB, identity string) int {
	t.Helper()
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	st, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	eng := routing.New(db, st, nil)
	if err := eng.SetVectorIdentity(func() string { return identity }); err != nil {
		t.Fatal(err)
	}
	return len(eng.Samples())
}

// R136-01: the migration resolves the identity the runtime really uses (account base
// URL and mapped upstream model) and treats legacy empty identities as compatible.
func TestR136MigrationResolvesActualVectorIdentity(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE route_samples (id integer primary key autoincrement, label text, text text, threshold real, note text, vector blob, vector_dim integer, vector_model text)`,
		`CREATE TABLE settings (key text primary key, value text, updated_at datetime)`,
		`INSERT INTO settings (key,value) VALUES ('vector','{"account_id":7,"model":"embed"}')`,
		`CREATE TABLE accounts (id integer primary key autoincrement, name text, provider text, type text, base_url text, api_key_enc text, protocols text, enabled numeric)`,
		`INSERT INTO accounts (id,name,provider,type,base_url,api_key_enc,protocols,enabled) VALUES (7,'vec','custom','embedding','http://current','k','[]',1)`,
		`CREATE TABLE model_mappings (id integer primary key autoincrement, account_id integer, request_model text, upstream_model text)`,
		`INSERT INTO model_mappings (account_id,request_model,upstream_model) VALUES (7,'embed','provider-embed-v2')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(stmt, err)
		}
	}
	if err := db.Exec(`INSERT INTO route_samples (id,label,text,threshold,note,vector,vector_dim,vector_model) VALUES (1,'simple','same',0,'current',?,2,'7|http://current|provider-embed-v2'),(2,'simple','same',0,'stale',?,2,'7|http://old|embed'),(3,'simple','legacy',0,'legacy',?,2,''),(4,'simple','legacy',0,'wrong',?,2,'8|http://old|other')`,
		vector.Encode([]float32{1, 0}), vector.Encode([]float32{0, 1}), vector.Encode([]float32{1, 0}), vector.Encode([]float32{0, 1})).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
	if got := r136LoadRoute(t, db, "7|http://current|provider-embed-v2"); got != 2 {
		t.Fatalf("the resolved-identity vector and the legacy vector must survive: loaded=%d", got)
	}
	var ids []uint
	db.Model(&model.RouteSample{}).Order("id").Pluck("id", &ids)
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("survivors: %v", ids)
	}
	// A malformed vector setting aborts the destructive migration.
	db2, _ := gorm.Open(sqlite.Open("file:"+t.Name()+"2?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	for _, stmt := range []string{
		`CREATE TABLE route_samples (id integer primary key autoincrement, label text, text text, threshold real, note text, vector blob, vector_dim integer, vector_model text)`,
		`INSERT INTO route_samples (label,text,threshold,note,vector,vector_dim,vector_model) VALUES ('simple','same',0,'',x'0100',2,'a'),('simple','same',0,'',x'0001',2,'b')`,
		`CREATE TABLE settings (key text primary key, value text, updated_at datetime)`,
		`INSERT INTO settings (key,value) VALUES ('vector','not json')`,
	} {
		if err := db2.Exec(stmt).Error; err != nil {
			t.Fatal(stmt, err)
		}
	}
	if err := migrateIdempotencyKeys(db2); err == nil {
		t.Fatal("a malformed vector setting must abort the migration instead of guessing")
	}
	var n int64
	db2.Raw("SELECT COUNT(*) FROM route_samples").Scan(&n)
	if n != 2 {
		t.Fatalf("aborted migration must not delete: %d", n)
	}
}

// R136-02: a same-named index that is not the correct unique index is replaced, for
// every key table.
func TestR136MigrationVerifiesIndexIsUnique(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE accounts (id integer primary key autoincrement, name text, provider text, type text, base_url text, api_key_enc text, protocols text, enabled numeric)`,
		`CREATE INDEX uq_accounts_name ON accounts (name)`,
		`INSERT INTO accounts (name,provider,type,base_url,api_key_enc,protocols,enabled) VALUES ('dup','openai','text','http://a','k','[]',1),('dup','openai','text','http://b','k','[]',1)`,
		`CREATE TABLE sensitive_words (id integer primary key autoincrement, policy_group_id integer, word text, note text, enabled numeric)`,
		`CREATE UNIQUE INDEX uq_sensitive_words_key ON sensitive_words (word)`, // unique but the wrong columns
		`INSERT INTO sensitive_words (policy_group_id, word, note, enabled) VALUES (1,'w','',1)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(stmt, err)
		}
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO accounts (name,provider,type,base_url,api_key_enc,protocols,enabled) VALUES ('dup','openai','text','http://c','k','[]',1)`).Error; err == nil {
		t.Fatal("the non-unique same-name index must have been replaced by the unique key")
	}
	if err := db.Exec(`INSERT INTO sensitive_words (policy_group_id, word, note, enabled) VALUES (2,'w','',1)`).Error; err != nil {
		t.Fatalf("the wrong-column unique index must have been replaced: same word in another group is allowed: %v", err)
	}
	if err := db.Exec(`INSERT INTO sensitive_words (policy_group_id, word, note, enabled) VALUES (1,'w','',1)`).Error; err == nil {
		t.Fatal("duplicate (group, word) must be refused")
	}
	if err := db.AutoMigrate(&model.Account{}, &model.SensitiveWord{}); err != nil {
		t.Fatal(err)
	}
}
