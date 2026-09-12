package api

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"
)

// Runtime copies of the configuration. Every mutating handler writes the database
// first and then refreshes the in-memory copies the data plane serves from; a refresh
// that fails must never be reported as success, otherwise the administrator believes a
// change (or a rollback) is live while requests still run on the previous state.

type runtimeFailure struct {
	Subsystem string `json:"subsystem"`
	Code      string `json:"code"`
	Error     string `json:"error"`
}

// refreshRuntimes refreshes the named runtimes ("prices", "gateway", "compliance",
// "route", "vector") and returns every failure; one failing runtime never skips the
// others. "vector" invalidates the embedding client and refreshes the two engines that
// depend on it.
func (s *Server) refreshRuntimes(names ...string) []runtimeFailure {
	var failed []runtimeFailure
	add := func(sub, code string, err error) {
		if err != nil {
			slog.Error("runtime reload failed after a committed write; the previous state stays live", "subsystem", sub, "err", err)
			failed = append(failed, runtimeFailure{Subsystem: sub, Code: code, Error: err.Error()})
		}
	}
	for _, n := range names {
		switch n {
		case "prices":
			add("prices", "price_reload_failed", s.pricer.Reload())
		case "gateway":
			add("gateway", "gateway_reload_failed", s.gw.Reload())
		case "compliance":
			if s.eng.Compliance != nil {
				add("compliance", "compliance_reload_failed", s.eng.Compliance.Reload())
			}
		case "route":
			if s.eng.Route != nil {
				add("route", "route_reload_failed", s.eng.Route.Reload())
			}
		case "vector":
			s.InvalidateVector()
			failed = append(failed, s.refreshRuntimes("route", "compliance")...)
		}
	}
	return failed
}

// reloadFailed answers 503: the database already holds the change, the listed runtimes
// do not. extra carries the fields the success response would have had.
func reloadFailed(c *gin.Context, failed []runtimeFailure, extra gin.H) {
	code := "runtime_reload_failed"
	if len(failed) == 1 {
		code = failed[0].Code
	}
	subs := make([]string, 0, len(failed))
	for _, f := range failed {
		subs = append(subs, f.Subsystem)
	}
	body := gin.H{"code": code, "failed": failed,
		"error": "数据已写入数据库，但运行态未刷新（" + strings.Join(subs, "、") + "），请求仍按之前的配置处理；请重试或重启网关"}
	for k, v := range extra {
		body[k] = v
	}
	c.JSON(503, body)
}

// reloadRuntimes refreshes the named runtimes and, on any failure, responds 503 and
// returns false so the handler stops.
func (s *Server) reloadRuntimes(c *gin.Context, names ...string) bool {
	if failed := s.refreshRuntimes(names...); len(failed) > 0 {
		reloadFailed(c, failed, nil)
		return false
	}
	return true
}
