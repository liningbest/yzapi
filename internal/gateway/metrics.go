package gateway

import (
	"io"

	"yzapi/internal/metrics"
	"yzapi/internal/model"
)

// ExtraMetrics lets main add gauges from other subsystems (log store, ES sink ...).
type ExtraMetrics func(w *metrics.Writer)

// WriteMetrics renders all gateway metrics in Prometheus text format.
func (g *Gateway) WriteMetrics(out io.Writer, extra ...ExtraMetrics) {
	w := metrics.NewWriter(out)
	w.LabeledCounter("yzapi_requests_total", "Data-plane requests by result and api type", g.Metrics.Requests)
	w.Histogram("yzapi_request_duration_seconds", "End-to-end request latency", g.Metrics.Latency)
	w.LabeledCounter("yzapi_tokens_total", "Tokens accounted by kind", g.Metrics.Tokens)
	w.LabeledCounter("yzapi_upstream_attempts_total", "Upstream attempts by outcome", g.Metrics.UpstreamAttempts)
	w.Counter("yzapi_compliance_degraded_total", "Requests where the semantic compliance check could not run", &g.Metrics.ComplianceDegraded)
	w.Counter("yzapi_compliance_blocked_total", "Requests blocked by content compliance", &g.Metrics.ComplianceBlocked)
	w.Gauge("yzapi_compliance_last_degraded_timestamp", "Unix time of the last degraded compliance check (0 = never)", float64(g.Metrics.lastDegraded.Load()))

	inflight, limit, waiting, queue := g.gate.stats()
	w.Gauge("yzapi_gateway_inflight", "Requests currently holding a gateway slot", float64(inflight))
	w.Gauge("yzapi_gateway_limit", "Configured gateway concurrency limit (0 = unlimited)", float64(limit))
	w.Gauge("yzapi_gateway_waiting", "Requests waiting for a gateway slot", float64(waiting))
	w.Gauge("yzapi_gateway_queue_size", "Configured wait queue size", float64(queue))
	w.Gauge("yzapi_streams_active", "Streaming responses in progress", float64(g.streamCount.Load()))
	used, blimit := g.bodyBudget.stats()
	w.Gauge("yzapi_body_memory_bytes", "Request body bytes currently reserved", float64(used))
	w.Gauge("yzapi_body_memory_limit_bytes", "Configured request body memory budget", float64(blimit))
	w.Gauge("yzapi_active_users", "Users seen in the last 5 minutes", float64(g.activeUsers.count()))
	w.Gauge("yzapi_keycache_entries", "Entries in the API key cache", float64(g.keys.size()))

	snap := g.snap.get()
	acc := g.accounts.snapshot()
	var accRows, healthRows, limitRows [][2]any
	for _, u := range snap.Accounts {
		lbl := metrics.Label("account", u.Name) + "," + metrics.Label("id", itoa(u.ID)) + "," + metrics.Label("provider", u.Provider)
		accRows = append(accRows, [2]any{lbl, acc[u.ID]})
		limitRows = append(limitRows, [2]any{lbl, u.MaxConcurrency})
		st, _, _ := g.health.state(u.ID)
		hv := 1.0
		switch st {
		case model.HealthCooling:
			hv = 0.5
		case model.HealthUnavailable:
			hv = 0
		}
		healthRows = append(healthRows, [2]any{lbl, hv})
	}
	w.GaugeLabeled("yzapi_account_inflight", "In-flight requests per upstream account", accRows)
	w.GaugeLabeled("yzapi_account_limit", "Configured concurrency limit per account (0 = unlimited)", limitRows)
	w.GaugeLabeled("yzapi_account_health", "Account health: 1 available, 0.5 cooling, 0 unavailable", healthRows)

	grp := g.groups.snapshot()
	var grpRows, quotaRows, usedRows [][2]any
	for _, gv := range snap.Groups {
		lbl := metrics.Label("group", gv.Name) + "," + metrics.Label("id", itoa(gv.ID))
		grpRows = append(grpRows, [2]any{lbl, grp[gv.ID]})
		quotaRows = append(quotaRows, [2]any{lbl, gv.TokenQuota})
		usedRows = append(usedRows, [2]any{lbl, g.quota.usage(gv.ID)})
	}
	w.GaugeLabeled("yzapi_group_inflight", "In-flight requests per user group", grpRows)
	w.GaugeLabeled("yzapi_group_token_quota", "Monthly token quota per user group (0 = unlimited)", quotaRows)
	w.GaugeLabeled("yzapi_group_tokens_used", "Tokens used this month per user group", usedRows)

	for _, fn := range extra {
		fn(w)
	}
}

func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
