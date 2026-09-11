package pricing

import (
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
	plan, err := PlanImport(db, cat, false)
	if err != nil {
		t.Fatal(err)
	}
	// gpt-5.5 manual -> kept; gemini-3.8-flash builtin -> same numbers (0.75/3.75/0.075) -> same;
	// qwen3.8-max builtin CNY row is 12/36 -> update (numbers differ, currency differs); embedding -> same.
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
