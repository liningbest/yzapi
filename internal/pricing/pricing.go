// Package pricing turns token usage into an estimated cost using a per-model price
// table. Prices are reference values that administrators are expected to review and
// override; a model without a matching row is reported as "unpriced", never as free.
package pricing

import (
	"math"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// BuiltinUpdated is the date the built-in table was last checked against provider
// price pages. Shown in the UI so operators know how stale the defaults may be.
const BuiltinUpdated = "2026-06"

// Builtin returns the reference price table (per 1M tokens, in the row's currency).
// Patterns match a model name exactly or as a dash-separated prefix ("gpt-5" matches
// "gpt-5-2026-01-01" but not "gpt-5-mini", which has its own row); the longest match wins.
func Builtin() []model.ModelPrice {
	usd := func(pattern, provider string, in, out, cached, cacheWrite float64) model.ModelPrice {
		return model.ModelPrice{Pattern: pattern, Provider: provider, InputPerM: in, OutputPerM: out, CachedInputPerM: cached, CacheWritePerM: cacheWrite, Currency: "USD", Builtin: true}
	}
	cny := func(pattern, provider string, in, out, cached float64) model.ModelPrice {
		return model.ModelPrice{Pattern: pattern, Provider: provider, InputPerM: in, OutputPerM: out, CachedInputPerM: cached, Currency: "CNY", Builtin: true}
	}
	return []model.ModelPrice{
		// OpenAI
		usd("gpt-5", "openai", 1.25, 10, 0.125, 0),
		usd("gpt-5-codex", "openai", 1.25, 10, 0.125, 0),
		usd("gpt-5-mini", "openai", 0.25, 2, 0.025, 0),
		usd("gpt-5-nano", "openai", 0.05, 0.4, 0.005, 0),
		usd("gpt-4.1", "openai", 2, 8, 0.5, 0),
		usd("gpt-4.1-mini", "openai", 0.4, 1.6, 0.1, 0),
		usd("gpt-4.1-nano", "openai", 0.1, 0.4, 0.025, 0),
		usd("gpt-4o", "openai", 2.5, 10, 1.25, 0),
		usd("gpt-4o-mini", "openai", 0.15, 0.6, 0.075, 0),
		usd("o3", "openai", 2, 8, 0.5, 0),
		usd("o3-pro", "openai", 20, 80, 0, 0),
		usd("o4-mini", "openai", 1.1, 4.4, 0.275, 0),
		usd("text-embedding-3-small", "openai", 0.02, 0, 0, 0),
		usd("text-embedding-3-large", "openai", 0.13, 0, 0, 0),
		// Anthropic (cache write is billed at 1.25x input)
		usd("claude-opus-4", "anthropic", 15, 75, 1.5, 18.75),
		usd("claude-opus-4-1", "anthropic", 15, 75, 1.5, 18.75),
		usd("claude-opus-4-5", "anthropic", 5, 25, 0.5, 6.25),
		usd("claude-sonnet-4", "anthropic", 3, 15, 0.3, 3.75),
		usd("claude-sonnet-4-5", "anthropic", 3, 15, 0.3, 3.75),
		usd("claude-3-7-sonnet", "anthropic", 3, 15, 0.3, 3.75),
		usd("claude-haiku-4-5", "anthropic", 1, 5, 0.1, 1.25),
		usd("claude-3-5-haiku", "anthropic", 0.8, 4, 0.08, 1),
		// Google
		usd("gemini-2.5-pro", "gemini", 1.25, 10, 0.31, 0),
		usd("gemini-2.5-flash", "gemini", 0.3, 2.5, 0.03, 0),
		usd("gemini-2.5-flash-lite", "gemini", 0.1, 0.4, 0.01, 0),
		usd("gemini-3-pro", "gemini", 2, 12, 0.2, 0),
		// xAI
		usd("grok-4", "xai", 3, 15, 0.75, 0),
		usd("grok-4-fast", "xai", 0.2, 0.5, 0.05, 0),
		usd("grok-code-fast-1", "xai", 0.2, 1.5, 0.02, 0),
		// DeepSeek (USD list price)
		usd("deepseek-chat", "deepseek", 0.28, 0.42, 0.028, 0),
		usd("deepseek-reasoner", "deepseek", 0.28, 0.42, 0.028, 0),
		// Moonshot (international list price)
		usd("kimi-k2", "moonshot", 0.6, 2.5, 0.15, 0),
		usd("kimi-k2-thinking", "moonshot", 0.6, 2.5, 0.15, 0),
		// MiniMax
		usd("MiniMax-M2", "minimax", 0.3, 1.2, 0.03, 0),
		// Zhipu (CNY)
		cny("glm-4.5", "zhipu", 2, 8, 0.4),
		cny("glm-4.6", "zhipu", 2, 8, 0.4),
		cny("glm-4.5-air", "zhipu", 0.8, 2, 0.16),
		cny("glm-4.5-flash", "zhipu", 0, 0, 0),
		// Alibaba DashScope (CNY, lowest context tier)
		cny("qwen3-coder-plus", "aliyun", 4, 16, 0.8),
		cny("qwen3-coder-flash", "aliyun", 1, 4, 0.2),
		cny("qwen-max", "aliyun", 2.4, 9.6, 0.48),
		cny("qwen-plus", "aliyun", 0.8, 2, 0.16),
		cny("qwen-turbo", "aliyun", 0.3, 0.6, 0.06),
		// Volcengine Doubao (CNY, lowest context tier)
		cny("doubao-seed-1.6", "volcengine", 0.8, 8, 0.16),
		cny("doubao-seed-1.6-flash", "volcengine", 0.15, 1.5, 0.03),
	}
}

// Seed inserts built-in rows whose pattern is not present yet. Existing rows, including
// edited built-ins, are never touched.
func Seed(db *gorm.DB) error {
	var existing []model.ModelPrice
	if err := db.Select("pattern", "provider").Find(&existing).Error; err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[strings.ToLower(p.Provider)+"|"+strings.ToLower(p.Pattern)] = true
	}
	for _, p := range Builtin() {
		if have[strings.ToLower(p.Provider)+"|"+strings.ToLower(p.Pattern)] {
			continue
		}
		p.UpdatedAt = time.Now()
		if err := db.Create(&p).Error; err != nil {
			return err
		}
	}
	return nil
}

// ResetBuiltin restores every built-in row to the reference values and removes
// built-in rows that no longer exist; custom rows are kept.
func ResetBuiltin(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("builtin = ?", true).Delete(&model.ModelPrice{}).Error; err != nil {
			return err
		}
		for _, p := range Builtin() {
			p.UpdatedAt = time.Now()
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Service resolves prices from an in-memory copy of the table.
type Service struct {
	db *gorm.DB
	st *settings.Store
	mu sync.RWMutex
	// byProvider[provider] and byProvider[""] (provider-agnostic rows)
	byProvider map[string][]model.ModelPrice
}

func New(db *gorm.DB, st *settings.Store) (*Service, error) {
	s := &Service{db: db, st: st}
	return s, s.Reload()
}

func (s *Service) Reload() error {
	var rows []model.ModelPrice
	if err := s.db.Where("enabled = ?", true).Find(&rows).Error; err != nil {
		return err
	}
	m := map[string][]model.ModelPrice{}
	for _, r := range rows {
		k := strings.ToLower(r.Provider)
		m[k] = append(m[k], r)
	}
	s.mu.Lock()
	s.byProvider = m
	s.mu.Unlock()
	return nil
}

// Lookup returns the best price row for a model: provider-specific rows first, then
// provider-agnostic ones; exact name beats prefix, longer prefix beats shorter.
func (s *Service) Lookup(provider, modelName string) (model.ModelPrice, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name := strings.ToLower(strings.TrimSpace(modelName))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:] // vendor prefixes such as "anthropic/claude-..."
	}
	// Provider-specific rows first, then provider-agnostic rows, then any provider's
	// row: a custom relay serving "claude-sonnet-4-5" should still price as Anthropic.
	for _, key := range []string{strings.ToLower(provider), "", "*"} {
		var rows []model.ModelPrice
		if key == "*" {
			for _, rs := range s.byProvider {
				rows = append(rows, rs...)
			}
		} else {
			rows = s.byProvider[key]
		}
		var best *model.ModelPrice
		bestLen := -1
		for i := range rows {
			r := &rows[i]
			pat := strings.ToLower(r.Pattern)
			if name == pat || strings.HasPrefix(name, pat+"-") || strings.HasPrefix(name, pat+":") || strings.HasPrefix(name, pat+"@") {
				if len(pat) > bestLen {
					best, bestLen = r, len(pat)
				}
			}
		}
		if best != nil {
			return *best, true
		}
	}
	return model.ModelPrice{}, false
}

// Ledger is the immutable currency every stored cost is denominated in. Costs are
// converted into it when a request is priced and out of it into the display currency
// when reported, so changing the display currency or the rate never relabels history.
const Ledger = "USD"

// Cost returns the estimated cost in ledger micro-units (1e-6 USD) and whether a price
// was found. cached and cacheWrite are disjoint parts of prompt: read from the prompt
// cache (cheaper) and written into it (Anthropic bills a premium; a row without a
// cache-write price falls back to the ordinary input price).
func (s *Service) Cost(provider, modelName string, prompt, completion, cached, cacheWrite int64) (int64, bool) {
	p, ok := s.Lookup(provider, modelName)
	if !ok {
		return 0, false
	}
	if cached > prompt {
		cached = prompt
	}
	if cacheWrite > prompt-cached {
		cacheWrite = prompt - cached
	}
	writePrice := p.CacheWritePerM
	if writePrice <= 0 {
		writePrice = p.InputPerM
	}
	plain := prompt - cached - cacheWrite
	amount := (float64(plain)*p.InputPerM + float64(cached)*p.CachedInputPerM + float64(cacheWrite)*writePrice + float64(completion)*p.OutputPerM) / 1e6
	return int64(math.Round(s.toLedger(amount, p.Currency) * 1e6)), true
}

func (s *Service) rate() float64 {
	if r := s.st.Get().Pricing.USDToCNY; r > 0 {
		return r
	}
	return 7.2
}

// toLedger moves an amount from a price row's currency into the ledger currency.
func (s *Service) toLedger(amount float64, from string) float64 {
	if strings.ToUpper(from) == "CNY" {
		return amount / s.rate()
	}
	return amount
}

// Display converts ledger micro-units into the display currency as a float amount.
func (s *Service) Display(micros int64) float64 {
	amount := float64(micros) / 1e6
	if s.Currency() == "CNY" {
		return amount * s.rate()
	}
	return amount
}

// Currency is the display currency costs are reported in (CNY or USD).
func (s *Service) Currency() string {
	c := strings.ToUpper(s.st.Get().Pricing.Currency)
	if c == "" {
		return Ledger
	}
	return c
}

// ledgerMarker records that stored costs are denominated in the ledger currency.
const ledgerMarker = "cost_ledger"

// MigrateLedger converts costs written by 1.0.12 builds that stored amounts in the
// then-configured base currency into the fixed USD ledger. It runs once: the marker
// row is written afterwards, so later changes of the display currency never touch
// stored values. Attempt-level amounts inside call_logs.attempts are left as written.
func MigrateLedger(db *gorm.DB, st *settings.Store) error {
	var row model.Setting
	if err := db.Where("key = ?", ledgerMarker).First(&row).Error; err == nil {
		return nil
	}
	pr := st.Get().Pricing
	if strings.ToUpper(pr.Currency) == "CNY" {
		rate := pr.USDToCNY
		if rate <= 0 {
			rate = 7.2
		}
		for _, table := range []string{"call_logs", "usage_hourlies"} {
			if err := db.Exec("UPDATE "+table+" SET cost_micros = CAST(ROUND(cost_micros / ?) AS INTEGER) WHERE cost_micros <> 0", rate).Error; err != nil {
				return err
			}
		}
	}
	return db.Save(&model.Setting{Key: ledgerMarker, Value: Ledger, UpdatedAt: time.Now()}).Error
}
