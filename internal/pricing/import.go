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
// own shape (per 1M tokens, one row per provider + pattern). Every row has passed the
// same validation as a row typed into the price API (ValidateRow).
type Catalog struct {
	Source  string       `json:"source"` // "litellm" | "easycpa"
	Date    string       `json:"date"`   // the source's own date when it publishes one
	Rows    []CatalogRow `json:"rows"`
	Skipped int          `json:"skipped"` // entries the parser could not use (unknown provider, non-text mode, no prices)
	// Invalid counts entries that parsed but failed validation (negative or huge prices,
	// over-long names); InvalidRows lists the first few with the reason.
	Invalid     int      `json:"invalid"`
	InvalidRows []string `json:"invalid_rows,omitempty"`
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

// Limits shared by the price API and the importer.
const (
	MaxPatternLen  = 128
	MaxProviderLen = 32
	MaxPricePerM   = 1e6
)

// ValidateRow normalises a row (trimmed, lower-cased key, upper-cased currency) and
// returns a reason when it must not enter the table. The same rule guards manual rows.
func ValidateRow(r *CatalogRow) string {
	r.Pattern = strings.ToLower(strings.TrimSpace(r.Pattern))
	r.Provider = strings.ToLower(strings.TrimSpace(r.Provider))
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	if r.Pattern == "" || len(r.Pattern) > MaxPatternLen {
		return "模型名称（或前缀）不能为空，最长 128 字符"
	}
	if len(r.Provider) > MaxProviderLen {
		return "供应商最长 32 字符"
	}
	if r.Currency != "USD" && r.Currency != "CNY" {
		return "货币只支持 USD / CNY"
	}
	for _, v := range []float64{r.InputPerM, r.OutputPerM, r.CachedPerM, r.WritePerM} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > MaxPricePerM {
			return "单价范围 0 - 1000000（每百万 Token）"
		}
	}
	if len(r.Note) > 255 {
		r.Note = r.Note[:255]
	}
	return ""
}

const invalidRowsListed = 50

// add validates a row and files it as a row or an invalid entry.
func (c *Catalog) add(r CatalogRow) {
	if msg := ValidateRow(&r); msg != "" {
		c.Invalid++
		if len(c.InvalidRows) < invalidRowsListed {
			c.InvalidRows = append(c.InvalidRows, r.Pattern+": "+msg)
		}
		return
	}
	c.Rows = append(c.Rows, r)
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

type easyCPAEntry struct {
	ID       string   `json:"id"`
	Input    *float64 `json:"inputPer1M"`
	Output   *float64 `json:"outputPer1M"`
	CacheRd  *float64 `json:"cacheReadPer1M"`
	CacheCrt *float64 `json:"cacheCreationPer1M"`
}

// parseEasyCPA reads EasyCLIProxyAPI's model_prices.json. The published file keys
// "models" by model id ({"models":{"gpt-5.5":{...}}}); the older array form with an
// "id" field per entry is accepted too. Keys are processed in sorted order so the
// result does not depend on map iteration.
func parseEasyCPA(data []byte) (*Catalog, error) {
	var in struct {
		UpdatedAt string          `json:"updatedAt"`
		Models    json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	var entries []easyCPAEntry
	raw := strings.TrimSpace(string(in.Models))
	switch {
	case strings.HasPrefix(raw, "{"):
		var byID map[string]easyCPAEntry
		if err := json.Unmarshal(in.Models, &byID); err != nil {
			return nil, fmt.Errorf("models: %w", err)
		}
		ids := make([]string, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			e := byID[id]
			e.ID = id
			entries = append(entries, e)
		}
	case strings.HasPrefix(raw, "["):
		if err := json.Unmarshal(in.Models, &entries); err != nil {
			return nil, fmt.Errorf("models: %w", err)
		}
	default:
		return nil, errors.New("models must be an object keyed by model id or an array")
	}
	cat := &Catalog{Source: "easycpa", Date: in.UpdatedAt}
	seen := map[string]bool{}
	for _, m := range entries {
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if id == "" || m.Input == nil || m.Output == nil || seen[id] {
			cat.Skipped++
			continue
		}
		seen[id] = true
		row := CatalogRow{Pattern: id, Provider: "", InputPerM: *m.Input, OutputPerM: *m.Output, Currency: "USD", Note: "导入自 EasyCLIProxyAPI " + in.UpdatedAt}
		if m.CacheRd != nil {
			row.CachedPerM = *m.CacheRd
		}
		if m.CacheCrt != nil {
			row.WritePerM = *m.CacheCrt
		}
		cat.add(row)
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
		name = strings.ToLower(name)
		if name == "" || seen[prov+"|"+name] {
			cat.Skipped++
			continue
		}
		seen[prov+"|"+name] = true
		note := "导入自 LiteLLM"
		if e.Source != "" {
			note += " · " + e.Source
		}
		if len(note) > 250 {
			note = note[:250]
		}
		cat.add(CatalogRow{Pattern: name, Provider: prov, InputPerM: perM(e.In), OutputPerM: perM(e.Out),
			CachedPerM: perM(e.CacheRead), WritePerM: perM(e.CacheWrite), Currency: "USD", Note: note})
	}
	return cat, nil
}

// ImportChange is one planned row change.
type ImportChange struct {
	Action      string      `json:"action"` // new | update | keep
	Pattern     string      `json:"pattern"`
	Provider    string      `json:"provider"`
	Old         *[4]float64 `json:"old,omitempty"` // in, out, cached, write
	New         [4]float64  `json:"new"`
	OldCurrency string      `json:"old_currency,omitempty"`
	NewCurrency string      `json:"new_currency"`
	// CurrencyChanged marks an update or keep whose currency differs, e.g. a CNY
	// built-in row the catalog prices in USD.
	CurrencyChanged bool   `json:"currency_changed,omitempty"`
	Reason          string `json:"reason,omitempty"` // keep: "manual" | "currency"
}

// ImportOptions control which existing rows an import may replace.
type ImportOptions struct {
	// OverwriteEdited replaces rows an administrator created or edited by hand.
	OverwriteEdited bool `json:"overwrite_edited"`
	// OverwriteCurrency lets the import change a row's currency (CNY built-ins for the
	// Chinese vendors would otherwise be repriced in USD from an international list).
	OverwriteCurrency bool `json:"overwrite_currency"`
}

// ImportPlan is what an import would do; Apply writes it.
type ImportPlan struct {
	Source    string         `json:"source"`
	Date      string         `json:"date"`
	Total     int            `json:"total"`   // rows in the catalog
	New       int            `json:"new"`     // rows that do not exist yet
	Updated   int            `json:"updated"` // existing rows whose numbers or currency change
	Same      int            `json:"same"`    // existing rows already equal
	Kept      int            `json:"kept"`    // manual / edited / other-currency rows left alone
	Skipped   int            `json:"skipped"` // catalog entries the parser could not use
	Invalid   int            `json:"invalid"` // entries rejected by validation
	Invalids  []string       `json:"invalid_rows,omitempty"`
	Duplicate int            `json:"duplicates"` // existing duplicate rows that Apply removes
	Changes   []ImportChange `json:"changes"`    // new + update + kept (capped for the preview)
	opts      ImportOptions
	catalog   *Catalog
	drop      []uint // duplicate row ids to delete on apply
}

const importPreviewCap = 500

func priceKey(provider, pattern string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "|" + strings.ToLower(strings.TrimSpace(pattern))
}

// preferRow decides which of two rows with the same key survives a de-duplication:
// an edited row over an untouched one, a manual row over a built-in, then the older id.
func preferRow(a, b *model.ModelPrice) *model.ModelPrice {
	if a.Edited != b.Edited {
		if a.Edited {
			return a
		}
		return b
	}
	if a.Builtin != b.Builtin {
		if !a.Builtin {
			return a
		}
		return b
	}
	if a.ID < b.ID {
		return a
	}
	return b
}

// indexPrices maps the table by key and reports duplicate ids (the losers).
func indexPrices(rows []model.ModelPrice) (map[string]*model.ModelPrice, []uint) {
	byKey := map[string]*model.ModelPrice{}
	var drop []uint
	for i := range rows {
		k := priceKey(rows[i].Provider, rows[i].Pattern)
		cur, ok := byKey[k]
		if !ok {
			byKey[k] = &rows[i]
			continue
		}
		win := preferRow(cur, &rows[i])
		if win == cur {
			drop = append(drop, rows[i].ID)
		} else {
			drop = append(drop, cur.ID)
			byKey[k] = &rows[i]
		}
	}
	return byKey, drop
}

// PlanImport compares a catalog with the current table using default options: manual
// and edited rows are kept, and so are rows whose currency differs.
func PlanImport(db *gorm.DB, cat *Catalog, overwriteEdited bool) (*ImportPlan, error) {
	return PlanImportWith(db, cat, ImportOptions{OverwriteEdited: overwriteEdited})
}

// PlanImportWith compares a catalog with the current table. Rows an administrator
// created or edited by hand are kept unless OverwriteEdited; rows priced in another
// currency are kept unless OverwriteCurrency; built-in and previously imported rows are
// updated in place (their Builtin flag survives so "reset built-in" still works).
func PlanImportWith(db *gorm.DB, cat *Catalog, opts ImportOptions) (*ImportPlan, error) {
	var existing []model.ModelPrice
	if err := db.Order("id ASC").Find(&existing).Error; err != nil {
		return nil, err
	}
	byKey, drop := indexPrices(existing)
	plan := &ImportPlan{Source: cat.Source, Date: cat.Date, Total: len(cat.Rows), Skipped: cat.Skipped, Invalid: cat.Invalid, Invalids: cat.InvalidRows,
		Duplicate: len(drop), opts: opts, catalog: cat, drop: drop}
	// Updates and kept rows are what an operator must look at; new rows come after them
	// so a large catalog never pushes them past the preview cap.
	var news []ImportChange
	add := func(ch ImportChange) {
		if ch.Action == "new" {
			if len(news) < importPreviewCap {
				news = append(news, ch)
			}
			return
		}
		if len(plan.Changes) < importPreviewCap {
			plan.Changes = append(plan.Changes, ch)
		}
	}
	defer func() {
		for _, ch := range news {
			if len(plan.Changes) >= importPreviewCap {
				break
			}
			plan.Changes = append(plan.Changes, ch)
		}
	}()
	for _, r := range cat.Rows {
		nv := [4]float64{r.InputPerM, r.OutputPerM, r.CachedPerM, r.WritePerM}
		cur, ok := byKey[priceKey(r.Provider, r.Pattern)]
		if !ok {
			plan.New++
			add(ImportChange{Action: "new", Pattern: r.Pattern, Provider: r.Provider, New: nv, NewCurrency: r.Currency})
			continue
		}
		ov := [4]float64{cur.InputPerM, cur.OutputPerM, cur.CachedInputPerM, cur.CacheWritePerM}
		ch := ImportChange{Pattern: r.Pattern, Provider: r.Provider, Old: &ov, New: nv, OldCurrency: cur.Currency, NewCurrency: r.Currency,
			CurrencyChanged: !strings.EqualFold(cur.Currency, r.Currency)}
		switch action, reason := decide(cur, &r, opts); action {
		case "keep":
			plan.Kept++
			ch.Action, ch.Reason = "keep", reason
			add(ch)
		case "same":
			plan.Same++
		default:
			plan.Updated++
			ch.Action = "update"
			add(ch)
		}
	}
	return plan, nil
}

// decide classifies an existing row against its catalog row: keep (with reason), same
// or update.
func decide(cur *model.ModelPrice, r *CatalogRow, opts ImportOptions) (string, string) {
	manual := cur.Edited || (!cur.Builtin && cur.Source == "")
	if manual && !opts.OverwriteEdited {
		return "keep", "manual"
	}
	if !strings.EqualFold(cur.Currency, r.Currency) && !opts.OverwriteCurrency {
		return "keep", "currency"
	}
	ov := [4]float64{cur.InputPerM, cur.OutputPerM, cur.CachedInputPerM, cur.CacheWritePerM}
	nv := [4]float64{r.InputPerM, r.OutputPerM, r.CachedPerM, r.WritePerM}
	if ov == nv && strings.EqualFold(cur.Currency, r.Currency) {
		return "same", ""
	}
	return "update", ""
}

// Apply writes the catalog in one transaction. The plan is recomputed from the table as
// it is inside the transaction, so the counts left on p describe what was actually
// written even if the table changed since the preview. Duplicate rows sharing a key are
// collapsed to the preferred one.
func (p *ImportPlan) Apply(db *gorm.DB) error {
	if p.catalog == nil {
		return errors.New("nothing to apply")
	}
	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
		final, err := PlanImportWith(tx, p.catalog, p.opts)
		if err != nil {
			return err
		}
		if len(final.drop) > 0 {
			if err := tx.Where("id IN ?", final.drop).Delete(&model.ModelPrice{}).Error; err != nil {
				return err
			}
		}
		var existing []model.ModelPrice
		if err := tx.Order("id ASC").Find(&existing).Error; err != nil {
			return err
		}
		byKey, _ := indexPrices(existing)
		for _, r := range p.catalog.Rows {
			cur, ok := byKey[priceKey(r.Provider, r.Pattern)]
			if !ok {
				row := model.ModelPrice{Pattern: r.Pattern, Provider: r.Provider, InputPerM: r.InputPerM, OutputPerM: r.OutputPerM,
					CachedInputPerM: r.CachedPerM, CacheWritePerM: r.WritePerM, Currency: r.Currency, Enabled: true, Note: r.Note,
					Source: p.Source, SourceDate: p.Date, UpdatedAt: now}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				continue
			}
			if action, _ := decide(cur, &r, p.opts); action != "update" {
				continue
			}
			upd := map[string]any{"input_per_m": r.InputPerM, "output_per_m": r.OutputPerM, "cached_input_per_m": r.CachedPerM,
				"cache_write_per_m": r.WritePerM, "currency": r.Currency, "note": r.Note, "source": p.Source, "source_date": p.Date,
				"edited": false, "updated_at": now}
			if err := tx.Model(&model.ModelPrice{}).Where("id = ?", cur.ID).Updates(upd).Error; err != nil {
				return err
			}
		}
		*p = *final
		return nil
	})
}

// Summary is a one-line description for logs and the UI.
func (p *ImportPlan) Summary() string {
	return fmt.Sprintf("source=%s date=%s total=%d new=%d updated=%d same=%d kept=%d skipped=%d invalid=%d duplicates=%d",
		p.Source, p.Date, p.Total, p.New, p.Updated, p.Same, p.Kept, p.Skipped, p.Invalid, p.Duplicate)
}

// DedupePrices removes rows that share a (provider, pattern) key, keeping the preferred
// one, and lower-cases the key columns. It prepares a table for the unique index and is
// what Apply does for a table that predates it.
func DedupePrices(db *gorm.DB) (int64, error) {
	var removed int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var rows []model.ModelPrice
		if err := tx.Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		for i := range rows {
			lp, lpat := strings.ToLower(strings.TrimSpace(rows[i].Provider)), strings.ToLower(strings.TrimSpace(rows[i].Pattern))
			if lp != rows[i].Provider || lpat != rows[i].Pattern {
				if err := tx.Model(&model.ModelPrice{}).Where("id = ?", rows[i].ID).Updates(map[string]any{"provider": lp, "pattern": lpat}).Error; err != nil {
					return err
				}
				rows[i].Provider, rows[i].Pattern = lp, lpat
			}
		}
		_, drop := indexPrices(rows)
		if len(drop) == 0 {
			return nil
		}
		res := tx.Where("id IN ?", drop).Delete(&model.ModelPrice{})
		removed = res.RowsAffected
		return res.Error
	})
	return removed, err
}
