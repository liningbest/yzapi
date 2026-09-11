package pricing

import (
	"encoding/json"
	"testing"
	"time"

	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R113-01: the migration converts the per-attempt amounts too, so a rebuild of the
// hourly rollup (which sums attempts) agrees with the request amount.
func TestR113MigrationRollupConsistency(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	l := model.CallLog{RequestID: "old-cny", CreatedAt: time.Now(), AccountID: 1, Provider: "custom", CostMicros: 7200000, CostKnown: true, PromptTokens: 1, TotalTokens: 1, UsageStatus: model.UsageConfirmed,
		Attempts: model.JSON(`[{"account_id":1,"provider":"custom","usage_status":"confirmed","prompt_tokens":1,"cost_micros":7200000}]`)}
	if err := db.Create(&l).Error; err != nil {
		t.Fatal(err)
	}
	h := model.UsageHourly{Hour: time.Now().Truncate(time.Hour), AccountID: 1, CostMicros: 7200000, Requests: 1}
	db.Create(&h)
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	db.First(&l, l.ID)
	if l.CostMicros != 1000000 || l.CostLedger != Ledger {
		t.Fatalf("migrated log: %d %q", l.CostMicros, l.CostLedger)
	}
	var sum int64
	for _, r := range logstore.Aggregate([]*model.CallLog{&l}) {
		sum += r.CostMicros
	}
	if sum != l.CostMicros {
		t.Fatalf("migrated log=%d but rebuild aggregation=%d", l.CostMicros, sum)
	}
	db.First(&h, h.ID)
	if h.CostMicros != 1000000 {
		t.Fatalf("hourly: %d", h.CostMicros)
	}
	// Idempotent: a second run changes nothing.
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	var again model.CallLog
	db.First(&again, l.ID)
	if again.CostMicros != 1000000 || string(again.Attempts) != string(l.Attempts) {
		t.Fatalf("second run must be a no-op: %d %s", again.CostMicros, again.Attempts)
	}
}

// R113-02: data and marker move in one transaction; a failed run leaves the amounts
// untouched and the retry converts exactly once.
func TestR113MigrationRetryAtomic(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	l := model.CallLog{RequestID: "retry-cny", CostMicros: 7200000, Attempts: model.JSON(`[{"cost_micros":7200000}]`)}
	db.Create(&l)
	db.Create(&model.UsageHourly{Hour: time.Now().Truncate(time.Hour), CostMicros: 7200000, Requests: 1})
	if err := db.Exec(`CREATE TRIGGER fail_cost_marker BEFORE INSERT ON settings WHEN NEW.key = 'cost_ledger' BEGIN SELECT RAISE(ABORT, 'injected marker failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateLedger(db, st); err == nil {
		t.Fatal("expected injected error")
	}
	var mid model.CallLog
	db.First(&mid, l.ID)
	var hmid model.UsageHourly
	db.First(&hmid)
	if mid.CostMicros != 7200000 || hmid.CostMicros != 7200000 || mid.CostLedger != "" {
		t.Fatalf("failed migration must roll back everything: log=%d hourly=%d ledger=%q", mid.CostMicros, hmid.CostMicros, mid.CostLedger)
	}
	db.Exec("DROP TRIGGER fail_cost_marker")
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	db.First(&l, l.ID)
	var h model.UsageHourly
	db.First(&h)
	if l.CostMicros != 1000000 || h.CostMicros != 1000000 || string(l.Attempts) != `[{"cost_micros":1000000}]` {
		t.Fatalf("retry converts twice or misses: log=%d hourly=%d attempts=%s", l.CostMicros, h.CostMicros, l.Attempts)
	}
	var marker model.Setting
	db.Where("key = ?", ledgerMarker).First(&marker)
	if marker.Value != ledgerVersion {
		t.Fatalf("marker %q", marker.Value)
	}
}

// A database migrated by the first 1.0.13 build (marker "usd", attempts unconverted)
// gets its attempts converted without touching the already-converted amounts.
func TestLedgerV1Upgrade(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Setting{Key: ledgerMarker, Value: "USD", UpdatedAt: time.Now()}) // exactly what 1.0.13 wrote
	l := model.CallLog{RequestID: "v1", CostMicros: 1000000, Attempts: model.JSON(`[{"cost_micros":7200000}]`)}
	db.Create(&l)
	db.Create(&model.UsageHourly{Hour: time.Now().Truncate(time.Hour), CostMicros: 1000000, Requests: 1})
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	db.First(&l, l.ID)
	var h model.UsageHourly
	db.First(&h)
	if l.CostMicros != 1000000 || h.CostMicros != 1000000 || string(l.Attempts) != `[{"cost_micros":1000000}]` || l.CostLedger != Ledger {
		t.Fatalf("v1 upgrade: log=%d hourly=%d attempts=%s ledger=%q", l.CostMicros, h.CostMicros, l.Attempts, l.CostLedger)
	}
}

// With a USD display currency there is nothing to convert; rows are only stamped.
func TestLedgerUSDDisplayStampsOnly(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "USD", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	l := model.CallLog{RequestID: "usd", CostMicros: 1000000, Attempts: model.JSON(`[{"cost_micros":1000000}]`)}
	db.Create(&l)
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	db.First(&l, l.ID)
	if l.CostMicros != 1000000 || string(l.Attempts) != `[{"cost_micros":1000000}]` || l.CostLedger != Ledger {
		t.Fatalf("usd display: %d %s %q", l.CostMicros, l.Attempts, l.CostLedger)
	}
}

// Journal records from an older binary are converted once at commit; stamped records
// (written by this binary) pass through untouched.
func TestLegacyCostFixer(t *testing.T) {
	_, _, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	fix := LegacyCostFixer(st)
	old := &model.CallLog{CostMicros: 7200000, Attempts: model.JSON(`[{"account_id":1,"cost_micros":7200000},{"account_id":2}]`)}
	fix(old)
	var atts []map[string]any
	_ = json.Unmarshal([]byte(old.Attempts), &atts)
	if old.CostMicros != 1000000 || old.CostLedger != Ledger || atts[0]["cost_micros"] != float64(1000000) {
		t.Fatalf("legacy record: %d %q %s", old.CostMicros, old.CostLedger, old.Attempts)
	}
	fix(old) // already stamped
	if old.CostMicros != 1000000 {
		t.Fatalf("stamped record converted again: %d", old.CostMicros)
	}
	fresh := &model.CallLog{CostMicros: 500, CostLedger: Ledger}
	fix(fresh)
	if fresh.CostMicros != 500 {
		t.Fatal("fresh record must not change")
	}
}
