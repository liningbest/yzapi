package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/pricing"
)

func r128Plan(id string, rows int, claimed bool) *storedImport {
	return &storedImport{id: id, created: time.Now(), claimed: claimed, catalog: &pricing.Catalog{Rows: make([]pricing.CatalogRow, rows)}}
}

// R128-02: the preview cache's count and row limits are hard bounds: an oversized
// catalog is refused, a full cache of claimed plans refuses a new one, and claim never
// evicts a live preview just because the cache is at capacity.
func TestR128ImportPlanBudgetIsAHardBound(t *testing.T) {
	plans := newImportPlans()
	if err := plans.put(r128Plan("oversize", importRowBudget+1, false)); !errors.Is(err, errImportPlanTooLarge) {
		t.Fatalf("oversized preview must be refused: %v", err)
	}
	if n, rows := plans.pending(); n != 0 || rows != 0 {
		t.Fatalf("oversized preview retained: %d %d", n, rows)
	}
	for i := 0; i < importPlanCap; i++ {
		if err := plans.put(r128Plan(string(rune('a'+i)), 1, false)); err != nil {
			t.Fatal(err)
		}
	}
	if plans.claim("a") == nil {
		t.Fatalf("claim must not evict a live preview at capacity")
	}
	// One more preview evicts the oldest unclaimed ("b"), never the claimed "a".
	if err := plans.put(r128Plan("q", 1, false)); err != nil {
		t.Fatal(err)
	}
	if n, _ := plans.pending(); n != importPlanCap || plans.claim("b") != nil {
		t.Fatalf("expected the oldest unclaimed preview to be evicted: pending=%d", n)
	}
	// Row budget: a large preview evicts older ones until it fits.
	if err := plans.put(r128Plan("big", importRowBudget-1, false)); err != nil {
		t.Fatal(err)
	}
	if n, rows := plans.pending(); rows > importRowBudget || n != 2 { // "a" (claimed) + "big"
		t.Fatalf("row budget exceeded or wrong eviction: plans=%d rows=%d", n, rows)
	}
	// Every plan claimed: no room, refuse instead of exceeding the cap.
	full := newImportPlans()
	for i := 0; i < importPlanCap; i++ {
		if err := full.put(r128Plan(string(rune('a'+i)), 1, true)); err != nil {
			t.Fatal(err)
		}
	}
	if err := full.put(r128Plan("extra", 1, false)); !errors.Is(err, errImportPlansFull) {
		t.Fatalf("new preview must be refused when every cached plan is claimed: %v", err)
	}
	if n, _ := full.pending(); n != importPlanCap {
		t.Fatalf("cap exceeded: %d", n)
	}
}

// R128-04: note truncation never splits a multi-byte character, on every path.
func TestR128NoteTruncationPreservesUTF8(t *testing.T) {
	long := "a" + strings.Repeat("中", 85)
	r := pricing.CatalogRow{Pattern: "r128-note", Currency: "USD", Note: long}
	if msg := pricing.ValidateRow(&r); msg != "" || !utf8.ValidString(r.Note) || len(r.Note) > 255 || len(r.Note) < 250 {
		t.Fatalf("validate: %q len=%d valid=%v", msg, len(r.Note), utf8.ValidString(r.Note))
	}
	if got := pricing.TruncateUTF8("héllo", 2); got != "h" {
		t.Fatalf("boundary back-off: %q", got)
	}
	s := auditServer(t)
	admin := auditUser(s, "r128-note")
	if w := review126Call(s, s.createPrice, admin, "/api/admin/prices", map[string]any{"pattern": "r128-note", "provider": "openai", "input_per_m": 1, "output_per_m": 2, "currency": "USD", "note": long}); w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var row model.ModelPrice
	s.db.Where("pattern = ?", "r128-note").First(&row)
	if !utf8.ValidString(row.Note) {
		t.Fatalf("manual create stored invalid UTF-8: %q", row.Note)
	}
	cat, _ := pricing.ParseCatalog([]byte(`{"r128-lite":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":1e-06,"output_cost_per_token":1e-06,"source":"` + strings.Repeat("源", 120) + `"}}`))
	if len(cat.Rows) != 1 || !utf8.ValidString(cat.Rows[0].Note) || len(cat.Rows[0].Note) > 250 {
		t.Fatalf("import note: %+v", cat.Rows)
	}
	kept, _, _ := pricing.NormalizeRows([]model.ModelPrice{{ID: 1, Pattern: "r128-snap", Provider: "openai", Currency: "USD", Note: long}})
	if len(kept) != 1 || !utf8.ValidString(kept[0].Note) {
		t.Fatalf("restore note: %+v", kept)
	}
}

// R128-01: a disabled create is one transaction; a failing second statement leaves no
// row behind, for every resource that can be created disabled.
func TestR128DisabledCreateIsAtomicOnSecondWriteFailure(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r128-atomic")
	const cb = "r128:fail_disable_update"
	var table string
	if err := s.db.Callback().Update().Before("gorm:update").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			tx.AddError(errors.New("injected disable update failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer s.db.Callback().Update().Remove(cb)
	pg := model.PolicyGroup{Name: "r128-pg", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	cases := []struct {
		table string
		call  func() int
		count func() int64
	}{
		{table: "model_prices", call: func() int {
			return review126Call(s, s.createPrice, admin, "/x", map[string]any{"pattern": "r128-atomic", "provider": "openai", "input_per_m": 1, "output_per_m": 2, "currency": "USD", "enabled": false}).Code
		}, count: func() (n int64) {
			s.db.Model(&model.ModelPrice{}).Where("pattern = ?", "r128-atomic").Count(&n)
			return
		}},
		{table: "user_groups", call: func() int {
			return review126Call(s, s.createUserGroup, admin, "/x", map[string]any{"name": "r128-atomic", "enabled": false}).Code
		}, count: func() (n int64) { s.db.Model(&model.UserGroup{}).Where("name = ?", "r128-atomic").Count(&n); return }},
		{table: "policy_groups", call: func() int {
			return review126Call(s, s.createPolicyGroup, admin, "/x", map[string]any{"name": "r128-atomic", "action": "audit", "risk_level": "low", "enabled": false}).Code
		}, count: func() (n int64) { s.db.Model(&model.PolicyGroup{}).Where("name = ?", "r128-atomic").Count(&n); return }},
		{table: "sensitive_words", call: func() int {
			return review126Call(s, s.createWord, admin, "/x", map[string]any{"policy_group_id": pg.ID, "word": "r128-atomic", "enabled": false}).Code
		}, count: func() (n int64) {
			s.db.Model(&model.SensitiveWord{}).Where("word = ?", "r128-atomic").Count(&n)
			return
		}},
		{table: "audit_samples", call: func() int {
			return review126Call(s, s.createAuditSample, admin, "/x", map[string]any{"policy_group_id": pg.ID, "text": "r128-atomic", "enabled": false}).Code
		}, count: func() (n int64) { s.db.Model(&model.AuditSample{}).Where("text = ?", "r128-atomic").Count(&n); return }},
	}
	for _, tc := range cases {
		table = tc.table
		if code := tc.call(); code != http.StatusInternalServerError {
			t.Fatalf("%s: expected the injected failure to surface, got %d", tc.table, code)
		}
		if n := tc.count(); n != 0 {
			t.Fatalf("%s: failed disabled create left %d committed row(s)", tc.table, n)
		}
	}
}
