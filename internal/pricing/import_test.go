package pricing

import (
	"math"
	"strings"
	"testing"

	"yzapi/internal/model"
)

const easycpaSample = `{"schemaVersion":1,"updatedAt":"2026-09-07","models":[
 {"id":"gpt-6-astra","inputPer1M":10,"outputPer1M":50,"cacheReadPer1M":1,"cacheCreationPer1M":12.5},
 {"id":"gpt-5.5","inputPer1M":5,"outputPer1M":30,"cacheReadPer1M":0.5,"cacheCreationPer1M":6.25},
 {"id":"broken"}]}`

const litellmSample = `{
 "sample_spec": {"max_tokens": 1, "litellm_provider": "x", "mode": "chat"},
 "gpt-5.5": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":5e-06,"output_cost_per_token":3e-05,"cache_read_input_token_cost":5e-07,"source":"https://openai.com/api/pricing"},
 "gemini/gemini-3.8-flash": {"litellm_provider":"gemini","mode":"chat","input_cost_per_token":7.5e-07,"output_cost_per_token":3.75e-06,"cache_read_input_token_cost":7.5e-08},
 "openrouter/openai/gpt-5.5": {"litellm_provider":"openrouter","mode":"chat","input_cost_per_token":5e-06,"output_cost_per_token":3e-05},
 "azure/gpt-5.5": {"litellm_provider":"azure","mode":"chat","input_cost_per_token":5e-06,"output_cost_per_token":3e-05},
 "dall-e-3": {"litellm_provider":"openai","mode":"image_generation","input_cost_per_token":0,"output_cost_per_token":0},
 "dashscope/qwen3.8-max": {"litellm_provider":"dashscope","mode":"chat","input_cost_per_token":2e-06,"output_cost_per_token":6e-06,"cache_read_input_token_cost":2.5e-07},
 "text-embedding-3-small": {"litellm_provider":"openai","mode":"embedding","input_cost_per_token":2e-08,"output_cost_per_token":0}
}`

func TestParseCatalogFormats(t *testing.T) {
	cat, err := ParseCatalog([]byte(easycpaSample))
	if err != nil || cat.Source != "easycpa" || cat.Date != "2026-09-07" || len(cat.Rows) != 2 || cat.Skipped != 1 {
		t.Fatalf("easycpa: %v %+v", err, cat)
	}
	if r := cat.Rows[0]; r.Pattern != "gpt-6-astra" || r.Provider != "" || r.InputPerM != 10 || r.WritePerM != 12.5 || r.Currency != "USD" {
		t.Fatalf("easycpa row: %+v", r)
	}
	cat, err = ParseCatalog([]byte(litellmSample))
	if err != nil || cat.Source != "litellm" {
		t.Fatalf("litellm: %v %+v", err, cat)
	}
	got := map[string]CatalogRow{}
	for _, r := range cat.Rows {
		got[r.Provider+"|"+r.Pattern] = r
	}
	if len(cat.Rows) != 4 || cat.Skipped != 3 { // openrouter, azure, dall-e skipped; sample_spec ignored
		t.Fatalf("litellm rows=%d skipped=%d: %+v", len(cat.Rows), cat.Skipped, got)
	}
	if r := got["openai|gpt-5.5"]; r.InputPerM != 5 || r.OutputPerM != 30 || r.CachedPerM != 0.5 || r.Note == "" {
		t.Fatalf("gpt-5.5: %+v", r)
	}
	if r := got["gemini|gemini-3.8-flash"]; r.InputPerM != 0.75 || r.OutputPerM != 3.75 || r.CachedPerM != 0.075 {
		t.Fatalf("gemini prefix stripped and scaled: %+v", r)
	}
	if r := got["aliyun|qwen3.8-max"]; r.InputPerM != 2 {
		t.Fatalf("dashscope -> aliyun: %+v", r)
	}
	if _, ok := got["openai|text-embedding-3-small"]; !ok {
		t.Fatal("embedding rows are imported")
	}
	if _, err := ParseCatalog([]byte("not json")); err == nil {
		t.Fatal("garbage must fail")
	}
}

// New rows are added, built-in / imported rows are updated in place (Builtin kept),
// manual and edited rows are kept unless overwrite is set; a second run is all "same".
func TestImportPlanAndApply(t *testing.T) {
	_, db, _ := testSvc(t)
	// A manual row (replacing the built-in one) and an edited built-in row that the import must not touch.
	db.Where("pattern = ? AND provider = ?", "gpt-5.5", "openai").Delete(&model.ModelPrice{})
	db.Create(&model.ModelPrice{Pattern: "gpt-5.5", Provider: "openai", InputPerM: 4, OutputPerM: 24, Currency: "USD", Enabled: true})
	db.Model(&model.ModelPrice{}).Where("pattern = ? AND provider = ?", "gemini-2.5-flash", "gemini").Updates(map[string]any{"input_per_m": 9, "edited": true})
	cat, err := ParseCatalog([]byte(litellmSample))
	if err != nil {
		t.Fatal(err)
	}
	// Default options: gpt-5.5 manual -> kept (manual); gemini-3.8-flash builtin -> same
	// numbers (0.75/3.75/0.075) -> same; qwen3.8-max builtin is 12/36 CNY and the catalog
	// prices it in USD -> kept (currency) with both currencies in the change; embedding -> same.
	plan0, err := PlanImport(db, cat, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan0.Kept != 2 || plan0.Same != 2 || plan0.Updated != 0 || plan0.New != 0 {
		t.Fatalf("default plan: %s", plan0.Summary())
	}
	var qwenCh *ImportChange
	for i := range plan0.Changes {
		if plan0.Changes[i].Pattern == "qwen3.8-max" {
			qwenCh = &plan0.Changes[i]
		}
	}
	if qwenCh == nil || qwenCh.Action != "keep" || qwenCh.Reason != "currency" || qwenCh.OldCurrency != "CNY" || qwenCh.NewCurrency != "USD" || !qwenCh.CurrencyChanged {
		t.Fatalf("currency-differing built-in must be kept by default and show both currencies: %+v", qwenCh)
	}
	// Opting in to currency changes turns it into an update.
	plan, err := PlanImportWith(db, cat, ImportOptions{OverwriteCurrency: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kept != 1 || plan.Same != 2 || plan.Updated != 1 || plan.New != 0 {
		t.Fatalf("plan: %s", plan.Summary())
	}
	if err := plan.Apply(db); err != nil {
		t.Fatal(err)
	}
	var manual, qwen model.ModelPrice
	db.Where("pattern = ? AND provider = ?", "gpt-5.5", "openai").First(&manual)
	db.Where("pattern = ? AND provider = ?", "qwen3.8-max", "aliyun").First(&qwen)
	if manual.InputPerM != 4 || manual.Source != "" {
		t.Fatalf("manual row must be untouched: %+v", manual)
	}
	if qwen.InputPerM != 2 || qwen.Currency != "USD" || qwen.Source != "litellm" || !qwen.Builtin {
		t.Fatalf("builtin row updated in place with Builtin kept: %+v", qwen)
	}
	// Second run: nothing changes.
	plan2, _ := PlanImport(db, cat, false)
	if plan2.Updated != 0 || plan2.New != 0 || plan2.Kept != 1 {
		t.Fatalf("second run: %s", plan2.Summary())
	}
	// Overwrite: the manual row is replaced and loses its edited flag.
	plan3, _ := PlanImport(db, cat, true)
	if plan3.Kept != 0 || plan3.Updated != 1 {
		t.Fatalf("overwrite plan: %s", plan3.Summary())
	}
	if err := plan3.Apply(db); err != nil {
		t.Fatal(err)
	}
	if plan3.Updated != 1 || plan3.Same != 3 { // Apply leaves the in-transaction counts on the plan
		t.Fatalf("applied counts: %s", plan3.Summary())
	}
	db.Where("pattern = ? AND provider = ?", "gpt-5.5", "openai").First(&manual)
	if manual.InputPerM != 5 || manual.Source != "litellm" || manual.Edited {
		t.Fatalf("overwrite: %+v", manual)
	}
	// EasyCPA rows have no provider: they land as generic rows, new on top of the provider rows.
	cat2, _ := ParseCatalog([]byte(easycpaSample))
	plan4, _ := PlanImport(db, cat2, false)
	if plan4.New != 2 {
		t.Fatalf("easycpa generic rows: %s", plan4.Summary())
	}
	// ResetBuiltin restores the reference numbers on built-in rows and keeps imported/manual extras.
	if err := plan4.Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := ResetBuiltin(db); err != nil {
		t.Fatal(err)
	}
	var qwenReset model.ModelPrice // fresh receiver: First() also filters by an id already set on the struct
	db.Where("pattern = ? AND provider = ?", "qwen3.8-max", "aliyun").First(&qwenReset)
	qwen = qwenReset
	var generic int64
	db.Model(&model.ModelPrice{}).Where("provider = '' AND pattern = ?", "gpt-6-astra").Count(&generic)
	if qwen.InputPerM != 12 || qwen.Currency != "CNY" || generic != 1 {
		t.Fatalf("reset: qwen=%+v generic=%d", qwen, generic)
	}
}

// R126-01: the configured EasyCLIProxyAPI source keys "models" by model id (an object),
// not an array; both shapes parse and keys are visited in sorted order.
func TestR126EasyCPAObjectCatalog(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"updatedAt":"2026-09-07","models":{
		"gpt-6-astra":{"inputPer1M":10,"outputPer1M":50,"cacheReadPer1M":1,"cacheCreationPer1M":12.5},
		"GPT-5.5":{"inputPer1M":5,"outputPer1M":30,"cacheReadPer1M":0.5},
		"empty":{}}}`)
	cat, err := ParseCatalog(raw)
	if err != nil {
		t.Fatalf("configured EasyCLIProxyAPI source format must parse: %v", err)
	}
	if len(cat.Rows) != 2 || cat.Skipped != 1 || cat.Rows[0].Pattern != "gpt-5.5" || cat.Rows[1].Pattern != "gpt-6-astra" || cat.Rows[1].InputPerM != 10 || cat.Rows[1].WritePerM != 12.5 {
		t.Fatalf("object catalog rows: %+v", cat)
	}
	if _, err := ParseCatalog([]byte(`{"schemaVersion":1,"models":"nope"}`)); err == nil {
		t.Fatal("models of a wrong type must be an error")
	}
}

// R126-04: every catalog row passes the same validation as a manual row: negative or
// huge prices, over-long names and unknown currencies are rejected with a reason, not
// written.
func TestR126CatalogRejectsOutOfRangePrices(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"models":[
		{"id":"negative-price","inputPer1M":-1,"outputPer1M":2},
		{"id":"huge-output","inputPer1M":1,"outputPer1M":1000001},
		{"id":"bad-cache","inputPer1M":1,"outputPer1M":2,"cacheReadPer1M":-0.5},
		{"id":"bad-write","inputPer1M":1,"outputPer1M":2,"cacheCreationPer1M":1e9},
		{"id":"` + strings.Repeat("x", 129) + `","inputPer1M":1,"outputPer1M":2},
		{"id":"fine","inputPer1M":1,"outputPer1M":2}]}`)
	cat, err := ParseCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Rows) != 1 || cat.Rows[0].Pattern != "fine" || cat.Invalid != 5 || len(cat.InvalidRows) != 5 {
		t.Fatalf("validation: rows=%+v invalid=%d reasons=%v", cat.Rows, cat.Invalid, cat.InvalidRows)
	}
	for _, bad := range []CatalogRow{
		{Pattern: "m", Currency: "EUR", InputPerM: 1}, {Pattern: "m", Currency: "USD", InputPerM: math.NaN()},
		{Pattern: "m", Currency: "USD", OutputPerM: math.Inf(1)}, {Pattern: "m", Provider: strings.Repeat("p", 33), Currency: "USD"},
	} {
		if ValidateRow(&bad) == "" {
			t.Fatalf("row must be rejected: %+v", bad)
		}
	}
	good := CatalogRow{Pattern: " GPT-5.5 ", Provider: "OpenAI", Currency: "usd", InputPerM: 1}
	if msg := ValidateRow(&good); msg != "" || good.Pattern != "gpt-5.5" || good.Provider != "openai" || good.Currency != "USD" {
		t.Fatalf("normalisation: %q %+v", msg, good)
	}
	_, db, _ := testSvc(t)
	plan, err := PlanImport(db, cat, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Invalid != 5 || plan.New != 1 {
		t.Fatalf("plan carries the invalid count: %s", plan.Summary())
	}
}

// R126-03: rows sharing a (provider, pattern) key collapse to one on import (edited over
// untouched, manual over built-in, then the older id), and DedupePrices prepares an old
// table for the unique index. Lookups never see two candidates for one key.
func TestR126ImportKeepsProviderPatternUnique(t *testing.T) {
	_, db, _ := testSvc(t)
	const pattern = "review-duplicate-model"
	rows := []model.ModelPrice{
		{Pattern: pattern, Provider: "openai", InputPerM: 1, OutputPerM: 2, Currency: "USD", Enabled: true, Source: "litellm"},
		{Pattern: pattern, Provider: "openai", InputPerM: 3, OutputPerM: 4, Currency: "USD", Enabled: true, Source: "litellm"},
		{Pattern: pattern, Provider: "OpenAI", InputPerM: 7, OutputPerM: 8, Currency: "USD", Enabled: true, Edited: true},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	cat := &Catalog{Source: "litellm", Rows: []CatalogRow{{Pattern: pattern, Provider: "openai", InputPerM: 5, OutputPerM: 6, Currency: "USD"}}}
	plan, err := PlanImport(db, cat, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Duplicate != 2 || plan.Kept != 1 {
		t.Fatalf("plan must report the duplicates and keep the edited survivor: %s", plan.Summary())
	}
	if err := plan.Apply(db); err != nil {
		t.Fatal(err)
	}
	var got []model.ModelPrice
	if err := db.Where("LOWER(provider) = ? AND pattern = ?", "openai", pattern).Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Edited || got[0].InputPerM != 7 {
		t.Fatalf("price lookup key is not unique after import or the wrong row survived: %+v", got)
	}
	// DedupePrices on a pre-index table: lower-cases keys and removes the losers.
	db.Create(&model.ModelPrice{Pattern: "Dup-Model", Provider: "Gemini", InputPerM: 1, OutputPerM: 1, Currency: "USD", Enabled: true})
	db.Create(&model.ModelPrice{Pattern: "dup-model", Provider: "gemini", InputPerM: 2, OutputPerM: 2, Currency: "USD", Enabled: true, Builtin: true})
	removed, err := DedupePrices(db)
	if err != nil || removed != 1 {
		t.Fatalf("dedupe: removed=%d err=%v", removed, err)
	}
	var left []model.ModelPrice
	db.Where("pattern = ?", "dup-model").Find(&left)
	if len(left) != 1 || left[0].Provider != "gemini" || left[0].Builtin || left[0].InputPerM != 1 {
		t.Fatalf("manual row wins over built-in and keys are lower-cased: %+v", left)
	}
}

// R127-05: an invalid entry never shadows a later valid entry for the same key, in
// either format; a later invalid duplicate of a valid entry is counted as invalid.
func TestR127InvalidDuplicateDoesNotSuppressValidRow(t *testing.T) {
	cat, err := ParseCatalog([]byte(`{"schemaVersion":1,"models":[
		{"id":"r127-dup","inputPer1M":-1,"outputPer1M":2},
		{"id":"R127-DUP","inputPer1M":1,"outputPer1M":2},
		{"id":"r127-dup","inputPer1M":3,"outputPer1M":3}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Rows) != 1 || cat.Rows[0].Pattern != "r127-dup" || cat.Rows[0].InputPerM != 1 || cat.Invalid != 1 || cat.Skipped != 1 {
		t.Fatalf("easycpa array: rows=%+v invalid=%d skipped=%d", cat.Rows, cat.Invalid, cat.Skipped)
	}
	cat, err = ParseCatalog([]byte(`{"schemaVersion":1,"models":{"R127-Obj":{"inputPer1M":-1,"outputPer1M":2},"r127-obj":{"inputPer1M":2,"outputPer1M":2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Rows) != 1 || cat.Rows[0].InputPerM != 2 || cat.Invalid != 1 {
		t.Fatalf("easycpa object: rows=%+v invalid=%d skipped=%d", cat.Rows, cat.Invalid, cat.Skipped)
	}
	cat, err = ParseCatalog([]byte(`{
		"openai/r127-lite": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":-1,"output_cost_per_token":1e-06},
		"r127-lite": {"litellm_provider":"openai","mode":"chat","input_cost_per_token":1e-06,"output_cost_per_token":1e-06}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Rows) != 1 || cat.Rows[0].Pattern != "r127-lite" || cat.Rows[0].InputPerM != 1 || cat.Invalid != 1 {
		t.Fatalf("litellm prefix collision: rows=%+v invalid=%d skipped=%d", cat.Rows, cat.Invalid, cat.Skipped)
	}
}

// NormalizeRows is what a pre-1.0.27 snapshot goes through on restore.
func TestR127NormalizeRows(t *testing.T) {
	kept, skipped, merged := NormalizeRows([]model.ModelPrice{
		{ID: 1, Pattern: "A-Model", Provider: "OpenAI", InputPerM: 1, OutputPerM: 1, Currency: "usd"},
		{ID: 2, Pattern: "a-model", Provider: "openai", InputPerM: 2, OutputPerM: 2, Currency: "USD", Builtin: true},
		{ID: 3, Pattern: "b-model", Provider: "openai", InputPerM: 1, OutputPerM: 1, Currency: "EUR"},
	})
	if len(kept) != 1 || kept[0].ID != 1 || kept[0].Pattern != "a-model" || kept[0].Provider != "openai" || kept[0].Currency != "USD" || merged != 1 || len(skipped) != 1 {
		t.Fatalf("kept=%+v skipped=%v merged=%d", kept, skipped, merged)
	}
}
