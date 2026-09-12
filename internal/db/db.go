// Package db opens the database and runs migrations.
package db

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
// keys were enforced (1.0.34): account names become unique (duplicates are renamed
// "<name> #<id>", never deleted, so no credential is lost), duplicate sensitive words
// are removed keeping the oldest, and samples get their text hash column filled and
// de-duplicated. AutoMigrate then adds the unique indexes without failing on old rows.
func migrateIdempotencyKeys(db *gorm.DB) error {
	m := db.Migrator()
	if m.HasTable(&model.Account{}) && !m.HasIndex(&model.Account{}, "uq_accounts_name") {
		var dups []struct {
			ID   uint
			Name string
		}
		if err := db.Raw("SELECT id, name FROM accounts WHERE id NOT IN (SELECT MIN(id) FROM accounts GROUP BY name)").Scan(&dups).Error; err != nil {
			return err
		}
		for _, d := range dups {
			name := fmt.Sprintf("%s #%d", d.Name, d.ID)
			if len(name) > 64 {
				name = name[len(name)-64:]
			}
			if err := db.Exec("UPDATE accounts SET name = ? WHERE id = ?", name, d.ID).Error; err != nil {
				return err
			}
			slog.Warn("renamed a duplicate account name so names can be unique", "id", d.ID, "old", d.Name, "new", name)
		}
	}
	if m.HasTable(&model.SensitiveWord{}) && !m.HasIndex(&model.SensitiveWord{}, "uq_sensitive_words_key") {
		res := db.Exec("DELETE FROM sensitive_words WHERE id NOT IN (SELECT MIN(id) FROM sensitive_words GROUP BY policy_group_id, word)")
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			slog.Warn("removed duplicate sensitive words before adding the unique key", "rows", res.RowsAffected)
		}
	}
	for _, t := range []struct {
		table, model, index, group string
		has                        bool
	}{
		{"audit_samples", "audit_samples", "uq_audit_samples_key", "policy_group_id", m.HasTable(&model.AuditSample{})},
		{"route_samples", "route_samples", "uq_route_samples_key", "label", m.HasTable(&model.RouteSample{})},
	} {
		if !t.has || m.HasIndex(t.model, t.index) {
			continue
		}
		if !m.HasColumn(t.model, "text_hash") {
			if err := db.Exec("ALTER TABLE " + t.table + " ADD COLUMN text_hash varchar(64) NOT NULL DEFAULT ''").Error; err != nil {
				return err
			}
		}
		var rows []struct {
			ID   uint
			Text string
		}
		if err := db.Raw("SELECT id, text FROM " + t.table + " WHERE text_hash = ''").Scan(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			if err := db.Exec("UPDATE "+t.table+" SET text_hash = ? WHERE id = ?", model.TextKey(r.Text), r.ID).Error; err != nil {
				return err
			}
		}
		res := db.Exec("DELETE FROM " + t.table + " WHERE id NOT IN (SELECT MIN(id) FROM " + t.table + " GROUP BY " + t.group + ", text_hash)")
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			slog.Warn("removed duplicate samples before adding the unique key", "table", t.table, "rows", res.RowsAffected)
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
