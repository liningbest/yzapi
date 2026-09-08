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
