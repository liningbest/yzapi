// Package config loads process-level configuration from environment variables.
// Everything that can change at runtime lives in the settings table instead.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr string
	DataDir    string

	// Database: "sqlite" (default) or "postgres".
	DBDriver string
	DBDSN    string

	// Optional Redis for multi-instance concurrency accounting.
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	InitialAdminPassword string
	JWTSecret            string

	// HTTPProxy forces every upstream request through this proxy (YZAPI_HTTP_PROXY). When
	// empty the standard HTTP_PROXY / HTTPS_PROXY / NO_PROXY variables apply, which never
	// proxy loopback or NO_PROXY hosts.
	HTTPProxy string
	// UpstreamHTTP2 allows HTTP/2 to upstreams (default true). Set YZAPI_UPSTREAM_HTTP2=0
	// to force HTTP/1.1 when a provider's HTTP/2 path streams poorly.
	UpstreamHTTP2 bool

	LogLevel string
	Dev      bool

	// Optional bearer token required to scrape /metrics (empty = open).
	MetricsToken string

	ShutdownTimeout time.Duration
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func Load() *Config {
	dataDir := env("YZAPI_DATA_DIR", "/opt/yzapi")
	c := &Config{
		ListenAddr:           env("YZAPI_LISTEN", "0.0.0.0:8080"),
		DataDir:              dataDir,
		DBDriver:             env("YZAPI_DB_DRIVER", "sqlite"),
		DBDSN:                env("YZAPI_DB_DSN", ""),
		RedisAddr:            env("YZAPI_REDIS_ADDR", ""),
		RedisPassword:        env("YZAPI_REDIS_PASSWORD", ""),
		RedisDB:              envInt("YZAPI_REDIS_DB", 0),
		InitialAdminPassword: env("YZAPI_INITIAL_ADMIN_PASSWORD", ""),
		JWTSecret:            env("YZAPI_JWT_SECRET", ""),
		HTTPProxy:            env("YZAPI_HTTP_PROXY", ""),
		UpstreamHTTP2:        env("YZAPI_UPSTREAM_HTTP2", "1") != "0",
		LogLevel:             env("YZAPI_LOG_LEVEL", "info"),
		MetricsToken:         env("YZAPI_METRICS_TOKEN", ""),
		Dev:                  env("YZAPI_DEV", "") == "1",
		ShutdownTimeout:      time.Duration(envInt("YZAPI_SHUTDOWN_TIMEOUT", 60)) * time.Second,
	}
	return c
}
