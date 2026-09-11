// Package pricing turns token usage into an estimated cost using a per-model price
// table. Prices are reference values that administrators are expected to review and
// override; a model without a matching row is reported as "unpriced", never as free.
package pricing

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// BuiltinUpdated is the date the built-in table was last checked against provider
// price pages. Shown in the UI so operators know how stale the defaults may be.
const BuiltinUpdated = "2026-09"

// Builtin returns the reference price table (per 1M tokens, in the row's currency),
// checked against the providers' own price pages on 2026-09-12 (sources listed in
// docs/pricing-sources-2026-09.md). Patterns match a model name exactly or as a
// dash / colon / at-separated prefix ("gpt-5" matches "gpt-5-2026-01-01" but not
// "gpt-5-mini" or "gpt-5.5", which have their own rows); the longest match wins.
//
// The table holds one price per row. Where a provider tiers by context length the row
// carries the lowest tier and says so in Note; where a page quotes a promotional price
// the row carries the price actually billed today and Note gives the list price. Rows
// kept from the 2026-06 table that no longer appear on a price page are marked as
// unverified in Note rather than dropped, so existing routes stay priced.
func Builtin() []model.ModelPrice {
	usd := func(pattern, provider string, in, out, cached, cacheWrite float64, note string) model.ModelPrice {
		return model.ModelPrice{Pattern: pattern, Provider: provider, InputPerM: in, OutputPerM: out, CachedInputPerM: cached, CacheWritePerM: cacheWrite, Currency: "USD", Builtin: true, Note: note}
	}
	cny := func(pattern, provider string, in, out, cached float64, note string) model.ModelPrice {
		return model.ModelPrice{Pattern: pattern, Provider: provider, InputPerM: in, OutputPerM: out, CachedInputPerM: cached, Currency: "CNY", Builtin: true, Note: note}
	}
	const old = "2026-06 参考价，2026-09 复核时官网已不再列出，未复核"
	return []model.ModelPrice{
		// OpenAI — developers.openai.com/api/docs/pricing, standard tier
		usd("gpt-6-astra", "openai", 10, 50, 1, 0, ""),
		usd("gpt-5.6-sol", "openai", 4, 20, 0.4, 0, "促销价（至 2026-11-21）"),
		usd("gpt-5.6-terra", "openai", 2, 12, 0.2, 0, ""),
		usd("gpt-5.6-luna", "openai", 0.2, 1.2, 0.02, 0, ""),
		usd("gpt-5.5", "openai", 5, 30, 0.5, 0, ""),
		usd("gpt-5.5-pro", "openai", 30, 180, 0, 0, "不支持缓存折扣"),
		usd("gpt-5.4", "openai", 2.5, 15, 0.25, 0, ""),
		usd("gpt-5.4-mini", "openai", 0.75, 4.5, 0.075, 0, ""),
		usd("gpt-5.4-nano", "openai", 0.2, 1.25, 0.02, 0, ""),
		usd("gpt-5.4-pro", "openai", 30, 180, 0, 0, "不支持缓存折扣"),
		usd("gpt-5.3-codex", "openai", 1.75, 14, 0.175, 0, ""),
		usd("gpt-5.3-codex-spark", "openai", 1.75, 14, 0.175, 0, "官网无公开 API 价，按 gpt-5.3-codex 同价"),
		usd("gpt-5.2", "openai", 1.75, 14, 0.175, 0, ""),
		usd("gpt-5.2-pro", "openai", 21, 168, 0, 0, "不支持缓存折扣"),
		usd("gpt-5.1", "openai", 1.25, 10, 0.125, 0, ""),
		usd("gpt-5", "openai", 1.25, 10, 0.125, 0, ""),
		usd("gpt-5-mini", "openai", 0.25, 2, 0.025, 0, ""),
		usd("gpt-5-nano", "openai", 0.05, 0.4, 0.005, 0, ""),
		usd("gpt-5-pro", "openai", 15, 120, 0, 0, "不支持缓存折扣"),
		usd("gpt-5-codex", "openai", 1.25, 10, 0.125, 0, old),
		usd("o3", "openai", 2, 8, 0.5, 0, ""),
		usd("o3-pro", "openai", 20, 80, 0, 0, ""),
		usd("o3-mini", "openai", 1.1, 4.4, 0.55, 0, ""),
		usd("o4-mini", "openai", 1.1, 4.4, 0.275, 0, ""),
		usd("o1", "openai", 15, 60, 7.5, 0, ""),
		usd("gpt-4o", "openai", 2.5, 10, 1.25, 0, ""),
		usd("gpt-4o-mini", "openai", 0.15, 0.6, 0.075, 0, ""),
		usd("gpt-4.1", "openai", 2, 8, 0.5, 0, old),
		usd("gpt-4.1-mini", "openai", 0.4, 1.6, 0.1, 0, old),
		usd("gpt-4.1-nano", "openai", 0.1, 0.4, 0.025, 0, old),
		usd("text-embedding-3-small", "openai", 0.02, 0, 0, 0, ""),
		usd("text-embedding-3-large", "openai", 0.13, 0, 0, 0, ""),
		// Anthropic — platform.claude.com/docs/en/about-claude/pricing (cache write = 5-minute write, 1.25x input)
		usd("claude-fable-5-1", "anthropic", 10, 50, 0.25, 12.5, "缓存读为输入价的 2.5%"),
		usd("claude-fable-5", "anthropic", 10, 50, 1, 12.5, ""),
		usd("claude-mythos-5-1", "anthropic", 10, 50, 0.25, 12.5, "限定客户可用"),
		usd("claude-mythos-5", "anthropic", 10, 50, 1, 12.5, "限定客户可用"),
		usd("claude-opus-5", "anthropic", 5, 25, 0.5, 6.25, ""),
		usd("claude-opus-4-8", "anthropic", 5, 25, 0.5, 6.25, ""),
		usd("claude-opus-4-7", "anthropic", 5, 25, 0.5, 6.25, ""),
		usd("claude-opus-4-6", "anthropic", 5, 25, 0.5, 6.25, ""),
		usd("claude-opus-4-5", "anthropic", 5, 25, 0.5, 6.25, ""),
		usd("claude-opus-4-1", "anthropic", 15, 75, 1.5, 18.75, "已退役（Bedrock / Google Cloud 仍可用）"),
		usd("claude-opus-4", "anthropic", 15, 75, 1.5, 18.75, "已退役（Google Cloud 仍可用）"),
		usd("claude-sonnet-5", "anthropic", 2, 10, 0.2, 2.5, "官网已确认 $2/$10 为正式价"),
		usd("claude-sonnet-4-6", "anthropic", 3, 15, 0.3, 3.75, ""),
		usd("claude-sonnet-4-5", "anthropic", 3, 15, 0.3, 3.75, ""),
		usd("claude-sonnet-4", "anthropic", 3, 15, 0.3, 3.75, "已退役（Bedrock / Google Cloud 仍可用）"),
		usd("claude-3-7-sonnet", "anthropic", 3, 15, 0.3, 3.75, old),
		usd("claude-haiku-4-5", "anthropic", 1, 5, 0.1, 1.25, ""),
		usd("claude-3-5-haiku", "anthropic", 0.8, 4, 0.08, 1, "已退役（Bedrock / Google Cloud 仍可用）"),
		// Google — ai.google.dev/gemini-api/docs/pricing, paid tier, text input; prices valid through 2026-12-31
		usd("gemini-3.8-flash", "gemini", 0.75, 3.75, 0.075, 0, ""),
		usd("gemini-3.7-flash", "gemini", 0.75, 3.75, 0.075, 0, ""),
		usd("gemini-3.6-flash", "gemini", 0.75, 3.75, 0.075, 0, ""),
		usd("gemini-3.5-flash", "gemini", 1.5, 9, 0.15, 0, ""),
		usd("gemini-3.5-flash-lite", "gemini", 0.3, 2.5, 0.03, 0, ""),
		usd("gemini-3.1-flash-lite", "gemini", 0.25, 1.5, 0.025, 0, "音频输入另计"),
		usd("gemini-3.1-pro", "gemini", 2, 12, 0.2, 0, "≤200K 档；>200K 为 $4 / $18 / $0.40"),
		usd("gemini-3-pro", "gemini", 2, 12, 0.2, 0, old),
		usd("gemini-2.5-pro", "gemini", 1.25, 10, 0.125, 0, "≤200K 档；>200K 为 $2.50 / $15 / $0.25"),
		usd("gemini-2.5-flash", "gemini", 0.3, 2.5, 0.03, 0, "音频输入另计"),
		usd("gemini-2.5-flash-lite", "gemini", 0.1, 0.4, 0.01, 0, "音频输入另计"),
		// xAI — docs.x.ai/docs/models, <200K prompt tier (≥200K doubles input and output)
		usd("grok-4.6", "xai", 2, 6, 0.5, 0, "<200K 档；≥200K 全部按 2 倍"),
		usd("grok-4.5", "xai", 2, 6, 0.3, 0, "<200K 档；≥200K 全部按 2 倍"),
		usd("grok-4.3", "xai", 1.25, 2.5, 0.2, 0, "<200K 档；≥200K 全部按 2 倍"),
		usd("grok-4.20", "xai", 1.25, 2.5, 0.2, 0, "含 -reasoning / -non-reasoning / multi-agent；<200K 档"),
		usd("grok-build", "xai", 1, 2, 0.2, 0, "<200K 档；≥200K 全部按 2 倍"),
		usd("grok-4", "xai", 3, 15, 0.75, 0, old),
		usd("grok-4-fast", "xai", 0.2, 0.5, 0.05, 0, old),
		usd("grok-code-fast-1", "xai", 0.2, 1.5, 0.02, 0, old),
		// DeepSeek — api-docs.deepseek.com/quick_start/pricing, peak rate (off-peak 01:00–04:00 / 06:00–10:00 UTC weekdays is half)
		usd("deepseek-v4-pro", "deepseek", 1.32, 3.96, 0.044, 0, "高峰价；非高峰时段减半"),
		usd("deepseek-flash", "deepseek", 0.3, 1.2, 0.006, 0, "高峰价；非高峰时段减半"),
		usd("deepseek-v4-flash", "deepseek", 0.3, 1.2, 0.006, 0, "旧名，路由到 deepseek-flash"),
		usd("deepseek-chat", "deepseek", 0.28, 0.42, 0.028, 0, old),
		usd("deepseek-reasoner", "deepseek", 0.28, 0.42, 0.028, 0, old),
		// Moonshot — platform.kimi.com/docs/pricing/chat (CNY)
		cny("kimi-k3", "moonshot", 20, 100, 2, ""),
		cny("kimi-k2.7-code", "moonshot", 6.5, 27, 1.3, ""),
		cny("kimi-k2.7-code-highspeed", "moonshot", 13, 54, 2.6, ""),
		cny("kimi-k2.6", "moonshot", 6.5, 27, 1.1, ""),
		usd("kimi-k2", "moonshot", 0.6, 2.5, 0.15, 0, old),
		usd("kimi-k2-thinking", "moonshot", 0.6, 2.5, 0.15, 0, old),
		// MiniMax — platform.minimax.cn/docs/guides/pricing-paygo (CNY, standard tier)
		cny("MiniMax-M3", "minimax", 2.1, 8.4, 0.42, "永久五折后价；≤512K 档，>512K 翻倍；priority 档 1.5 倍"),
		cny("MiniMax-M2.7", "minimax", 2.1, 8.4, 0.42, "缓存写 ¥2.625"),
		cny("MiniMax-M2.7-highspeed", "minimax", 4.2, 16.8, 0.42, ""),
		usd("MiniMax-M2", "minimax", 0.3, 1.2, 0.03, 0, old),
		// Zhipu — docs.bigmodel.cn/cn/guide/start/pricing (CNY, lowest tier)
		cny("glm-5.3", "zhipu", 8, 28, 2, ""),
		cny("glm-5.3-flash", "zhipu", 0.8, 2.8, 0.23, ""),
		cny("glm-5.2", "zhipu", 8, 28, 2, ""),
		cny("glm-5.1", "zhipu", 6, 24, 1.3, "输入 <32K 档；≥32K 为 8 / 28 / 2"),
		cny("glm-5-turbo", "zhipu", 5, 22, 1.2, "输入 <32K 档；≥32K 为 7 / 26 / 1.8"),
		cny("glm-5", "zhipu", 4, 18, 1, "输入 <32K 档；≥32K 为 6 / 22 / 1.5"),
		cny("glm-4.7", "zhipu", 2, 8, 0.4, "输入 <32K 且输出 <0.2K 档；更长为 3 / 14 或 4 / 16"),
		cny("glm-4.7-flashx", "zhipu", 0.5, 3, 0.1, ""),
		cny("glm-4.7-flash", "zhipu", 0, 0, 0, "免费"),
		cny("glm-4.5-air", "zhipu", 0.8, 2, 0.16, "输入 <32K 且输出 <0.2K 档；更长为 0.8 / 6 或 1.2 / 8"),
		cny("glm-4.6", "zhipu", 2, 8, 0.4, old),
		cny("glm-4.5", "zhipu", 2, 8, 0.4, old),
		cny("glm-4.5-flash", "zhipu", 0, 0, 0, old),
		// Alibaba Model Studio — help.aliyun.com/zh/model-studio/model-pricing (CNY, China region, lowest tier, list price)
		cny("qwen3.8-max", "aliyun", 12, 36, 1.2, ""),
		cny("qwen3.7-max", "aliyun", 12, 36, 1.2, "官网当前 5 折促销 6 / 18"),
		cny("qwen3.7-plus", "aliyun", 2, 8, 0.2, "牌价；官网当前 8 折 1.6 / 6.4；≤256K 档"),
		cny("qwen3.8-flash", "aliyun", 0.8, 2.7, 0.08, ""),
		cny("qwen3-max", "aliyun", 2.5, 10, 0.25, "≤32K 档，更长上下文按更高档"),
		cny("qwen3-coder-plus", "aliyun", 4, 16, 0.4, "≤32K 档，更长上下文按更高档"),
		cny("qwen-plus", "aliyun", 0.8, 2, 0.08, "≤128K 档，更长上下文按更高档"),
		cny("qwen-turbo", "aliyun", 0.3, 0.6, 0, "思考模式输出 ¥3"),
		cny("qwen-long", "aliyun", 0.5, 2, 0, ""),
		cny("qwen3-coder-flash", "aliyun", 1, 4, 0.2, old),
		cny("qwen-max", "aliyun", 2.4, 9.6, 0.48, old),
		// Volcengine Doubao — ai.volcengine.com/model and the 2.0 launch page (CNY, ≤32K tier)
		cny("doubao-seed-2-1-pro", "volcengine", 6, 30, 1.2, "缓存价按 2.0 系列 20% 比例推算，官网未列"),
		cny("doubao-seed-2-1-turbo", "volcengine", 3, 15, 0.6, "缓存价按 2.0 系列 20% 比例推算，官网未列"),
		cny("doubao-seed-2-0-pro", "volcengine", 3.2, 16, 0.64, "≤32K 档，更长上下文按更高档"),
		cny("doubao-seed-2-0-lite", "volcengine", 0.6, 3.6, 0.12, "≤32K 档；最高档 1.8 / 10.8"),
		cny("doubao-seed-2-0-mini", "volcengine", 0.2, 2, 0.04, "≤32K 档；最高档 0.8 / 8"),
		cny("doubao-seed-evolving", "volcengine", 6, 30, 1.2, "缓存价按 2.0 系列 20% 比例推算，官网未列"),
		cny("doubao-seed-1.8", "volcengine", 0.8, 8, 0.16, "≤16K 档；缓存价按比例推算"),
		cny("doubao-seed-1.6", "volcengine", 0.8, 8, 0.16, old),
		cny("doubao-seed-1.6-flash", "volcengine", 0.15, 1.5, 0.03, old),
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
const Ledger = model.CostLedgerUSD

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

// Ledger migration (R112-02, R113-01, R113-02, R114-01, R114-02).
//
// Every row written by 1.0.14+ carries cost_ledger="USD". Rows without a stamp were
// written by an earlier build and mean different things depending on which build it was:
//
//	origin v0 (no marker):     1.0.12 rows, request and attempt amounts in the then-display currency
//	origin v1 (marker "USD"):  1.0.13 ran: old rows have the request amount in USD but the
//	                           attempts still in the display currency; rows created after the
//	                           marker were priced by 1.0.13 in USD on both sides
//	origin v2 (marker usd-v2): an early 1.0.14 whose startup replay bypassed the fixer
//
// Each row is classified from its own evidence (stamp, request-vs-attempts ratio,
// creation time against the marker time) and converted at most once inside one
// transaction with the version marker; rows that cannot be classified are stamped
// "unverified" and reported, never divided on a guess. Journal records committed later
// go through LegacyCostFixer, which applies the same origin rule.
const (
	ledgerMarker    = "cost_ledger"
	ledgerOriginKey = "cost_ledger_origin" // which build the database came from when first migrated
	ledgerVersion   = "usd-v4"
	ledgerV3        = "usd-v3"    // 1.0.15: rows marked unverified but their amounts left in the hourly rollup
	ledgerMigrating = "migrating" // placeholder held inside the migration transaction

	LedgerUnverified = model.CostLedgerUnverified // stamp for rows whose currency could not be established
)

var errLedgerDone = errors.New("ledger migration already applied")

// ledgerOriginOf maps the marker found at startup to the origin build.
func ledgerOriginOf(marker string, found bool) string {
	switch {
	case !found:
		return "v0"
	case marker == "USD":
		return "v1"
	case marker == "usd-v2":
		return "v2"
	}
	return "v3"
}

// MigrateLedger converts stored costs into the USD ledger. Safe to call on every start.
func MigrateLedger(db *gorm.DB, st *settings.Store) error {
	var row model.Setting
	err := db.Where("key = ?", ledgerMarker).First(&row).Error
	switch {
	case err == nil && row.Value == ledgerVersion:
		return nil
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		return err // a read failure must not be mistaken for "not migrated"
	}
	found := err == nil
	origin := ledgerOriginOf(row.Value, found)
	if found && row.Value == ledgerV3 {
		// Only 1.0.15+ recorded the origin; for earlier markers the marker itself says
		// where the database comes from, whatever an origin row (e.g. from a test
		// fixture that rewound the marker) may claim.
		if o, e := readSetting(db, ledgerOriginKey); e == nil && o != "" {
			origin = o
		}
	}
	repairV3 := found && row.Value == ledgerV3
	markerTime := row.UpdatedAt
	pr := st.Get().Pricing
	rate := pr.USDToCNY
	if rate <= 0 {
		rate = 7.2
	}
	divide := strings.ToUpper(pr.Currency) == "CNY"
	var unverified, unsettled int64
	txErr := db.Transaction(func(tx *gorm.DB) error {
		// Claim the marker first: a second instance racing on the same database either
		// blocks on the row and then finds it claimed, or fails the insert.
		if found {
			res := tx.Model(&model.Setting{}).Where("key = ? AND value NOT IN ?", ledgerMarker, []string{ledgerVersion, ledgerMigrating}).Update("value", ledgerMigrating)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errLedgerDone
			}
		} else if err := tx.Create(&model.Setting{Key: ledgerMarker, Value: ledgerMigrating, UpdatedAt: time.Now()}).Error; err != nil {
			return err
		}
		// The origin is recorded once, on the first migration of this database.
		var o model.Setting
		if e := tx.Where("key = ?", ledgerOriginKey).First(&o).Error; errors.Is(e, gorm.ErrRecordNotFound) {
			if err := tx.Create(&model.Setting{Key: ledgerOriginKey, Value: origin, UpdatedAt: time.Now()}).Error; err != nil {
				return err
			}
		} else if e != nil {
			return e
		}
		// Hourly rows carry no per-row stamp; they are converted exactly once: on the very
		// first migration of a database that has no marker at all (a 1.0.12 database).
		// Any later pass, whatever the recorded origin, finds them already in USD (R117-01).
		if divide && !found {
			if err := tx.Exec("UPDATE usage_hourlies SET cost_micros = CAST(ROUND(cost_micros / ?) AS INTEGER) WHERE cost_micros <> 0", rate).Error; err != nil {
				return err
			}
		}
		// 1.0.15 marked rows unverified without taking their amounts out of the hourly
		// rollup (R116-01). Those rows are still recognisable by their stamp; repair them
		// once, inside this transaction, before the marker moves past v3.
		// 1.0.15 and 1.0.16 both wrote usd-v3. 1.0.16 already took the amounts out and set
		// cost_known=false on the rows it processed; 1.0.15 left cost_known untouched (true
		// for a priced row). So cost_known=true is the per-row evidence of "not yet taken
		// out"; a row 1.0.16 processed, or one that was never known, is left alone.
		if repairV3 {
			// Counted before this pass touches anything, so the rows it settles (which end
			// up cost_known=false themselves) are not reported as unsettled (R118-01).
			if err := tx.Model(&model.CallLog{}).Where("cost_ledger = ? AND cost_known = ? AND cost_micros <> 0", LedgerUnverified, false).Count(&unsettled).Error; err != nil {
				return err
			}
			lastID := uint(0)
			for {
				var rows []model.CallLog
				if err := tx.Where("id > ? AND cost_ledger = ? AND cost_known = ?", lastID, LedgerUnverified, true).Order("id").Limit(500).Find(&rows).Error; err != nil {
					return err
				}
				if len(rows) == 0 {
					break
				}
				for i := range rows {
					r := &rows[i]
					if err := unbookUnverified(tx, r, origin == "v1"); err != nil {
						return err
					}
					if err := tx.Model(&model.CallLog{}).Where("id = ?", r.ID).Update("cost_known", false).Error; err != nil {
						return err
					}
				}
				lastID = rows[len(rows)-1].ID
			}
			// Rows that were already cost_known=false when this pass started carry no
			// evidence either way (1.0.15 marked them while partially priced, or 1.0.16
			// already took them out); they are not touched, only reported above.
		}
		// In a repair pass, a row still without a stamp has no known history: never guess.
		classifyOrigin := origin
		if repairV3 {
			classifyOrigin = "v2"
		}
		lastID := uint(0)
		for {
			var rows []model.CallLog
			if err := tx.Where("id > ? AND (cost_ledger IS NULL OR cost_ledger = '')", lastID).
				Order("id").Limit(500).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			for i := range rows {
				r := &rows[i]
				micros, att, stamp := classifyLegacyRow(r, classifyOrigin, markerTime, rate, divide)
				upd := map[string]any{"cost_micros": micros, "cost_ledger": stamp}
				if stamp == LedgerUnverified {
					unverified++
					// Its amounts were booked into the hourly rollup as if they were USD
					// (origin v2 replayed them without a fixer): take them back out and
					// count the request as unverified there too.
					if err := unbookUnverified(tx, r, origin == "v1"); err != nil {
						return err
					}
					upd["cost_known"] = false
				}
				if string(att) != string(r.Attempts) {
					upd["attempts"] = att
				}
				if err := tx.Model(&model.CallLog{}).Where("id = ?", r.ID).Updates(upd).Error; err != nil {
					return err
				}
			}
			lastID = rows[len(rows)-1].ID
		}
		return tx.Model(&model.Setting{}).Where("key = ?", ledgerMarker).Updates(map[string]any{"value": ledgerVersion, "updated_at": time.Now()}).Error
	})
	if errors.Is(txErr, errLedgerDone) {
		return nil
	}
	if txErr != nil {
		// The claim may have failed because another instance already migrated.
		var again model.Setting
		if e := db.Where("key = ?", ledgerMarker).First(&again).Error; e == nil && again.Value == ledgerVersion {
			return nil
		}
		return txErr
	}
	if unverified > 0 {
		slog.Warn("cost ledger migration: rows whose currency could not be established were left as written", "rows", unverified, "stamp", LedgerUnverified)
	}
	if unsettled > 0 {
		slog.Warn("cost ledger migration: unverified rows with no evidence either way about the hourly rollup; reconcile the retention window and rebuild only if it reports differences", "rows", unsettled)
	}
	return nil
}

// classifyLegacyRow decides what an unstamped row holds and returns the request amount,
// the attempts JSON and the stamp to write. It never divides on a guess.
func classifyLegacyRow(r *model.CallLog, origin string, markerTime time.Time, rate float64, divide bool) (int64, model.JSON, string) {
	sum, priced := attemptsCostSum(r.Attempts)
	switch origin {
	case "v0":
		// Everything is in the display currency.
		if !divide {
			return r.CostMicros, r.Attempts, Ledger
		}
		att, _ := scaleAttempts(r.Attempts, 1/rate)
		return int64(math.Round(float64(r.CostMicros) / rate)), att, Ledger
	case "v1":
		if rate < 4 && priced && sum > 0 && r.CreatedAt.Before(markerTime) {
			// The ratio rule cannot separate "attempts still in CNY" from "consistent"
			// when the rate is this small; do not guess.
			return r.CostMicros, r.Attempts, LedgerUnverified
		}
		switch {
		case r.CostMicros > 0 && priced && float64(sum)/float64(r.CostMicros) > 3:
			// Migrated by 1.0.13: request already USD, attempts still in the display
			// currency. The request amount is the trustworthy side; scale the attempts
			// onto it (independent of whatever rate 1.0.13 used).
			att, _ := scaleAttempts(r.Attempts, float64(r.CostMicros)/float64(sum))
			return r.CostMicros, att, Ledger
		case priced && sum > 0 && r.CreatedAt.Before(markerTime) && divide:
			// Consistent on both sides but older than the 1.0.13 migration: a 1.0.12 record
			// replayed from the journal after that migration, still in the display currency.
			att, _ := scaleAttempts(r.Attempts, 1/rate)
			return int64(math.Round(float64(r.CostMicros) / rate)), att, Ledger
		default:
			// Priced by 1.0.13 in USD on both sides (or nothing to convert).
			return r.CostMicros, r.Attempts, Ledger
		}
	case "v2":
		// Rows an early 1.0.14 replayed without the fixer: they may be 1.0.13 USD records or
		// 1.0.12 display-currency records and nothing in the row tells which.
		if r.CostMicros == 0 && !priced {
			return r.CostMicros, r.Attempts, Ledger
		}
		return r.CostMicros, r.Attempts, LedgerUnverified
	}
	return r.CostMicros, r.Attempts, Ledger
}

// attemptsCostSum sums the attempts' cost_micros; priced reports whether any attempt carried one.
func attemptsCostSum(raw model.JSON) (sum int64, priced bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var atts []map[string]any
	if err := json.Unmarshal([]byte(raw), &atts); err != nil {
		return 0, false
	}
	for _, a := range atts {
		if v, ok := a["cost_micros"].(float64); ok && v != 0 {
			sum += int64(v)
			priced = true
		}
	}
	return sum, priced
}

// scaleAttempts multiplies every attempt's cost_micros by factor and reports whether the
// JSON changed. Unknown shapes are left untouched.
func scaleAttempts(raw model.JSON, factor float64) (model.JSON, bool) {
	if len(raw) == 0 || factor == 1 {
		return raw, false
	}
	var atts []map[string]any
	if err := json.Unmarshal([]byte(raw), &atts); err != nil || len(atts) == 0 {
		return raw, false
	}
	changed := false
	for _, a := range atts {
		v, ok := a["cost_micros"].(float64)
		if !ok || v == 0 {
			continue
		}
		a["cost_micros"] = int64(math.Round(v * factor))
		changed = true
	}
	if !changed {
		return raw, false
	}
	b, err := json.Marshal(atts)
	if err != nil {
		return raw, false
	}
	return model.JSON(b), true
}

// readSetting returns a settings row's value.
func readSetting(db *gorm.DB, key string) (string, error) {
	var row model.Setting
	if err := db.Where("key = ?", key).First(&row).Error; err != nil {
		return "", err
	}
	return row.Value, nil
}

// unbookUnverified removes from the hourly rollup exactly what was booked there for
// this row, with the rollup's own attribution, and counts the request as unverified.
//
// What was booked depends on the database's history. A v2-origin row was replayed and
// booked with its raw amounts. A v1-origin row was booked by 1.0.12 from its attempts and
// then had the hourly amounts divided by the 1.0.13 migration along with the request
// amount, so the booked per-attempt amounts are the attempts scaled onto the request
// amount (scaleToRequest); using the raw attempts there would subtract display-currency
// numbers from a USD rollup (R116-02).
func unbookUnverified(tx *gorm.DB, row *model.CallLog, scaleToRequest bool) error {
	l := *row
	l.CostLedger = "" // Aggregate must see the amounts, not the stamp
	if scaleToRequest {
		if sum, priced := attemptsCostSum(l.Attempts); priced && sum > 0 && l.CostMicros > 0 {
			l.Attempts, _ = scaleAttempts(l.Attempts, float64(l.CostMicros)/float64(sum))
		}
	}
	for _, u := range logstore.Aggregate([]*model.CallLog{&l}) {
		if u.CostMicros == 0 && u.Requests == 0 {
			continue
		}
		err := tx.Exec("UPDATE usage_hourlies SET cost_micros = COALESCE(cost_micros, 0) - ?, cost_unverified = COALESCE(cost_unverified, 0) + ? "+
			"WHERE hour = ? AND user_id = ? AND group_id = ? AND api_key_id = ? AND account_id = ? AND provider = ? AND request_model = ? AND model_group = ? AND api_type = ?",
			u.CostMicros, u.Requests, u.Hour, u.UserID, u.GroupID, u.APIKeyID, u.AccountID, u.Provider, u.RequestModel, u.ModelGroup, u.APIType).Error
		if err != nil {
			return err
		}
	}
	return nil
}

// LegacyCostFixer returns the commit-time hook for journal records without a ledger
// stamp, applying the same evidence rule as the migration: a database that came straight
// from 1.0.12 (origin v0) holds display-currency records created before that first
// migration, which convert; a 1.0.13 database (origin v1) holds 1.0.13's own USD records;
// any other origin cannot tell what an unstamped record is, so it is kept as written and
// marked unverified (never counted as USD).
func LegacyCostFixer(db *gorm.DB, st *settings.Store) func(*model.CallLog) {
	origin, migratedAt := "v3", time.Time{}
	var o, m model.Setting
	if db.Where("key = ?", ledgerOriginKey).First(&o).Error == nil {
		origin = o.Value
	}
	if db.Where("key = ?", ledgerMarker).First(&m).Error == nil {
		migratedAt = m.UpdatedAt
	}
	return func(l *model.CallLog) {
		if l.CostLedger != "" {
			return
		}
		switch {
		case origin == "v0" && (migratedAt.IsZero() || l.CreatedAt.Before(migratedAt)):
			pr := st.Get().Pricing
			if strings.ToUpper(pr.Currency) == "CNY" {
				rate := pr.USDToCNY
				if rate <= 0 {
					rate = 7.2
				}
				l.CostMicros = int64(math.Round(float64(l.CostMicros) / rate))
				if att, changed := scaleAttempts(l.Attempts, 1/rate); changed {
					l.Attempts = att
				}
			}
			l.CostLedger = Ledger
		case origin == "v0" || origin == "v1":
			l.CostLedger = Ledger // created after the migration, or 1.0.13's own USD records
		default:
			_, priced := attemptsCostSum(l.Attempts)
			if l.CostMicros == 0 && !priced {
				l.CostLedger = Ledger
				return
			}
			l.CostLedger = LedgerUnverified
			l.CostKnown = false
		}
	}
}
