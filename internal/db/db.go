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

	if err := db.AutoMigrate(model.All()...); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	slog.Info("database ready", "driver", cfg.DBDriver)
	return db, nil
}
