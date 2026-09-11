package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

// Catalog is a price list parsed from an external source, normalised to the table's
// own shape (per 1M tokens, one row per provider + pattern).
type Catalog struct {
	Source  string       `json:"source"` // "litellm" | "easycpa"
	Date    string       `json:"date"`   // the source's own date when it publishes one
	Rows    []CatalogRow `json:"rows"`
	Skipped int          `json:"skipped"` // entries the parser could not use (unknown provider, non-text mode, no prices)
}

type CatalogRow struct {
	Pattern    string  `json:"pattern"`
	Provider   string  `json:"provider"`
	InputPerM  float64 `json:"input_per_m"`
	OutputPerM float64 `json:"output_per_m"`
	CachedPerM float64 `json:"cached_input_per_m"`
	WritePerM  float64 `json:"cache_write_per_m"`
	Currency   string  `json:"currency"`
	Note       string  `json:"note"`
}

// KnownSources are the catalogs the UI offers by name.
var KnownSources = map[string]string{
	"litellm": "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json",
	"easycpa": "https://raw.githubusercontent.com/router-for-me/EasyCLIProxyAPI/main/src-tauri/resources/model_prices.json",
}

// litellmProviders maps LiteLLM's provider keys to this gateway's provider keys. Anything
// not listed (bedrock, azure, openrouter, ...) is skipped: those are resellers whose
// prices would collide with the first-party rows.
var litellmProviders = map[string]string{
	"openai": "openai", "anthropic": "anthropic", "gemini": "gemini", "xai": "xai", "deepseek": "deepseek",
	"moonshot": "moonshot", "dashscope": "aliyun", "volcengine": "volcengine", "minimax": "minimax", "zhipu": "zhipu", "zai": "zhipu",
	"mistral": "mistral", "groq": "groq", "together_ai": "together", "fireworks_ai": "fireworks", "cerebras": "cerebras",
}

// ParseCatalog detects the file format and converts it.
func ParseCatalog(data []byte) (*Catalog, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, errors.New("not a JSON document")
	}
	var probe struct {
		SchemaVersion *int            `json:"schemaVersion"`
		Models        json.RawMessage `json:"models"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.SchemaVersion != nil && len(probe.Models) > 0 {
		return parseEasyCPA(data)
	}
	return parseLiteLLM(data)
}

func parseEasyCPA(data []byte) (*Catalog, error) {
	var in struct {
		UpdatedAt string `json:"updatedAt"`
		Models    []struct {
			ID       string   `json:"id"`
			Input    *float64 `json:"inputPer1M"`
			Output   *float64 `json:"outputPer1M"`
			CacheRd  *float64 `json:"cacheReadPer1M"`
			CacheCrt *float64 `json:"cacheCreationPer1M"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	cat := &Catalog{Source: "easycpa", Date: in.UpdatedAt}
	seen := map[string]bool{}
	for _, m := range in.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" || m.Input == nil || m.Output == nil || seen[strings.ToLower(id)] {
			cat.Skipped++
			continue
		}
		seen[strings.ToLower(id)] = true
		row := CatalogRow{Pattern: id, Provider: "", InputPerM: *m.Input, OutputPerM: *m.Output, Currency: "USD", Note: "导入自 EasyCLIProxyAPI " + in.UpdatedAt}
		if m.CacheRd != nil {
			row.CachedPerM = *m.CacheRd
		}
		if m.CacheCrt != nil {
			row.WritePerM = *m.CacheCrt
		}
		cat.Rows = append(cat.Rows, row)
	}
	return cat, nil
}

func parseLiteLLM(data []byte) (*Catalog, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	type entry struct {
		Provider   string   `json:"litellm_provider"`
		Mode       string   `json:"mode"`
		In         *float64 `json:"input_cost_per_token"`
		Out        *float64 `json:"output_cost_per_token"`
		CacheRead  *float64 `json:"cache_read_input_token_cost"`
		CacheWrite *float64 `json:"cache_creation_input_token_cost"`
		Source     string   `json:"source"`
	}
	cat := &Catalog{Source: "litellm"}
	perM := func(v *float64) float64 {
		if v == nil {
			return 0
		}
		return math.Round(*v*1e6*1e6) / 1e6 // per-token -> per-1M, 6 decimals
	}
	seen := map[string]bool{}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic "first wins" on duplicates
	for _, k := range keys {
		if k == "sample_spec" {
			continue
		}
		var e entry
		if json.Unmarshal(in[k], &e) != nil || e.In == nil || e.Out == nil {
			cat.Skipped++
			continue
		}
		switch e.Mode {
		case "chat", "completion", "responses", "embedding":
		default:
			cat.Skipped++
			continue
		}
		prov, ok := litellmProviders[e.Provider]
		if !ok {
			cat.Skipped++
			continue
		}
		name := k
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if name == "" || seen[prov+"|"+strings.ToLower(name)] {
			cat.Skipped++
			continue
		}
		seen[prov+"|"+strings.ToLower(name)] = true
		note := "导入自 LiteLLM"
		if e.Source != "" {
			note += " · " + e.Source
		}
		if len(note) > 250 {
			note = note[:250]
		}
		cat.Rows = append(cat.Rows, CatalogRow{Pattern: name, Provider: prov, InputPerM: perM(e.In), OutputPerM: perM(e.Out),
			CachedPerM: perM(e.CacheRead), WritePerM: perM(e.CacheWrite), Currency: "USD", Note: note})
	}
	return cat, nil
}

// ImportChange is one planned row change.
type ImportChange struct {
	Action   string      `json:"action"` // new | update | keep
	Pattern  string      `json:"pattern"`
	Provider string      `json:"provider"`
	Old      *[4]float64 `json:"old,omitempty"` // in, out, cached, write
	New      [4]float64  `json:"new"`
	Reason   string      `json:"reason,omitempty"`
}

// ImportPlan is what an import would do; Apply writes it.
type ImportPlan struct {
	Source    string         `json:"source"`
	Date      string         `json:"date"`
	Total     int            `json:"total"`   // rows in the catalog
	New       int            `json:"new"`     // rows that do not exist yet
	Updated   int            `json:"updated"` // existing rows whose numbers change
	Same      int            `json:"same"`    // existing rows already equal
	Kept      int            `json:"kept"`    // manual / edited rows left alone
	Skipped   int            `json:"skipped"` // catalog entries the parser could not use
	Changes   []ImportChange `json:"changes"` // new + update + kept (capped for the preview)
	overwrite bool
	catalog   *Catalog
}

const importPreviewCap = 500

// PlanImport compares a catalog with the current table. Rows an administrator created or
// edited by hand are kept unless overwrite is set; built-in and previously imported rows
// are updated in place (their Builtin flag survives so "reset built-in" still works).
func PlanImport(db *gorm.DB, cat *Catalog, overwrite bool) (*ImportPlan, error) {
	var existing []model.ModelPrice
	if err := db.Find(&existing).Error; err != nil {
		return nil, err
	}
	byKey := map[string]*model.ModelPrice{}
	for i := range existing {
		byKey[strings.ToLower(existing[i].Provider)+"|"+strings.ToLower(existing[i].Pattern)] = &existing[i]
	}
	plan := &ImportPlan{Source: cat.Source, Date: cat.Date, Total: len(cat.Rows), Skipped: cat.Skipped, overwrite: overwrite, catalog: cat}
	add := func(ch ImportChange) {
		if len(plan.Changes) < importPreviewCap {
			plan.Changes = append(plan.Changes, ch)
		}
	}
	for _, r := range cat.Rows {
		nv := [4]float64{r.InputPerM, r.OutputPerM, r.CachedPerM, r.WritePerM}
		cur, ok := byKey[strings.ToLower(r.Provider)+"|"+strings.ToLower(r.Pattern)]
		if !ok {
			plan.New++
			add(ImportChange{Action: "new", Pattern: r.Pattern, Provider: r.Provider, New: nv})
			continue
		}
		ov := [4]float64{cur.InputPerM, cur.OutputPerM, cur.CachedInputPerM, cur.CacheWritePerM}
		manual := cur.Edited || (!cur.Builtin && cur.Source == "")
		if manual && !overwrite {
			plan.Kept++
			add(ImportChange{Action: "keep", Pattern: r.Pattern, Provider: r.Provider, Old: &ov, New: nv, Reason: "manual"})
			continue
		}
		if ov == nv && strings.EqualFold(cur.Currency, r.Currency) {
			plan.Same++
			continue
		}
		plan.Updated++
		add(ImportChange{Action: "update", Pattern: r.Pattern, Provider: r.Provider, Old: &ov, New: nv})
	}
	return plan, nil
}

// Apply writes the plan in one transaction and returns the same counts.
func (p *ImportPlan) Apply(db *gorm.DB) error {
	if p.catalog == nil {
		return errors.New("nothing to apply")
	}
	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
		var existing []model.ModelPrice
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		byKey := map[string]*model.ModelPrice{}
		for i := range existing {
			byKey[strings.ToLower(existing[i].Provider)+"|"+strings.ToLower(existing[i].Pattern)] = &existing[i]
		}
		for _, r := range p.catalog.Rows {
			cur, ok := byKey[strings.ToLower(r.Provider)+"|"+strings.ToLower(r.Pattern)]
			if !ok {
				row := model.ModelPrice{Pattern: r.Pattern, Provider: r.Provider, InputPerM: r.InputPerM, OutputPerM: r.OutputPerM,
					CachedInputPerM: r.CachedPerM, CacheWritePerM: r.WritePerM, Currency: r.Currency, Enabled: true, Note: r.Note,
					Source: p.Source, SourceDate: p.Date, UpdatedAt: now}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				continue
			}
			manual := cur.Edited || (!cur.Builtin && cur.Source == "")
			if manual && !p.overwrite {
				continue
			}
			upd := map[string]any{"input_per_m": r.InputPerM, "output_per_m": r.OutputPerM, "cached_input_per_m": r.CachedPerM,
				"cache_write_per_m": r.WritePerM, "currency": r.Currency, "note": r.Note, "source": p.Source, "source_date": p.Date,
				"edited": false, "updated_at": now}
			if err := tx.Model(&model.ModelPrice{}).Where("id = ?", cur.ID).Updates(upd).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Summary is a one-line description for logs and the UI.
func (p *ImportPlan) Summary() string {
	return fmt.Sprintf("source=%s date=%s total=%d new=%d updated=%d same=%d kept=%d skipped=%d", p.Source, p.Date, p.Total, p.New, p.Updated, p.Same, p.Kept, p.Skipped)
}
