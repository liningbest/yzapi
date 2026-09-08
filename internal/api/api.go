// Package api implements the management REST API (admin + user console).
package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/config"
	"yzapi/internal/crypto"
	"yzapi/internal/essink"
	"yzapi/internal/gateway"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// Engines groups optional subsystems wired in by main.
type Engines struct {
	Route      RouteEngine
	Compliance ComplianceEngine
	ES         ESSink
}

type RouteEngine interface {
	Reload() error
	BuildVectors(ctx context.Context, ids []uint) (built, failed int, err error)
	Preview(ctx context.Context, text string, msgCount int) (PreviewResult, error)
}

type PreviewResult struct {
	Label      string
	Source     string
	Confidence float64
	GroupID    uint
	TopK       model.JSON
	Normalized string
	LatencyMs  int64
}

type ComplianceEngine interface {
	Reload() error
	BuildVectors(ctx context.Context, ids []uint) (built, failed int, err error)
	Test(ctx context.Context, text string) gateway.ComplianceVerdict
}

type ESSink interface {
	Status() essink.Status
	Test(ctx context.Context, cfg settings.Elasticsearch) (version string, err error)
}

type Server struct {
	cfg     *config.Config
	db      *gorm.DB
	gw      *gateway.Gateway
	st      *settings.Store
	cipher  *crypto.Cipher
	eng     Engines
	auth    *authService
	version string
	started time.Time
}

func New(cfg *config.Config, db *gorm.DB, gw *gateway.Gateway, st *settings.Store, cipher *crypto.Cipher, eng Engines, version string) (*Server, error) {
	a, err := newAuthService(cfg, db)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, db: db, gw: gw, st: st, cipher: cipher, eng: eng, auth: a, version: version, started: time.Now()}, nil
}

// SetEngines installs subsystems after construction (they need the server's embed func).
func (s *Server) SetEngines(e Engines) { s.eng = e }

// Register mounts all routes on the engine.
func (s *Server) Register(r *gin.Engine) {
	pub := r.Group("/api/public")
	pub.GET("/info", s.publicInfo)

	auth := r.Group("/api/auth")
	auth.POST("/login", s.login)
	auth.POST("/logout", s.requireAuth(), s.logout)
	auth.GET("/me", s.requireAuth(), s.me)
	auth.POST("/change-password", s.requireAuth(), s.changePassword)

	admin := r.Group("/api/admin", s.requireAuth(), s.requireAdmin())
	{
		admin.GET("/overview/live", s.overviewLive)
		admin.GET("/overview/usage", s.overviewUsage)
		admin.GET("/providers", s.listProviders)
		admin.GET("/models", s.listModels)
		admin.GET("/system/info", s.systemInfo)

		acc := admin.Group("/accounts")
		acc.GET("", s.listAccounts)
		acc.POST("", s.createAccount)
		acc.POST("/discover", s.discoverModels)
		acc.POST("/test", s.testAccount)
		acc.GET("/:id", s.getAccount)
		acc.PUT("/:id", s.updateAccount)
		acc.DELETE("/:id", s.deleteAccount)
		acc.PATCH("/:id/enabled", s.setAccountEnabled)
		acc.POST("/:id/reset-health", s.resetAccountHealth)

		mg := admin.Group("/model-groups")
		mg.GET("", s.listModelGroups)
		mg.POST("", s.createModelGroup)
		mg.GET("/:id", s.getModelGroup)
		mg.PUT("/:id", s.updateModelGroup)
		mg.DELETE("/:id", s.deleteModelGroup)

		us := admin.Group("/users")
		us.GET("", s.listUsers)
		us.POST("", s.createUser)
		us.GET("/:id", s.getUser)
		us.PUT("/:id", s.updateUser)
		us.DELETE("/:id", s.deleteUser)
		us.PATCH("/:id/enabled", s.setUserEnabled)
		us.POST("/:id/reset-password", s.resetUserPassword)
		us.POST("/:id/unlock", s.unlockUser)

		ug := admin.Group("/user-groups")
		ug.GET("", s.listUserGroups)
		ug.POST("", s.createUserGroup)
		ug.GET("/:id", s.getUserGroup)
		ug.PUT("/:id", s.updateUserGroup)
		ug.DELETE("/:id", s.deleteUserGroup)
		ug.PATCH("/:id/enabled", s.setUserGroupEnabled)

		lg := admin.Group("/logs")
		lg.GET("", s.listLogs)
		lg.GET("/filters", s.logFilters)
		lg.GET("/:id", s.getLog)

		admin.GET("/usage", s.adminUsage)

		se := admin.Group("/settings")
		se.GET("", s.getSettings)
		se.PUT("/basic", s.putBasic)
		se.PUT("/performance", s.putPerformance)
		se.PUT("/vector", s.putVector)
		se.POST("/vector/test", s.testVector)
		se.PUT("/smart_route", s.putSmartRoute)
		se.PUT("/compliance", s.putCompliance)
		se.PUT("/elasticsearch", s.putElasticsearch)
		se.POST("/elasticsearch/test", s.testElasticsearch)
		se.GET("/elasticsearch/status", s.esStatus)

		s.registerRouteAPI(admin.Group("/route"))
		s.registerComplianceAPI(admin.Group("/compliance"))
	}

	user := r.Group("/api/user", s.requireAuth())
	{
		user.GET("/models", s.userModels)
		user.GET("/keys", s.listMyKeys)
		user.POST("/keys", s.createMyKey)
		user.PUT("/keys/:id", s.renameMyKey)
		user.PATCH("/keys/:id/enabled", s.setMyKeyEnabled)
		user.DELETE("/keys/:id", s.deleteMyKey)
		user.GET("/usage", s.userUsage)
		user.GET("/logs", s.userLogs)
		user.GET("/group", s.userGroup)
	}
}

// ---------- helpers ----------

func fail(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg, "code": code})
}

func badRequest(c *gin.Context, msg string) { fail(c, 400, "bad_request", msg) }
func notFound(c *gin.Context)               { fail(c, 404, "not_found", "resource not found") }
func serverError(c *gin.Context, err error) {
	fail(c, 500, "internal_error", err.Error())
}

func idParam(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 {
		badRequest(c, "invalid id")
		return 0, false
	}
	return uint(n), true
}

type pageParams struct {
	Page, Size int
	Offset     int
}

func paging(c *gin.Context) pageParams {
	p, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	sz, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if p < 1 {
		p = 1
	}
	if sz < 1 {
		sz = 20
	}
	if sz > 200 {
		sz = 200
	}
	return pageParams{Page: p, Size: sz, Offset: (p - 1) * sz}
}

func listResp(c *gin.Context, items any, total int64) {
	c.JSON(200, gin.H{"items": items, "total": total})
}

// timeRange parses range/from/to query params.
func timeRange(c *gin.Context) (from, to time.Time) {
	to = time.Now()
	switch c.DefaultQuery("range", "7d") {
	case "1h":
		from = to.Add(-time.Hour)
	case "24h":
		from = to.Add(-24 * time.Hour)
	case "7d":
		from = to.AddDate(0, 0, -7)
	case "30d":
		from = to.AddDate(0, 0, -30)
	case "90d":
		from = to.AddDate(0, 0, -90)
	case "custom":
		if f, err := time.Parse(time.RFC3339, c.Query("from")); err == nil {
			from = f
		} else {
			from = to.AddDate(0, 0, -7)
		}
		if t, err := time.Parse(time.RFC3339, c.Query("to")); err == nil {
			to = t
		}
	default:
		from = to.AddDate(0, 0, -7)
	}
	return from, to
}

func queryUint(c *gin.Context, key string) uint {
	n, _ := strconv.ParseUint(c.Query(key), 10, 64)
	return uint(n)
}

func queryBool(c *gin.Context, key string) (val bool, set bool) {
	v := c.Query(key)
	if v == "" {
		return false, false
	}
	return v == "true" || v == "1", true
}

func isNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }

func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return "%" + s + "%"
}

func (s *Server) publicInfo(c *gin.Context) {
	b := s.st.Get().Basic
	c.JSON(200, gin.H{"site_name": b.SiteName, "version": s.version, "base_url": b.BaseURL})
}

func (s *Server) systemInfo(c *gin.Context) {
	c.JSON(200, gin.H{
		"version":    s.version,
		"go_version": goVersion(),
		"db_driver":  s.cfg.DBDriver,
		"uptime_sec": int64(time.Since(s.started).Seconds()),
		"started_at": s.started,
		"data_dir":   s.cfg.DataDir,
	})
}

func maskKey(k string) string {
	if len(k) <= 10 {
		return "******"
	}
	return k[:6] + "…" + k[len(k)-4:]
}

var _ = http.StatusOK
