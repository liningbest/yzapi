package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

func validatePrice(in *priceIn) string {
	in.Pattern = strings.TrimSpace(in.Pattern)
	in.Provider = strings.TrimSpace(strings.ToLower(in.Provider))
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Pattern == "" || len(in.Pattern) > 128 {
		return "模型名称（或前缀）不能为空，最长 128 字符"
	}
	if in.Currency != "USD" && in.Currency != "CNY" {
		return "货币只支持 USD / CNY"
	}
	for _, v := range []float64{in.InputPerM, in.OutputPerM, in.CachedInputPerM, in.CacheWritePerM} {
		if v < 0 || v > 1e6 {
			return "单价范围 0 - 1000000（每百万 Token）"
		}
	}
	return ""
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
		serverError(c, err)
		return
	}
	if !p.Enabled {
		s.db.Model(&p).Update("enabled", false)
	}
	_ = s.pricer.Reload()
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
		serverError(c, err)
		return
	}
	_ = s.pricer.Reload()
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
	_ = s.pricer.Reload()
	c.JSON(200, gin.H{})
}

// resetBuiltinPrices restores the reference table; custom rows are kept.
func (s *Server) resetBuiltinPrices(c *gin.Context) {
	if err := pricing.ResetBuiltin(s.db); err != nil {
		serverError(c, err)
		return
	}
	_ = s.pricer.Reload()
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

const importMaxBytes = 32 << 20

type importIn struct {
	Source          string `json:"source"` // litellm | easycpa | url
	URL             string `json:"url"`
	Apply           bool   `json:"apply"`
	OverwriteEdited bool   `json:"overwrite_edited"`
}

// importPrices downloads a price catalog (LiteLLM or EasyCLIProxyAPI format) and either
// previews the changes or applies them. Manual and edited rows are kept unless
// overwrite_edited is set.
func (s *Server) importPrices(c *gin.Context) {
	var in importIn
	if err := c.ShouldBindJSON(&in); err != nil {
		badRequest(c, "invalid body")
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		badRequest(c, "来源地址无效")
		return
	}
	req.Header.Set("User-Agent", "yzapi-gateway/1.0")
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
	s.importCatalog(c, data, url, in.Apply, in.OverwriteEdited)
}

// importPricesFile is the upload variant: multipart field "file", plus "apply" and
// "overwrite_edited" form fields ("1" / "true").
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
	s.importCatalog(c, data, "upload:"+fh.Filename, truthy(c.PostForm("apply")), truthy(c.PostForm("overwrite_edited")))
}

func (s *Server) importCatalog(c *gin.Context, data []byte, origin string, apply, overwrite bool) {
	cat, err := pricing.ParseCatalog(data)
	if err != nil {
		badRequest(c, "价目文件格式无法识别（支持 LiteLLM model_prices_and_context_window.json 与 EasyCLIProxyAPI model_prices.json）: "+err.Error())
		return
	}
	if len(cat.Rows) == 0 {
		badRequest(c, "价目文件里没有可用的行")
		return
	}
	plan, err := pricing.PlanImport(s.db, cat, overwrite)
	if err != nil {
		serverError(c, err)
		return
	}
	if apply {
		if err := plan.Apply(s.db); err != nil {
			serverError(c, err)
			return
		}
		_ = s.pricer.Reload()
		slog.Info("price catalog imported", "origin", origin, "plan", plan.Summary(), "by", cur(c).Username)
	}
	c.JSON(200, gin.H{"applied": apply, "origin": origin, "plan": plan})
}
