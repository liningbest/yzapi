// Package gateway implements the data plane: authentication, authorization,
// concurrency control, routing, upstream forwarding and accounting.
package gateway

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"yzapi/internal/config"
	"yzapi/internal/crypto"
	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// RouteResult is produced by a Router for a virtual-model request.
type RouteResult struct {
	Label      string // simple | complex
	Source     string // rule | context | vector | fallback
	Confidence float64
	GroupID    uint
	TopK       model.JSON
	Normalized string
	LatencyMs  int64
}

// Router decides which model group serves a virtual-model request.
type Router interface {
	Decide(ctx context.Context, requestID, text string, msgCount int) RouteResult
}

// ComplianceVerdict is produced by a Checker.
type ComplianceVerdict struct {
	Hit          bool
	Block        bool
	Action       string
	RiskLevel    string
	DetectMethod string
	PolicyGroup  string
	PolicyID     uint
	Evidence     string
	Confidence   float64
	Hits         model.JSON
}

// Checker inspects request text before it is forwarded.
type Checker interface {
	Check(ctx context.Context, text string) ComplianceVerdict
}

type Gateway struct {
	cfg      *config.Config
	db       *gorm.DB
	cipher   *crypto.Cipher
	settings *settings.Store
	logs     *logstore.Store

	snap     snapshotHolder
	keys     *keyCache
	health   *healthTracker
	quota    *quotaTracker
	gate     *gate
	groups   *counterMap
	apikeys  *counterMap
	accounts *counterMap

	transport atomic.Pointer[http.Transport]
	client    atomic.Pointer[http.Client]

	router  atomic.Pointer[Router]
	checker atomic.Pointer[Checker]

	activeUsers    activeSet
	streamCount    atomic.Int64
	AuditLogger    func(*model.AuditLog)
	DecisionLogger func(*model.RouteDecision)
	BodySink       BodySink
}

// BodySink receives request/response bodies for external audit storage.
type BodySink interface {
	Enabled() bool
	Limits() (reqBytes, respBytes int)
	Record(l *model.CallLog, reqBody, respBody []byte, reqTruncated, respTruncated bool)
}

func New(cfg *config.Config, db *gorm.DB, cipher *crypto.Cipher, st *settings.Store, logs *logstore.Store) (*Gateway, error) {
	g := &Gateway{
		cfg: cfg, db: db, cipher: cipher, settings: st, logs: logs,
		keys:     newKeyCache(db),
		health:   newHealthTracker(db),
		quota:    newQuotaTracker(db),
		groups:   newCounterMap(),
		apikeys:  newCounterMap(),
		accounts: newCounterMap(),
	}
	g.snap = snapshotHolder{db: db, cipher: cipher}
	perf := st.Get().Performance
	g.gate = newGate(perf.MaxConcurrency, perf.QueueSize)
	g.buildTransport(perf)
	if err := g.Reload(); err != nil {
		return nil, err
	}
	st.OnApply(func(all settings.All) {
		g.gate.configure(all.Performance.MaxConcurrency, all.Performance.QueueSize)
		g.buildTransport(all.Performance)
		_ = g.Reload()
	})
	go g.quotaRefresher()
	return g, nil
}

func (g *Gateway) buildTransport(perf settings.Performance) {
	connTimeout := time.Duration(perf.UpstreamConnTimeout) * time.Second
	if connTimeout <= 0 {
		connTimeout = 10 * time.Second
	}
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: connTimeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          2000,
		MaxIdleConnsPerHost:   256,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: time.Duration(max(perf.RequestTimeoutSec, 30)) * time.Second,
		ForceAttemptHTTP2:     true,
		ReadBufferSize:        64 * 1024,
		WriteBufferSize:       64 * 1024,
	}
	if g.cfg.HTTPProxy != "" {
		if pu, err := url.Parse(g.cfg.HTTPProxy); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	g.client.Store(&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	if old := g.transport.Swap(tr); old != nil {
		// In-flight requests keep their own reference; only idle connections are dropped.
		old.CloseIdleConnections()
	}
}

// Reload rebuilds the routing snapshot and invalidates caches.
func (g *Gateway) Reload() error {
	sr := g.settings.Get().SmartRoute
	if err := g.snap.rebuild(sr.VirtualModel, sr.Enabled); err != nil {
		slog.Error("rebuild snapshot", "err", err)
		return err
	}
	g.keys.invalidate()
	return nil
}

func (g *Gateway) InvalidateKeys()          { g.keys.invalidate() }
func (g *Gateway) ResetHealth(id uint)      { g.health.reset(id) }
func (g *Gateway) SetRouter(r Router)       { g.router.Store(&r) }
func (g *Gateway) SetChecker(c Checker)     { g.checker.Store(&c) }
func (g *Gateway) Snapshot() *Snapshot      { return g.snap.get() }
func (g *Gateway) HTTPClient() *http.Client { return g.client.Load() }
func (g *Gateway) Cipher() *crypto.Cipher   { return g.cipher }
func (g *Gateway) GroupUsage(id uint) int64 { return g.quota.usage(id) }

func (g *Gateway) quotaRefresher() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		g.quota.refresh()
	}
}

// ---- Live metrics for the overview page ----

type AccountLoad struct {
	ID       uint       `json:"id"`
	Name     string     `json:"name"`
	Provider string     `json:"provider"`
	Priority int        `json:"priority"`
	Current  int64      `json:"current"`
	Limit    int64      `json:"limit"`
	Util     float64    `json:"util"`
	Health   string     `json:"health"`
	Until    *time.Time `json:"cooldown_until,omitempty"`
	LastErr  string     `json:"last_error,omitempty"`
}

type GroupLoad struct {
	ID      uint    `json:"id"`
	Name    string  `json:"name"`
	Current int64   `json:"current"`
	Limit   int64   `json:"limit"`
	Util    float64 `json:"util"`
	Used    int64   `json:"tokens_used"`
	Quota   int64   `json:"token_quota"`
}

type LiveStats struct {
	ActiveUsers int           `json:"active_users"`
	Streams     int64         `json:"streams"`
	Inflight    int           `json:"inflight"`
	Limit       int           `json:"limit"`
	Waiting     int           `json:"waiting"`
	QueueSize   int           `json:"queue_size"`
	Accounts    []AccountLoad `json:"accounts"`
	Groups      []GroupLoad   `json:"groups"`
}

func (g *Gateway) Live() LiveStats {
	inflight, limit, waiting, queue := g.gate.stats()
	ls := LiveStats{ActiveUsers: g.activeUsers.count(), Streams: g.streamCount.Load(),
		Inflight: inflight, Limit: limit, Waiting: waiting, QueueSize: queue}
	snap := g.snap.get()
	accSnap := g.accounts.snapshot()
	for _, u := range snap.Accounts {
		cur := accSnap[u.ID]
		st, until, lastErr := g.health.state(u.ID)
		al := AccountLoad{ID: u.ID, Name: u.Name, Provider: u.Provider, Priority: u.Priority, Current: cur[0], Limit: int64(u.MaxConcurrency), Health: st, LastErr: lastErr}
		if !until.IsZero() {
			al.Until = &until
		}
		if al.Limit > 0 {
			al.Util = float64(al.Current) / float64(al.Limit)
		}
		ls.Accounts = append(ls.Accounts, al)
	}
	grpSnap := g.groups.snapshot()
	for _, gv := range snap.Groups {
		cur := grpSnap[gv.ID]
		gl := GroupLoad{ID: gv.ID, Name: gv.Name, Current: cur[0], Limit: int64(gv.MaxConcurrency), Used: g.quota.usage(gv.ID), Quota: gv.TokenQuota}
		if gl.Limit > 0 {
			gl.Util = float64(gl.Current) / float64(gl.Limit)
		}
		ls.Groups = append(ls.Groups, gl)
	}
	if ls.Accounts == nil {
		ls.Accounts = []AccountLoad{}
	}
	if ls.Groups == nil {
		ls.Groups = []GroupLoad{}
	}
	return ls
}

// activeSet tracks users seen in the last 5 minutes.
type activeSet struct {
	m atomicMap
}

func (a *activeSet) touch(id uint) { a.m.store(id, time.Now()) }

func (a *activeSet) count() int {
	cut := time.Now().Add(-5 * time.Minute)
	n := 0
	a.m.rangeFn(func(k uint, t time.Time) {
		if t.After(cut) {
			n++
		} else {
			a.m.delete(k)
		}
	})
	return n
}
