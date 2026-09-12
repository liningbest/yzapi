// Package db opens the database and runs migrations.
package db

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/config"
	"yzapi/internal/model"
	"yzapi/internal/pricing"
)

func Open(cfg *config.Config) (*gorm.DB, error) {
	gcfg := &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		PrepareStmt:                              true,
		TranslateError:                           true, // driver error codes -> gorm.ErrDuplicatedKey (SQLite 2067, PostgreSQL 23505)
	}
	if cfg.Dev {
		gcfg.Logger = logger.Default.LogMode(logger.Warn)
	}

	var (
		db  *gorm.DB
		err error
	)
	switch cfg.DBDriver {
	case "postgres", "postgresql", "pg":
		if cfg.DBDSN == "" {
			return nil, fmt.Errorf("YZAPI_DB_DSN is required for postgres")
		}
		db, err = gorm.Open(postgres.Open(cfg.DBDSN), gcfg)
		if err != nil {
			return nil, err
		}
		sqlDB, _ := db.DB()
		sqlDB.SetMaxOpenConns(50)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxLifetime(time.Hour)
	default:
		dsn := cfg.DBDSN
		if dsn == "" {
			dir := filepath.Join(cfg.DataDir, "data", "db")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, err
			}
			dsn = filepath.Join(dir, "yzapi.db")
		}
		// WAL + busy timeout gives good concurrent read performance on a single node.
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(OFF)&_pragma=cache_size(-65536)"
		db, err = gorm.Open(sqlite.Open(dsn), gcfg)
		if err != nil {
			return nil, err
		}
		sqlDB, _ := db.DB()
		// SQLite allows one writer; keep the pool small to avoid lock churn.
		sqlDB.SetMaxOpenConns(8)
		sqlDB.SetMaxIdleConns(8)
	}

	if err := migrateCallLogRequestID(db); err != nil {
		return nil, fmt.Errorf("migrate call_logs.request_id: %w", err)
	}
	if err := migrateUsageHourlyAttempts(db); err != nil {
		return nil, fmt.Errorf("migrate usage_hourlies.attempts: %w", err)
	}
	if err := migrateIdempotencyKeys(db); err != nil {
		return nil, fmt.Errorf("migrate create idempotency keys: %w", err)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := migrateModelPriceKey(db); err != nil {
		return nil, fmt.Errorf("migrate model_prices key: %w", err)
	}
	slog.Info("database ready", "driver", cfg.DBDriver)
	return db, nil
}

// migrateIdempotencyKeys prepares databases created before the create idempotency
// keys were enforced (1.0.34+). It runs once, in one transaction, under a database
// level lock on PostgreSQL (several instances may start at once) with the checks
// repeated inside the lock:
//   - duplicate account names are renamed "<name> #<id>" (never deleted, so no
//     credential is lost); every candidate is checked against every name in use and
//     stays valid UTF-8 within the column;
//   - duplicate sensitive words collapse to one row that is enabled if any copy was;
//   - samples get their text hash filled, then duplicates collapse to the copy that
//     carries the live state (enabled, vectorised), merging what the others had
//     (vector, non-empty note, threshold) so no effective rule or vector is lost.
//
// Every dropped row is logged with its content. The unique index of each table is
// created inside the same transaction, right after its cleanup, so the function
// returns with the keys in force: no writer can recreate a duplicate between the
// cleanup and the index (AutoMigrate afterwards finds the indexes present).
func migrateIdempotencyKeys(db *gorm.DB) error {
	m := db.Migrator()
	need := (m.HasTable(&model.Account{}) && !m.HasIndex(&model.Account{}, "uq_accounts_name")) ||
		(m.HasTable(&model.SensitiveWord{}) && !m.HasIndex(&model.SensitiveWord{}, "uq_sensitive_words_key")) ||
		(m.HasTable(&model.AuditSample{}) && !m.HasIndex(&model.AuditSample{}, "uq_audit_samples_key")) ||
		(m.HasTable(&model.RouteSample{}) && !m.HasIndex(&model.RouteSample{}, "uq_route_samples_key"))
	if !need {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(7461227)").Error; err != nil { // arbitrary constant: idempotency key migration
				return err
			}
		}
		tm := tx.Migrator()
		identity := currentVectorIdentity(tx)
		if tm.HasTable(&model.Account{}) && !tm.HasIndex(&model.Account{}, "uq_accounts_name") {
			if err := migrateAccountNames(tx); err != nil {
				return err
			}
			if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uq_accounts_name ON accounts (name)").Error; err != nil {
				return err
			}
		}
		if tm.HasTable(&model.SensitiveWord{}) && !tm.HasIndex(&model.SensitiveWord{}, "uq_sensitive_words_key") {
			if err := migrateDuplicateWords(tx); err != nil {
				return err
			}
			if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uq_sensitive_words_key ON sensitive_words (policy_group_id, word)").Error; err != nil {
				return err
			}
		}
		if tm.HasTable(&model.AuditSample{}) && !tm.HasIndex(&model.AuditSample{}, "uq_audit_samples_key") {
			if err := migrateDuplicateSamples(tx, "audit_samples", "policy_group_id", identity); err != nil {
				return err
			}
			if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_samples_key ON audit_samples (policy_group_id, text_hash)").Error; err != nil {
				return err
			}
		}
		if tm.HasTable(&model.RouteSample{}) && !tm.HasIndex(&model.RouteSample{}, "uq_route_samples_key") {
			if err := migrateDuplicateSamples(tx, "route_samples", "label", identity); err != nil {
				return err
			}
			if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uq_route_samples_key ON route_samples (label, text_hash)").Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// currentVectorIdentity reads the configured embedding account and model from the
// settings table (the engines' vector identity is "<account id>:<model>", or
// "<account id>|<base url>|<model>" once the account resolves) so the migration can
// tell which of several stored vectors the runtime will actually use. Empty when the
// vector service is not configured or the table does not exist yet.
func currentVectorIdentity(tx *gorm.DB) string {
	if !tx.Migrator().HasTable("settings") {
		return ""
	}
	var raw string
	if err := tx.Raw("SELECT value FROM settings WHERE key = 'vector'").Scan(&raw).Error; err != nil || raw == "" {
		return ""
	}
	var v struct {
		AccountID uint   `json:"account_id"`
		Model     string `json:"model"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil || v.AccountID == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%s", v.AccountID, v.Model)
}

// vectorUsable reports whether a stored vector identity is the configured one:
// the plain "<id>:<model>" form or the resolved "<id>|<url>|<model>" form of it.
func vectorUsable(stored, identity string) bool {
	if stored == "" || identity == "" {
		return false
	}
	if stored == identity {
		return true
	}
	id, mdl, _ := strings.Cut(identity, ":")
	return strings.HasPrefix(stored, id+"|") && strings.HasSuffix(stored, "|"+mdl)
}

// migrateAccountNames renames the later copies of a duplicated account name to a
// name nobody uses.
func migrateAccountNames(tx *gorm.DB) error {
	var all []struct {
		ID   uint
		Name string
	}
	if err := tx.Raw("SELECT id, name FROM accounts ORDER BY id").Scan(&all).Error; err != nil {
		return err
	}
	used := map[string]bool{}
	firstOf := map[string]uint{}
	for _, a := range all {
		used[a.Name] = true
		if _, ok := firstOf[a.Name]; !ok {
			firstOf[a.Name] = a.ID
		}
	}
	for _, a := range all {
		if firstOf[a.Name] == a.ID {
			continue // the oldest keeps the name
		}
		candidate := ""
		for n := 0; ; n++ {
			suffix := fmt.Sprintf(" #%d", a.ID)
			if n > 0 {
				suffix = fmt.Sprintf(" #%d-%d", a.ID, n)
			}
			candidate = pricing.TruncateUTF8(a.Name, 64-len(suffix)) + suffix
			if !used[candidate] {
				break
			}
		}
		used[candidate] = true
		if err := tx.Exec("UPDATE accounts SET name = ? WHERE id = ?", candidate, a.ID).Error; err != nil {
			return err
		}
		slog.Warn("renamed a duplicate account name so names can be unique", "id", a.ID, "old", a.Name, "new", candidate)
	}
	return nil
}

// migrateDuplicateWords collapses (policy_group_id, word) duplicates: the survivor is
// an enabled copy when there is one (else the oldest), and it is enabled if any copy
// was; the first non-empty note is kept.
func migrateDuplicateWords(tx *gorm.DB) error {
	var rows []struct {
		ID            uint
		PolicyGroupID uint
		Word          string
		Note          string
		Enabled       bool
	}
	if err := tx.Raw("SELECT id, policy_group_id, word, note, enabled FROM sensitive_words ORDER BY id").Scan(&rows).Error; err != nil {
		return err
	}
	groups := map[string][]int{}
	var order []string
	for i, r := range rows {
		k := fmt.Sprintf("%d|%s", r.PolicyGroupID, r.Word)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	for _, k := range order {
		idx := groups[k]
		if len(idx) < 2 {
			continue
		}
		win := idx[0]
		for _, i := range idx {
			if rows[i].Enabled && !rows[win].Enabled {
				win = i
			}
		}
		enabled, note := rows[win].Enabled, rows[win].Note
		for _, i := range idx {
			if i == win {
				continue
			}
			enabled = enabled || rows[i].Enabled
			if note == "" {
				note = rows[i].Note
			}
			if err := tx.Exec("DELETE FROM sensitive_words WHERE id = ?", rows[i].ID).Error; err != nil {
				return err
			}
			slog.Warn("removed a duplicate sensitive word (merged into the surviving row)", "id", rows[i].ID, "kept", rows[win].ID,
				"policy_group_id", rows[i].PolicyGroupID, "word", rows[i].Word, "enabled", rows[i].Enabled, "note", rows[i].Note)
		}
		if err := tx.Exec("UPDATE sensitive_words SET enabled = ?, note = ? WHERE id = ?", enabled, note, rows[win].ID).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateDuplicateSamples fills text_hash and collapses (group, text_hash) duplicates
// in audit_samples / route_samples. The survivor is the copy with the most live state:
// a vector built with the configured identity first, then any vector (the newest,
// since it was built most recently), then enabled, then the oldest. The merged row is
// enabled if any copy was, takes a vector from a dropped copy when it has none, and
// keeps the first non-empty note and non-zero threshold. A dropped vector of another
// identity is logged with that identity so it can be rebuilt.
func migrateDuplicateSamples(tx *gorm.DB, table, group, identity string) error {
	if !tx.Migrator().HasColumn(table, "text_hash") {
		if err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN text_hash varchar(64) NOT NULL DEFAULT ''").Error; err != nil {
			return err
		}
	}
	hasEnabled := tx.Migrator().HasColumn(table, "enabled")
	hasThreshold := tx.Migrator().HasColumn(table, "threshold")
	var unhashed []struct {
		ID   uint
		Text string
	}
	if err := tx.Raw("SELECT id, text FROM " + table + " WHERE text_hash = ''").Scan(&unhashed).Error; err != nil {
		return err
	}
	for _, r := range unhashed {
		if err := tx.Exec("UPDATE "+table+" SET text_hash = ? WHERE id = ?", model.TextKey(r.Text), r.ID).Error; err != nil {
			return err
		}
	}
	type row struct {
		ID          uint
		Group       string
		TextHash    string
		Note        string
		Enabled     bool
		Threshold   float64
		Vector      []byte
		VectorDim   int
		VectorModel string
	}
	cols := "id, " + group + " AS \"group\", text_hash, note, vector, vector_dim, vector_model"
	if hasEnabled {
		cols += ", enabled"
	}
	if hasThreshold {
		cols += ", threshold"
	}
	var rows []row
	if err := tx.Raw("SELECT " + cols + " FROM " + table + " ORDER BY id").Scan(&rows).Error; err != nil {
		return err
	}
	groups := map[string][]int{}
	var order []string
	for i, r := range rows {
		k := r.Group + "|" + r.TextHash
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	hasVector := func(r row) bool { return r.VectorDim > 0 && len(r.Vector) > 0 }
	score := func(r row) int {
		s := 0
		if hasVector(r) {
			s += 2
			if vectorUsable(r.VectorModel, identity) {
				s += 4
			}
		}
		if !hasEnabled || r.Enabled {
			s++
		}
		return s
	}
	for _, k := range order {
		idx := groups[k]
		if len(idx) < 2 {
			continue
		}
		win := idx[0]
		for _, i := range idx {
			// Strictly better wins; on a tie between two vectorised copies the newer
			// vector wins (it was built most recently, closest to the current config).
			if score(rows[i]) > score(rows[win]) || (score(rows[i]) == score(rows[win]) && hasVector(rows[i]) && rows[i].ID > rows[win].ID) {
				win = i
			}
		}
		merged := rows[win]
		for _, i := range idx {
			if i == win {
				continue
			}
			d := rows[i]
			merged.Enabled = merged.Enabled || d.Enabled
			if merged.Note == "" {
				merged.Note = d.Note
			}
			if merged.Threshold == 0 {
				merged.Threshold = d.Threshold
			}
			if (merged.VectorDim == 0 || len(merged.Vector) == 0) && d.VectorDim > 0 && len(d.Vector) > 0 {
				merged.Vector, merged.VectorDim, merged.VectorModel = d.Vector, d.VectorDim, d.VectorModel
			}
			if err := tx.Exec("DELETE FROM "+table+" WHERE id = ?", d.ID).Error; err != nil {
				return err
			}
			slog.Warn("removed a duplicate sample (merged into the surviving row)", "table", table, "id", d.ID, "kept", merged.ID,
				group, d.Group, "enabled", d.Enabled, "vector_dim", d.VectorDim, "vector_model", d.VectorModel, "threshold", d.Threshold, "note", d.Note)
		}
		set := "note = ?, vector = ?, vector_dim = ?, vector_model = ?"
		args := []any{merged.Note, merged.Vector, merged.VectorDim, merged.VectorModel}
		if hasEnabled {
			set += ", enabled = ?"
			args = append(args, merged.Enabled)
		}
		if hasThreshold {
			set += ", threshold = ?"
			args = append(args, merged.Threshold)
		}
		args = append(args, merged.ID)
		if err := tx.Exec("UPDATE "+table+" SET "+set+" WHERE id = ?", args...).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateModelPriceKey makes (provider, pattern) unique on model_prices. Rows created
// before the index are lower-cased and de-duplicated first (edited over untouched,
// manual over built-in, then the older row), then the unique index is created. Lookups
// always lower-cased both sides, so nothing changes for pricing.
func migrateModelPriceKey(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&model.ModelPrice{}) || m.HasIndex(&model.ModelPrice{}, "uq_model_prices_key") {
		return nil
	}
	// Several instances may start against one PostgreSQL at once: the whole
	// normalise → de-duplicate → index step runs under an advisory lock and re-checks
	// the index inside it, so only the first instance does the work and the others
	// find it done. SQLite has a single writer, so the transaction itself serialises.
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(7461226)").Error; err != nil { // arbitrary constant: model_prices key migration
				return err
			}
			if tx.Migrator().HasIndex(&model.ModelPrice{}, "uq_model_prices_key") {
				return nil
			}
		}
		removed, err := pricing.DedupePrices(tx)
		if err != nil {
			return err
		}
		if removed > 0 {
			slog.Warn("removed duplicate price rows before adding the unique (provider, pattern) index", "rows", removed)
		}
		return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS uq_model_prices_key ON model_prices (provider, pattern)").Error
	})
}

// migrateUsageHourlyAttempts prepares databases where usage_hourlies.attempts was added
// as a nullable column (first 4526658 upgrade) for the NOT NULL definition: AutoMigrate
// rebuilds the table on SQLite and would fail on NULL values, so they are zeroed first.
// A zero here means "not recorded", never "zero attempts" (see logstore attempts_since).
func migrateUsageHourlyAttempts(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&model.UsageHourly{}) || !m.HasColumn(&model.UsageHourly{}, "attempts") {
		return nil
	}
	res := db.Exec("UPDATE usage_hourlies SET attempts = 0 WHERE attempts IS NULL")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		slog.Info("filled NULL attempts on existing usage rollup rows before tightening the column", "rows", res.RowsAffected)
	}
	return nil
}

// migrateCallLogRequestID upgrades databases created before request_id became unique.
// AutoMigrate never replaces an existing non-unique index, so the old index is dropped
// explicitly after duplicate rows (which only replays could have produced) are removed,
// keeping the newest copy of each request.
func migrateCallLogRequestID(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&model.CallLog{}) {
		return nil
	}
	if m.HasIndex(&model.CallLog{}, "uq_call_logs_request_id") {
		return nil
	}
	res := db.Exec("DELETE FROM call_logs WHERE id NOT IN (SELECT MAX(id) FROM call_logs GROUP BY request_id)")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		slog.Warn("removed duplicate call log rows before adding the unique request_id index", "rows", res.RowsAffected)
	}
	if m.HasIndex(&model.CallLog{}, "idx_call_logs_request_id") {
		if err := m.DropIndex(&model.CallLog{}, "idx_call_logs_request_id"); err != nil {
			return err
		}
	}
	return nil
}
