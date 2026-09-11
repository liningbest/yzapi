package api

import (
	"strings"

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
		"currency": s.st.Get().Pricing.Currency})
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
		"cached_input_per_m": in.CachedInputPerM, "cache_write_per_m": in.CacheWritePerM, "currency": in.Currency, "note": in.Note}
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

// costOut renders micro-units as a float in the base currency.
func costOut(micros int64) float64 { return float64(micros) / 1e6 }
