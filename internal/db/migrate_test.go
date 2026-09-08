package db

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
)

// oldCallLog mirrors the pre-upgrade schema: request_id had a plain index named
// idx_call_logs_request_id, which AutoMigrate alone will not turn into a unique one.
type oldCallLog struct {
	ID        uint   `gorm:"primaryKey"`
	RequestID string `gorm:"size:40;index"`
	Result    string `gorm:"size:16"`
}

func (oldCallLog) TableName() string { return "call_logs" }

func TestMigrateCallLogRequestIDUpgradesOldSchema(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&oldCallLog{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&[]oldCallLog{{RequestID: "dup", Result: "old"}, {RequestID: "dup", Result: "new"}, {RequestID: "solo"}})

	if err := migrateCallLogRequestID(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CallLog{}); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasIndex(&model.CallLog{}, "uq_call_logs_request_id") {
		t.Fatal("unique index missing after upgrade")
	}
	var rows []model.CallLog
	db.Order("id").Find(&rows)
	if len(rows) != 2 || rows[0].RequestID != "dup" || rows[0].Result != "new" {
		t.Fatalf("expected duplicates collapsed to the newest row, got %+v", rows)
	}
	// The upsert used by the journal writer must now be accepted.
	err = db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).
		Create(&model.CallLog{RequestID: "dup"}).Error
	if err != nil {
		t.Fatalf("ON CONFLICT insert failed after migration: %v", err)
	}
}

// A database upgraded once with a nullable attempts column (NULL on old rows) must
// survive the switch to NOT NULL: SQLite rebuilds the table and would reject NULLs.
func TestMigrateUsageHourlyAttemptsFillsNulls(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UsageHourly{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE usage_hourlies DROP COLUMN attempts").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE usage_hourlies ADD COLUMN attempts bigint").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO usage_hourlies (hour, user_id, group_id, api_key_id, account_id, provider, request_model, model_group, api_type, requests, success, failed, prompt_tokens, completion_tokens, total_tokens, cached_tokens, unknown_usage, latency_ms) VALUES ('2026-09-01 10:00:00', 1, 1, 0, 2, 'openai', 'm', '', 'text', 1, 1, 0, 5, 0, 5, 0, 0, 0)")
	if err := db.AutoMigrate(&model.UsageHourly{}); err == nil {
		t.Log("driver accepted NULL on NOT NULL rebuild; migration still must zero it")
	}
	if err := migrateUsageHourlyAttempts(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UsageHourly{}); err != nil {
		t.Fatalf("AutoMigrate after fill: %v", err)
	}
	var row model.UsageHourly
	if err := db.First(&row).Error; err != nil || row.Attempts != 0 || row.TotalTokens != 5 {
		t.Fatalf("row after migration: %+v %v", row, err)
	}
	var nulls int64
	db.Raw("SELECT COUNT(*) FROM usage_hourlies WHERE attempts IS NULL").Scan(&nulls)
	if nulls != 0 {
		t.Fatalf("%d NULL attempts remain", nulls)
	}
}
