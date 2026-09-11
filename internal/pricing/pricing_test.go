package pricing

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

func testSvc(t *testing.T) (*Service, *gorm.DB, *settings.Store) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	st, err := settings.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Seed(db); err != nil {
		t.Fatal(err)
	}
	svc, err := New(db, st)
	if err != nil {
		t.Fatal(err)
	}
	return svc, db, st
}

// Exact name beats prefix, longer prefix beats shorter, vendor prefixes and date
// suffixes are tolerated, and an unknown model is reported as unpriced (not free).
func TestLookupPrecedence(t *testing.T) {
	svc, _, _ := testSvc(t)
	for _, tc := range []struct{ prov, name, want string }{
		{"openai", "gpt-5", "gpt-5"},
		{"openai", "gpt-5-mini", "gpt-5-mini"},
		{"openai", "gpt-5-2026-03-01", "gpt-5"},
		{"anthropic", "claude-sonnet-4-5-20250929", "claude-sonnet-4-5"},
		{"custom", "anthropic/claude-sonnet-4-5", "claude-sonnet-4-5"}, // provider-agnostic fallback + vendor prefix
		{"openai", "GPT-4O", "gpt-4o"},
	} {
		p, ok := svc.Lookup(tc.prov, tc.name)
		if !ok || p.Pattern != tc.want {
			t.Fatalf("%s/%s -> %q ok=%v, want %s", tc.prov, tc.name, p.Pattern, ok, tc.want)
		}
	}
	if _, ok := svc.Lookup("custom", "no-such-model-9"); ok {
		t.Fatal("unknown model must be unpriced")
	}
}

// Cost uses cached price for cached prompt tokens and converts USD rows into the base
// currency with the configured rate; a CNY base with a CNY row needs no conversion.
func TestCostAndCurrency(t *testing.T) {
	svc, _, st := testSvc(t)
	// gpt-5: $1.25 in, $10 out, $0.125 cached. The ledger is USD whatever the display
	// currency is (default display CNY at 7.2).
	micros, ok := svc.Cost("openai", "gpt-5", 1_000_000, 100_000, 200_000, 0)
	if !ok {
		t.Fatal("priced")
	}
	usd := 0.8*1.25 + 0.2*0.125 + 0.1*10 // 1.0 + 0.025 + 1.0
	if want := int64(usd * 1e6); micros < want-5 || micros > want+5 {
		t.Fatalf("cost=%d want ~%d (ledger must be USD)", micros, want)
	}
	if got := svc.Display(micros); got < usd*7.2-1e-6 || got > usd*7.2+1e-6 || svc.Currency() != "CNY" {
		t.Fatalf("display in CNY: %v (%s)", got, svc.Currency())
	}
	if err := st.SetPricing(settings.Pricing{Currency: "USD", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Display(micros); got < usd-1e-6 || got > usd+1e-6 {
		t.Fatalf("display in USD: %v", got)
	}
	if again, _ := svc.Cost("openai", "gpt-5", 1_000_000, 100_000, 200_000, 0); again != micros {
		t.Fatalf("stored amount must not depend on the display currency: %d vs %d", again, micros)
	}
	cny, _ := svc.Cost("zhipu", "glm-4.5", 1_000_000, 0, 0, 0) // ¥2 -> ledger USD at 7.2
	cnyWant := 2.0 / 7.2 * 1e6
	if want := int64(cnyWant); cny < want-5 || cny > want+5 {
		t.Fatalf("cny row in usd ledger=%d want ~%d", cny, want)
	}
}

// Cache writes are billed at their own rate; a row without one falls back to the input
// price. cached + cacheWrite never exceed prompt.
func TestCacheWritePricing(t *testing.T) {
	svc, db, st := testSvc(t)
	if err := st.SetPricing(settings.Pricing{Currency: "USD", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.ModelPrice{Pattern: "cw", Provider: "anthropic", Currency: "USD", InputPerM: 3, OutputPerM: 15, CachedInputPerM: 0.3, CacheWritePerM: 3.75, Enabled: true})
	db.Create(&model.ModelPrice{Pattern: "nocw", Provider: "anthropic", Currency: "USD", InputPerM: 3, OutputPerM: 15, Enabled: true})
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	// 1000 written, 0 plain, 0 out -> 3750 micro-USD
	if got, _ := svc.Cost("anthropic", "cw", 1000, 0, 0, 1000); got != 3750 {
		t.Fatalf("cache write only: %d", got)
	}
	// 4000 prompt = 1000 plain + 2000 read + 1000 written, 100 out
	want := int64(1000*3 + 2000*0.3 + 1000*3.75 + 100*15)
	if got, _ := svc.Cost("anthropic", "cw", 4000, 100, 2000, 1000); got != want {
		t.Fatalf("mixed: %d want %d", got, want)
	}
	if got, _ := svc.Cost("anthropic", "nocw", 1000, 0, 0, 1000); got != 3000 {
		t.Fatalf("fallback to input price: %d", got)
	}
	if got, _ := svc.Cost("anthropic", "cw", 1000, 0, 800, 800); got != int64(800*0.3+200*3.75) {
		t.Fatalf("clamped write: %d", got)
	}
}

// Seeding is idempotent and never overwrites an edited built-in; reset restores it.
func TestSeedAndReset(t *testing.T) {
	svc, db, _ := testSvc(t)
	var n1 int64
	db.Model(&model.ModelPrice{}).Count(&n1)
	if err := Seed(db); err != nil {
		t.Fatal(err)
	}
	var n2 int64
	db.Model(&model.ModelPrice{}).Count(&n2)
	if n1 != n2 || n1 != int64(len(Builtin())) {
		t.Fatalf("seed not idempotent: %d %d %d", n1, n2, len(Builtin()))
	}
	db.Model(&model.ModelPrice{}).Where("pattern = ?", "gpt-5").Update("output_per_m", 99)
	if err := Seed(db); err != nil {
		t.Fatal(err)
	}
	_ = svc.Reload()
	if p, _ := svc.Lookup("openai", "gpt-5"); p.OutputPerM != 99 {
		t.Fatal("seed must keep edited rows")
	}
	if err := ResetBuiltin(db); err != nil {
		t.Fatal(err)
	}
	_ = svc.Reload()
	if p, _ := svc.Lookup("openai", "gpt-5"); p.OutputPerM != 10 {
		t.Fatalf("reset must restore reference price, got %v", p.OutputPerM)
	}
}
