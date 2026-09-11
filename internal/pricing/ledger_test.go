package pricing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

func attemptCosts(t *testing.T, raw model.JSON) []int64 {
	t.Helper()
	var atts []struct {
		Cost int64 `json:"cost_micros"`
	}
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		t.Fatal(err)
	}
	out := make([]int64, 0, len(atts))
	for _, a := range atts {
		out = append(out, a.Cost)
	}
	return out
}

// R113-01: a 1.0.12 database (origin v0): request and attempt amounts are both in the
// display currency and both convert, so a rebuild of the hourly rollup (which sums
// attempts) agrees with the request amount.
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
	var origin model.Setting
	db.Where("key = ?", ledgerOriginKey).First(&origin)
	if origin.Value != "v0" {
		t.Fatalf("origin %q", origin.Value)
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
	l := model.CallLog{RequestID: "retry-cny", CreatedAt: time.Now(), CostMicros: 7200000, Attempts: model.JSON(`[{"cost_micros":7200000}]`)}
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

// R114-02: a database that ran 1.0.13 (marker "USD") holds three kinds of unstamped
// rows, each recognised from its own evidence.
func TestLedgerV1Upgrade(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	markerAt := time.Now().Add(-time.Hour)
	db.Create(&model.Setting{Key: ledgerMarker, Value: "USD", UpdatedAt: markerAt}) // exactly what 1.0.13 wrote
	migrated := model.CallLog{RequestID: "migrated-by-113", CreatedAt: markerAt.Add(-time.Hour), CostMicros: 1000000, Attempts: model.JSON(`[{"account_id":1,"cost_micros":3600000},{"account_id":2,"cost_micros":3600000}]`)}
	newUSD := model.CallLog{RequestID: "new-on-113", CreatedAt: markerAt.Add(time.Minute), CostMicros: 1000000, Attempts: model.JSON(`[{"account_id":1,"cost_micros":1000000}]`)}
	replayed := model.CallLog{RequestID: "replayed-112", CreatedAt: markerAt.Add(-time.Minute), CostMicros: 7200000, Attempts: model.JSON(`[{"account_id":1,"cost_micros":7200000}]`)}
	unpriced := model.CallLog{RequestID: "unpriced", CreatedAt: markerAt.Add(-time.Minute), CostMicros: 0, Attempts: model.JSON(`[{"account_id":1,"usage_status":"unknown"}]`)}
	for _, l := range []*model.CallLog{&migrated, &newUSD, &replayed, &unpriced} {
		if err := db.Create(l).Error; err != nil {
			t.Fatal(err)
		}
	}
	db.Create(&model.UsageHourly{Hour: time.Now().Truncate(time.Hour), CostMicros: 1000000, Requests: 1})
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	get := func(id string) model.CallLog {
		var l model.CallLog
		db.Where("request_id = ?", id).First(&l)
		return l
	}
	if l := get("migrated-by-113"); l.CostMicros != 1000000 || l.CostLedger != Ledger || attemptCosts(t, l.Attempts)[0] != 500000 || attemptCosts(t, l.Attempts)[1] != 500000 {
		t.Fatalf("attempts must be scaled onto the USD request amount: %d %q %s", l.CostMicros, l.CostLedger, l.Attempts)
	}
	if l := get("new-on-113"); l.CostMicros != 1000000 || attemptCosts(t, l.Attempts)[0] != 1000000 || l.CostLedger != Ledger {
		t.Fatalf("already-USD 1.0.13 row converted again: %d %s", l.CostMicros, l.Attempts)
	}
	if l := get("replayed-112"); l.CostMicros != 1000000 || attemptCosts(t, l.Attempts)[0] != 1000000 || l.CostLedger != Ledger {
		t.Fatalf("1.0.12 record replayed after the 1.0.13 migration: %d %s", l.CostMicros, l.Attempts)
	}
	if l := get("unpriced"); l.CostMicros != 0 || l.CostLedger != Ledger {
		t.Fatalf("unpriced row: %+v", l)
	}
	var h model.UsageHourly
	db.First(&h)
	if h.CostMicros != 1000000 {
		t.Fatalf("hourly rows were already USD after 1.0.13: %d", h.CostMicros)
	}
	var origin model.Setting
	db.Where("key = ?", ledgerOriginKey).First(&origin)
	if origin.Value != "v1" {
		t.Fatalf("origin %q", origin.Value)
	}
}

// An early 1.0.14 database (marker usd-v2) may hold rows its startup replay stored
// unconverted; their currency cannot be told from the row, so they are flagged, not divided.
func TestLedgerV2UnverifiedRows(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Setting{Key: ledgerMarker, Value: "usd-v2", UpdatedAt: time.Now()})
	priced := model.CallLog{RequestID: "missed", CreatedAt: time.Now(), CostMicros: 7200000, Attempts: model.JSON(`[{"cost_micros":7200000}]`)}
	zero := model.CallLog{RequestID: "zero", CreatedAt: time.Now()}
	db.Create(&priced)
	db.Create(&zero)
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	db.First(&priced, priced.ID)
	db.First(&zero, zero.ID)
	if priced.CostMicros != 7200000 || priced.CostLedger != LedgerUnverified || attemptCosts(t, priced.Attempts)[0] != 7200000 {
		t.Fatalf("undecidable row must be flagged, not divided: %+v", priced)
	}
	if zero.CostLedger != Ledger {
		t.Fatalf("a row without amounts is simply stamped: %+v", zero)
	}
}

// With a USD display currency there is nothing to convert; rows are only stamped.
func TestLedgerUSDDisplayStampsOnly(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "USD", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	l := model.CallLog{RequestID: "usd", CreatedAt: time.Now(), CostMicros: 1000000, Attempts: model.JSON(`[{"cost_micros":1000000}]`)}
	db.Create(&l)
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	db.First(&l, l.ID)
	if l.CostMicros != 1000000 || string(l.Attempts) != `[{"cost_micros":1000000}]` || l.CostLedger != Ledger {
		t.Fatalf("usd display: %d %s %q", l.CostMicros, l.Attempts, l.CostLedger)
	}
}

// Journal records without a stamp: on a database that came from 1.0.12 the ones created
// before the migration are in the display currency; on any other origin they are USD.
func TestLegacyCostFixer(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLedger(db, st); err != nil { // fresh database: origin v0
		t.Fatal(err)
	}
	fix := LegacyCostFixer(db, st)
	old := &model.CallLog{CreatedAt: time.Now().Add(-time.Hour), CostMicros: 7200000, Attempts: model.JSON(`[{"account_id":1,"cost_micros":7200000},{"account_id":2}]`)}
	fix(old)
	if old.CostMicros != 1000000 || old.CostLedger != Ledger || attemptCosts(t, old.Attempts)[0] != 1000000 {
		t.Fatalf("legacy record: %d %q %s", old.CostMicros, old.CostLedger, old.Attempts)
	}
	fix(old) // already stamped
	if old.CostMicros != 1000000 {
		t.Fatalf("stamped record converted again: %d", old.CostMicros)
	}
	fresh := &model.CallLog{CreatedAt: time.Now(), CostMicros: 500, CostLedger: Ledger}
	fix(fresh)
	if fresh.CostMicros != 500 {
		t.Fatal("fresh record must not change")
	}
	late := &model.CallLog{CreatedAt: time.Now().Add(time.Hour), CostMicros: 500}
	fix(late)
	if late.CostMicros != 500 || late.CostLedger != Ledger {
		t.Fatalf("record created after the migration is USD: %+v", late)
	}

	// Origin v1: whatever 1.0.13 left in the journal is USD. (Subtest: testSvc names the
	// shared in-memory database after the test, so this needs its own name.)
	t.Run("origin-v1", func(t *testing.T) {
		_, db2, st2 := testSvc(t)
		if err := st2.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
			t.Fatal(err)
		}
		db2.Create(&model.Setting{Key: ledgerMarker, Value: "USD", UpdatedAt: time.Now().Add(-time.Hour)})
		if err := MigrateLedger(db2, st2); err != nil {
			t.Fatal(err)
		}
		usd := &model.CallLog{CreatedAt: time.Now().Add(-2 * time.Hour), CostMicros: 1000000, Attempts: model.JSON(`[{"cost_micros":1000000}]`)}
		LegacyCostFixer(db2, st2)(usd)
		if usd.CostMicros != 1000000 || attemptCosts(t, usd.Attempts)[0] != 1000000 || usd.CostLedger != Ledger {
			t.Fatalf("1.0.13 journal record divided: %+v", usd)
		}
	})
}

// R114-01: the fixer is part of the store's construction, so a record left in the
// journal by 1.0.12 is converted by the very first replay at startup.
func TestR114StartupReplay(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	jp := filepath.Join(dir, "data", "journal")
	if err := os.MkdirAll(jp, 0o700); err != nil {
		t.Fatal(err)
	}
	l := model.CallLog{RequestID: "pending-old", CreatedAt: time.Now(), CostMicros: 7200000, PromptTokens: 1, TotalTokens: 1, UsageStatus: model.UsageConfirmed,
		Attempts: model.JSON(`[{"account_id":1,"usage_status":"confirmed","prompt_tokens":1,"cost_micros":7200000}]`)}
	b, _ := json.Marshal(l)
	if err := os.WriteFile(filepath.Join(jp, "calls.jsonl"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	// Same order as main: migrate, then construct the store with the fixer.
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	s, err := logstore.New(db, dir, func() int { return 30 }, logstore.WithCostFixer(LegacyCostFixer(db, st)))
	if err != nil {
		t.Fatal(err)
	}
	s.Close(context.Background())
	var got model.CallLog
	if err := db.Where("request_id = ?", l.RequestID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.CostMicros != 1000000 || got.CostLedger != Ledger || attemptCosts(t, got.Attempts)[0] != 1000000 {
		t.Fatalf("startup replay bypasses fixer: cost=%d ledger=%q attempts=%s", got.CostMicros, got.CostLedger, got.Attempts)
	}
	// A second start finds nothing left to do.
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&model.CallLog{}).Where("cost_ledger = ''").Count(&n)
	if n != 0 {
		t.Fatalf("%d rows without a stamp after replay", n)
	}
}

// R114-02 (reviewer's shape): a 1.0.13 row, both sides USD, no stamp, display CNY.
func TestR114V113NewUSDRecords(t *testing.T) {
	_, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Setting{Key: ledgerMarker, Value: "USD"})
	l := model.CallLog{RequestID: "new-on-113", CreatedAt: time.Now(), CostMicros: 1000000, PromptTokens: 1, TotalTokens: 1, UsageStatus: model.UsageConfirmed,
		Attempts: model.JSON(`[{"account_id":1,"usage_status":"confirmed","prompt_tokens":1,"cost_micros":1000000}]`)}
	db.Create(&l)
	if err := MigrateLedger(db, st); err != nil {
		t.Fatal(err)
	}
	db.First(&l, l.ID)
	if c := attemptCosts(t, l.Attempts); c[0] != 1000000 || l.CostMicros != 1000000 {
		t.Fatalf("already-USD 1.0.13 attempt converted again: got=%d want=1000000", c[0])
	}
}
