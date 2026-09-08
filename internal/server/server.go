// Package server wires HTTP routing: data plane, management API and the embedded SPA.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/api"
	"yzapi/internal/config"
	"yzapi/internal/gateway"
	"yzapi/web"
)

// MetricsExtra adds subsystem gauges to the /metrics output.
type MetricsExtra = gateway.ExtraMetrics

func New(cfg *config.Config, db *gorm.DB, gw *gateway.Gateway, mgmt *api.Server, extra ...MetricsExtra) *http.Server {
	if !cfg.Dev {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(cfg.Dev), securityHeaders())
	r.RedirectTrailingSlash = false

	// Health
	r.GET("/health/live", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		sqlDB, err := db.DB()
		if err != nil || sqlDB.Ping() != nil || gw.Snapshot() == nil {
			c.JSON(503, gin.H{"status": "not_ready"})
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Prometheus metrics
	r.GET("/metrics", func(c *gin.Context) {
		if cfg.MetricsToken != "" && c.GetHeader("Authorization") != "Bearer "+cfg.MetricsToken {
			c.JSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.Status(200)
		gw.WriteMetrics(c.Writer, extra...)
	})

	// Data plane (plain net/http handlers for minimal overhead)
	v1 := r.Group("/v1")
	v1.GET("/models", gin.WrapF(gw.HandleModels))
	v1.POST("/chat/completions", gin.WrapF(gw.HandleChat))
	v1.POST("/responses", gin.WrapF(gw.HandleResponses))
	v1.POST("/messages", gin.WrapF(gw.HandleMessages))
	v1.POST("/embeddings", gin.WrapF(gw.HandleEmbeddings))
	v1.POST("/images/generations", gin.WrapF(gw.HandleImages))
	// Some clients omit /v1 or double it; be forgiving.
	r.POST("/chat/completions", gin.WrapF(gw.HandleChat))
	r.POST("/messages", gin.WrapF(gw.HandleMessages))
	r.POST("/v1/v1/chat/completions", gin.WrapF(gw.HandleChat))
	r.GET("/models", gin.WrapF(gw.HandleModels))

	// Management API
	mgmt.Register(r)

	// SPA
	mountSPA(r)

	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           r,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func mountSPA(r *gin.Engine) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		slog.Warn("embedded frontend not found; UI disabled")
		return
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		slog.Warn("embedded frontend missing index.html; build the web app first")
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/v1/") {
				c.JSON(404, gin.H{"error": "not found"})
				return
			}
			c.String(200, "YZ AI Gateway is running. Web UI is not bundled in this build.")
		})
		return
	}
	fileServer := http.FileServer(http.FS(dist))
	index, _ := fs.ReadFile(dist, "index.html")
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/v1/") {
			c.JSON(404, gin.H{"error": "not found", "code": "not_found"})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(405)
			return
		}
		clean := path.Clean(p)
		if clean != "/" {
			if f, err := dist.Open(strings.TrimPrefix(clean, "/")); err == nil {
				if st, err := f.Stat(); err == nil && !st.IsDir() {
					f.Close()
					if strings.HasPrefix(clean, "/assets/") {
						c.Header("Cache-Control", "public, max-age=31536000, immutable")
					}
					fileServer.ServeHTTP(c.Writer, c.Request)
					return
				}
				f.Close()
			}
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(200, "text/html; charset=utf-8", index)
	})
}

func requestLogger(dev bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if !dev && c.Writer.Status() < 500 {
			return
		}
		slog.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(),
			"ms", time.Since(start).Milliseconds(), "ip", c.ClientIP())
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "same-origin")
		c.Next()
	}
}
