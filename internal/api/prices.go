package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"yzapi/internal/model"
	"yzapi/internal/pricing"
	"yzapi/internal/settings"
)

// ---- price table ----

func (s *Server) listPrices(c *gin.Context) {
	q := s.db.Model(&model.ModelPrice{})
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		q = q.Where("pattern LIKE ? ESCAPE '\\' OR provider LIKE ? ESCAPE '\\'", likeEscape(v), likeEscape(v))
	}
	var rows []model.ModelPrice
	if err := q.Order("provider ASC, pattern ASC").Find(&rows).Error; err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": rows, "total": len(rows), "builtin_updated": pricing.BuiltinUpdated,
		"currency": s.pricer.Currency(), "ledger": pricing.Ledger})
}

type priceIn struct {
	Pattern         string  `json:"pattern"`
	Provider        string  `json:"provider"`
	InputPerM       float64 `json:"input_per_m"`
	OutputPerM      float64 `json:"output_per_m"`
	CachedInputPerM float64 `json:"cached_input_per_m"`
	CacheWritePerM  float64 `json:"cache_write_per_m"`
	Currency        string  `json:"currency"`
	Enabled         *bool   `json:"enabled"`
	Note            string  `json:"note"`
}

// validatePrice normalises and checks a manual row with the same rule the importer
// uses (pricing.ValidateRow): the key is lower-cased so the unique index is
// case-insensitive on every database.
func validatePrice(in *priceIn) string {
	r := pricing.CatalogRow{Pattern: in.Pattern, Provider: in.Provider, InputPerM: in.InputPerM, OutputPerM: in.OutputPerM,
		CachedPerM: in.CachedInputPerM, WritePerM: in.CacheWritePerM, Currency: in.Currency, Note: in.Note}
	msg := pricing.ValidateRow(&r)
	in.Pattern, in.Provider, in.Currency, in.Note = r.Pattern, r.Provider, r.Currency, r.Note
	return msg
}

// reloadPrices refreshes the in-memory price table after a write. A failure is not
// silent: the database already holds the new rows while requests would still be priced
// from the old copy, so the caller gets a 503 that says exactly that.
func (s *Server) reloadPrices(c *gin.Context) bool {
	if err := s.pricer.Reload(); err != nil {
		slog.Error("price table written but runtime reload failed", "err", err)
		fail(c, 503, "price_reload_failed", "价目已写入数据库，但运行态未刷新，当前请求仍按旧价目计费；请重试或重启网关: "+err.Error())
		return false
	}
	return true
}

// priceKeyConflict reports a unique-key violation on (provider, pattern) as a 409.
func priceKeyConflict(c *gin.Context, err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key") {
		fail(c, 409, "price_exists", "同一供应商下已有同名模型的价格行")
		return true
	}
	return false
}

func (s *Server) createPrice(c *gin.Context) {
	var in priceIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := validatePrice(&in); msg != "" {
		badRequest(c, msg)
		return
	}
	p := model.ModelPrice{Pattern: in.Pattern, Provider: in.Provider, InputPerM: in.InputPerM, OutputPerM: in.OutputPerM,
		CachedInputPerM: in.CachedInputPerM, CacheWritePerM: in.CacheWritePerM, Currency: in.Currency, Enabled: in.Enabled == nil || *in.Enabled, Note: in.Note}
	if err := s.db.Create(&p).Error; err != nil {
		if !priceKeyConflict(c, err) {
			serverError(c, err)
		}
		return
	}
	if !p.Enabled {
		s.db.Model(&p).Update("enabled", false)
	}
	if !s.reloadPrices(c) {
		return
	}
	c.JSON(200, p)
}

func (s *Server) updatePrice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var p model.ModelPrice
	if err := s.db.First(&p, id).Error; err != nil {
		notFound(c)
		return
	}
	var in priceIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if msg := validatePrice(&in); msg != "" {
		badRequest(c, msg)
		return
	}
	upd := map[string]any{"pattern": in.Pattern, "provider": in.Provider, "input_per_m": in.InputPerM, "output_per_m": in.OutputPerM,
		"cached_input_per_m": in.CachedInputPerM, "cache_write_per_m": in.CacheWritePerM, "currency": in.Currency, "note": in.Note, "edited": true}
	if in.Enabled != nil {
		upd["enabled"] = *in.Enabled
	}
	if err := s.db.Model(&p).Updates(upd).Error; err != nil {
		if !priceKeyConflict(c, err) {
			serverError(c, err)
		}
		return
	}
	if !s.reloadPrices(c) {
		return
	}
	s.db.First(&p, id)
	c.JSON(200, p)
}

func (s *Server) deletePrice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	res := s.db.Delete(&model.ModelPrice{}, id)
	if res.Error != nil {
		serverError(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		notFound(c)
		return
	}
	if !s.reloadPrices(c) {
		return
	}
	c.JSON(200, gin.H{})
}

// resetBuiltinPrices restores the reference table; custom rows are kept.
func (s *Server) resetBuiltinPrices(c *gin.Context) {
	if err := pricing.ResetBuiltin(s.db); err != nil {
		serverError(c, err)
		return
	}
	if !s.reloadPrices(c) {
		return
	}
	c.JSON(200, gin.H{"builtin_updated": pricing.BuiltinUpdated})
}

// lookupPrice answers "what would this model cost" for the UI and for self-checks.
func (s *Server) lookupPrice(c *gin.Context) {
	p, ok := s.pricer.Lookup(c.Query("provider"), c.Query("model"))
	if !ok {
		c.JSON(200, gin.H{"found": false})
		return
	}
	c.JSON(200, gin.H{"found": true, "price": p})
}

// ---- pricing settings ----

func (s *Server) putPricing(c *gin.Context) {
	var in settings.Pricing
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency != "CNY" && in.Currency != "USD" {
		badRequest(c, "计价货币只支持 CNY / USD")
		return
	}
	if in.USDToCNY <= 0 || in.USDToCNY > 100 {
		badRequest(c, "汇率范围 0 - 100")
		return
	}
	if err := s.st.SetPricing(in); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(200, in)
}

// costOut renders ledger micro-units (USD) as a float in the display currency.
func (s *Server) costOut(micros int64) float64 { return s.pricer.Display(micros) }

// displayCost is a call log's cost for display: 0 when its currency is unverified, so
// an unknown-currency amount is never shown as USD.
func (s *Server) displayCost(l *model.CallLog) float64 {
	if l.CostLedger == model.CostLedgerUnverified {
		return 0
	}
	return s.costOut(l.CostMicros)
}

// fillCost sets the display fields on a call log returned as a whole.
func (s *Server) fillCost(l *model.CallLog) {
	l.CostUnverified = l.CostLedger == model.CostLedgerUnverified
	l.Cost = s.displayCost(l)
}

// ---- price catalog import ----
//
// Importing is a two-step, bound flow: a preview parses the catalog, stores it under a
// plan_id together with its SHA-256, and returns the plan; apply takes that plan_id and
// writes exactly the bytes that were previewed (re-planned inside the write transaction
// so the counts describe what actually changed). Nothing is downloaded again at apply
// time, so a remote file that changed between the two clicks cannot slip in.

const (
	importMaxBytes  = 32 << 20
	importPlanTTL   = 30 * time.Minute
	importPlanCap   = 16
	importSourceUA  = "yzapi-gateway/1.0"
	importFetchWait = 60 * time.Second
)

type importIn struct {
	Source            string `json:"source"` // litellm | easycpa | url
	URL               string `json:"url"`
	Apply             bool   `json:"apply"` // rejected: apply goes through /import/apply with a plan_id
	OverwriteEdited   bool   `json:"overwrite_edited"`
	OverwriteCurrency bool   `json:"overwrite_currency"`
}

// storedImport is a previewed catalog waiting for apply.
type storedImport struct {
	id      string
	sha     string
	origin  string
	catalog *pricing.Catalog
	opts    pricing.ImportOptions
	by      string
	created time.Time
}

type importPlans struct {
	mu    sync.Mutex
	plans map[string]*storedImport
	// applying serialises Apply calls in this process; the unique index on
	// (provider, pattern) is what guarantees one row per key across processes.
	applying sync.Mutex
}

func newImportPlans() *importPlans { return &importPlans{plans: map[string]*storedImport{}} }

func (ip *importPlans) put(si *storedImport) {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	now := time.Now()
	for id, p := range ip.plans {
		if now.Sub(p.created) > importPlanTTL {
			delete(ip.plans, id)
		}
	}
	for len(ip.plans) >= importPlanCap {
		oldest := ""
		for id, p := range ip.plans {
			if oldest == "" || p.created.Before(ip.plans[oldest].created) {
				oldest = id
			}
		}
		delete(ip.plans, oldest)
	}
	ip.plans[si.id] = si
}

// get returns a stored preview that has not expired; it stays stored until done().
func (ip *importPlans) get(id string) *storedImport {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	p, ok := ip.plans[id]
	if !ok || time.Since(p.created) > importPlanTTL {
		delete(ip.plans, id)
		return nil
	}
	return p
}

// done removes a plan once it has been applied: a plan id is single-use.
func (ip *importPlans) done(id string) {
	ip.mu.Lock()
	delete(ip.plans, id)
	ip.mu.Unlock()
}

func newPlanID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// importPrices downloads a price catalog (LiteLLM or EasyCLIProxyAPI format) and
// returns a preview bound to a plan_id. It never writes.
func (s *Server) importPrices(c *gin.Context) {
	var in importIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if in.Apply {
		badRequest(c, "预览与应用已分离：先预览取得 plan_id，再调用 /api/admin/prices/import/apply")
		return
	}
	url := strings.TrimSpace(in.URL)
	if u, ok := pricing.KnownSources[strings.ToLower(in.Source)]; ok && url == "" {
		url = u
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		badRequest(c, "来源地址必须为 http(s) URL，或 source 取 litellm / easycpa")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), importFetchWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		badRequest(c, "来源地址无效")
		return
	}
	req.Header.Set("User-Agent", importSourceUA)
	resp, err := s.gw.HTTPClient().Do(req)
	if err != nil {
		fail(c, 502, "import_fetch_failed", "下载价目失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		fail(c, 502, "import_fetch_failed", fmt.Sprintf("下载价目失败: HTTP %d", resp.StatusCode))
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, importMaxBytes+1))
	if err != nil {
		fail(c, 502, "import_fetch_failed", "下载价目失败: "+err.Error())
		return
	}
	if len(data) > importMaxBytes {
		badRequest(c, "价目文件超过 32 MB")
		return
	}
	s.importPreview(c, data, url, pricing.ImportOptions{OverwriteEdited: in.OverwriteEdited, OverwriteCurrency: in.OverwriteCurrency})
}

// importPricesFile is the upload variant: multipart field "file" plus the option
// fields "overwrite_edited" / "overwrite_currency" ("1" / "true"). Preview only.
func (s *Server) importPricesFile(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		badRequest(c, "缺少文件字段 file")
		return
	}
	if fh.Size > importMaxBytes {
		badRequest(c, "价目文件超过 32 MB")
		return
	}
	f, err := fh.Open()
	if err != nil {
		badRequest(c, "无法读取文件")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, importMaxBytes+1))
	if err != nil {
		badRequest(c, "无法读取文件")
		return
	}
	truthy := func(v string) bool {
		v = strings.ToLower(strings.TrimSpace(v))
		return v == "1" || v == "true" || v == "yes"
	}
	if truthy(c.PostForm("apply")) {
		badRequest(c, "预览与应用已分离：先预览取得 plan_id，再调用 /api/admin/prices/import/apply")
		return
	}
	s.importPreview(c, data, "upload:"+fh.Filename, pricing.ImportOptions{OverwriteEdited: truthy(c.PostForm("overwrite_edited")), OverwriteCurrency: truthy(c.PostForm("overwrite_currency"))})
}

func (s *Server) importPreview(c *gin.Context, data []byte, origin string, opts pricing.ImportOptions) {
	cat, err := pricing.ParseCatalog(data)
	if err != nil {
		badRequest(c, "价目文件格式无法识别（支持 LiteLLM model_prices_and_context_window.json 与 EasyCLIProxyAPI model_prices.json）: "+err.Error())
		return
	}
	if len(cat.Rows) == 0 {
		msg := "价目文件里没有可用的行"
		if cat.Invalid > 0 {
			msg += fmt.Sprintf("（%d 条未通过校验）", cat.Invalid)
		}
		badRequest(c, msg)
		return
	}
	plan, err := pricing.PlanImportWith(s.db, cat, opts)
	if err != nil {
		serverError(c, err)
		return
	}
	sum := sha256.Sum256(data)
	si := &storedImport{id: newPlanID(), sha: hex.EncodeToString(sum[:]), origin: origin, catalog: cat, opts: opts, by: cur(c).Username, created: time.Now()}
	s.imports.put(si)
	c.JSON(200, gin.H{"applied": false, "plan_id": si.id, "sha256": si.sha, "origin": origin, "options": opts,
		"expires_in": int(importPlanTTL.Seconds()), "plan": plan})
}

type importApplyIn struct {
	PlanID string `json:"plan_id"`
	SHA256 string `json:"sha256"` // optional double-check against the preview
}

// importApply writes a previewed catalog. The configuration snapshot is taken here,
// immediately before the write transaction, so a preview never consumes one.
func (s *Server) importApply(c *gin.Context) {
	var in importApplyIn
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.PlanID) == "" {
		badRequest(c, "缺少 plan_id：请先预览")
		return
	}
	si := s.imports.get(strings.TrimSpace(in.PlanID))
	if si == nil {
		fail(c, 409, "import_plan_expired", "预览已过期或已被使用，请重新预览")
		return
	}
	if in.SHA256 != "" && !strings.EqualFold(in.SHA256, si.sha) {
		fail(c, 409, "import_plan_mismatch", "预览内容与应用请求不一致，请重新预览")
		return
	}
	s.imports.applying.Lock()
	defer s.imports.applying.Unlock()
	if err := s.snapshotConfig(cur(c).Username, "POST "+c.Request.URL.Path); err != nil {
		serverError(c, err)
		return
	}
	plan, err := pricing.PlanImportWith(s.db, si.catalog, si.opts)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := plan.Apply(s.db); err != nil {
		if !priceKeyConflict(c, err) {
			serverError(c, err)
		}
		return
	}
	s.imports.done(si.id)
	slog.Info("price catalog imported", "origin", si.origin, "sha256", si.sha, "plan", plan.Summary(), "by", cur(c).Username)
	if !s.reloadPrices(c) {
		return
	}
	c.JSON(200, gin.H{"applied": true, "plan_id": si.id, "sha256": si.sha, "origin": si.origin, "options": si.opts, "plan": plan})
}
