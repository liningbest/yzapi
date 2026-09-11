package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"yzapi/internal/gateway/convert"
	"yzapi/internal/metrics"
	"yzapi/internal/model"
	"yzapi/internal/provider"
)

// request carries per-request state through the pipeline.
type request struct {
	id        string
	proto     string // client wire protocol
	apiType   string
	anthropic bool // error format
	w         http.ResponseWriter
	r         *http.Request
	principal *Principal
	group     *GroupView
	snap      *Snapshot

	raw          map[string]json.RawMessage
	body         []byte
	model        string
	stream       bool
	includeUsage bool
	text         string
	msgCount     int

	start    time.Time
	log      *model.CallLog
	attempts []attemptRecord
	wrote    bool
	capture  *capWriter
	reserved int64 // body memory reserved from the budget
}

func (g *Gateway) newRequest(w http.ResponseWriter, r *http.Request, proto string) *request {
	req := &request{
		id: uuid.NewString(), proto: proto, apiType: provider.ProtocolType(proto),
		anthropic: proto == model.ProtoAnthropicMessages, w: w, r: r, start: time.Now(),
	}
	req.log = &model.CallLog{RequestID: req.id, APIType: req.apiType, ClientProtocol: proto, ClientIP: clientIP(r), CreatedAt: req.start}
	w.Header().Set("X-Request-Id", req.id)
	return req
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if i := strings.IndexByte(xf, ','); i > 0 {
			return strings.TrimSpace(xf[:i])
		}
		return strings.TrimSpace(xf)
	}
	if xr := r.Header.Get("X-Real-Ip"); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (g *Gateway) fail(req *request, e *GatewayError) {
	if !req.wrote {
		writeError(req.w, req.anthropic, e)
		req.wrote = true
	}
	req.log.StatusCode = e.Status
	req.log.Error = e.Message
	switch {
	case e == ErrContentBlocked:
		req.log.Result = "blocked"
	case e.Status == 429:
		req.log.Result = "rate_limited"
	case e.Status >= 500:
		req.log.Result = "upstream_error"
	default:
		req.log.Result = "client_error"
	}
	g.finish(req)
}

func (g *Gateway) finish(req *request) {
	if req.reserved > 0 {
		g.bodyBudget.release(req.reserved)
		req.reserved = 0
	}
	l := req.log
	l.LatencyMs = time.Since(req.start).Milliseconds()
	// Request-level usage is always derived from the per-attempt records, whatever path
	// led here: retries keep their consumption, error bodies keep their tokens and any
	// attempt whose consumption is undeterminable taints the whole request.
	l.UsageStatus, l.PromptTokens, l.CompletionTokens = usageFromAttempts(req.attempts)
	l.TotalTokens = l.PromptTokens + l.CompletionTokens
	l.TokensKnown = l.UsageStatus == model.UsageConfirmed
	if l.UsageStatus == model.UsagePartial || l.UsageStatus == model.UsageUnknown {
		l.EstPromptTokens = int64(len(req.body)) / 4
	}
	g.Metrics.Requests.With(metrics.Label("result", l.Result) + "," + metrics.Label("api_type", l.APIType)).Inc()
	g.Metrics.Latency.Observe(float64(l.LatencyMs) / 1000)
	if l.TotalTokens > 0 {
		g.Metrics.Tokens.With(metrics.Label("kind", "prompt")).Add(l.PromptTokens)
		g.Metrics.Tokens.With(metrics.Label("kind", "completion")).Add(l.CompletionTokens)
		g.Metrics.Tokens.With(metrics.Label("kind", "cached")).Add(l.CachedTokens)
	}
	g.priceAttempts(req)
	if len(req.attempts) > 0 {
		b, _ := json.Marshal(req.attempts)
		l.Attempts = model.JSON(b)
	}
	if req.principal != nil {
		l.UserID, l.Username, l.APIKeyID, l.APIKeyName = req.principal.UserID, req.principal.Username, req.principal.KeyID, req.principal.KeyName
	}
	if req.group != nil {
		l.GroupID, l.GroupName = req.group.ID, req.group.Name
	}
	if l.TotalTokens > 0 && req.group != nil {
		g.quota.add(req.group.ID, l.TotalTokens)
		g.rates.addTokens("g:"+strconv.FormatUint(uint64(req.group.ID), 10), l.TotalTokens)
	}
	if l.TotalTokens > 0 && req.principal != nil {
		g.rates.addTokens("k:"+strconv.FormatUint(uint64(req.principal.KeyID), 10), l.TotalTokens)
	}
	g.logs.Record(l)
	if g.BodySink != nil && g.BodySink.Enabled() {
		reqLimit, _ := g.BodySink.Limits()
		rb, rt := req.body, false
		if len(rb) > reqLimit {
			rb, rt = rb[:reqLimit], true
		}
		var resp []byte
		respTrunc := false
		if req.capture != nil {
			resp, respTrunc = req.capture.buf, req.capture.truncated
		}
		g.BodySink.Record(l, rb, resp, rt, respTrunc)
	}
}

// timedWriter accumulates the wall time spent inside Write calls to the client.
type timedWriter struct {
	w     io.Writer
	spent time.Duration
}

func (t *timedWriter) Write(p []byte) (int, error) {
	s := time.Now()
	n, err := t.w.Write(p)
	t.spent += time.Since(s)
	return n, err
}

// capWriter passes writes through while keeping a bounded copy.
type capWriter struct {
	w         io.Writer
	buf       []byte
	limit     int
	truncated bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	if room := c.limit - len(c.buf); room > 0 {
		if len(p) <= room {
			c.buf = append(c.buf, p...)
		} else {
			c.buf = append(c.buf, p[:room]...)
			c.truncated = true
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return c.w.Write(p)
}

// authenticate resolves the API key from Authorization or x-api-key.
func (g *Gateway) authenticate(r *http.Request) (*Principal, *GatewayError) {
	key := ""
	if a := r.Header.Get("Authorization"); a != "" {
		if strings.HasPrefix(strings.ToLower(a), "bearer ") {
			key = strings.TrimSpace(a[7:])
		}
	}
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("x-api-key"))
	}
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("api-key")) // Azure-style clients
	}
	if key == "" || !strings.HasPrefix(key, "sk-") {
		return nil, ErrUnauthorized
	}
	p := g.keys.lookup(key)
	if p == nil {
		return nil, ErrUnauthorized
	}
	if !p.KeyEnabled {
		return nil, ErrKeyDisabled
	}
	if p.KeyExpired {
		return nil, ErrKeyExpired
	}
	if !p.UserEnabled {
		return nil, ErrUserDisabled
	}
	return p, nil
}

// prepare runs the shared front half of the pipeline: auth, body, model, authorization, quota.
func (g *Gateway) prepare(req *request) *GatewayError {
	p, e := g.authenticate(req.r)
	if e != nil {
		return e
	}
	req.principal = p
	req.snap = g.snap.get()
	grp := req.snap.Groups[p.GroupID]
	if grp == nil {
		grp = req.snap.DefaultGroup
	}
	if grp == nil {
		return ErrGroupDisabled
	}
	if !grp.Enabled {
		grp = req.snap.DefaultGroup
		if grp == nil || !grp.Enabled {
			return ErrGroupDisabled
		}
	}
	req.group = grp
	g.activeUsers.touch(p.UserID)
	g.keys.touch(p.KeyID)

	perf := g.settings.Get().Performance
	limit := int64(perf.MaxBodyKB) * 1024
	if limit <= 0 {
		limit = 20 << 20
	}
	// Reserve memory for the body before reading it so a burst of large uploads cannot
	// exhaust the process; unknown lengths reserve the per-request cap.
	reserve := req.r.ContentLength
	if reserve <= 0 || reserve > limit {
		reserve = limit
	}
	if !g.bodyBudget.acquire(req.r.Context(), reserve, time.Duration(max(perf.QueueTimeoutSec, 1))*time.Second) {
		return ErrMemoryBudget
	}
	req.reserved = reserve
	body, err := io.ReadAll(http.MaxBytesReader(req.w, req.r.Body, limit))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return ErrBodyTooLarge
		}
		return newErr(400, "invalid_body", "Could not read request body")
	}
	req.body = body
	if err := json.Unmarshal(body, &req.raw); err != nil || req.raw == nil {
		return ErrBadJSON
	}
	_ = json.Unmarshal(req.raw["model"], &req.model)
	req.model = strings.TrimSpace(req.model)
	if req.model == "" {
		return newErr(400, "missing_model", "The 'model' field is required")
	}
	_ = json.Unmarshal(req.raw["stream"], &req.stream)
	if so, ok := req.raw["stream_options"]; ok {
		var opts struct {
			IncludeUsage bool `json:"include_usage"`
		}
		_ = json.Unmarshal(so, &opts)
		req.includeUsage = opts.IncludeUsage
	}
	req.log.Stream = req.stream

	// Clients send many spellings of the same model; resolve to the configured name
	// (or to a pass-through account) before authorisation so allow-lists see one name.
	canonical, mt, st := req.snap.ResolveDetail(req.model, req.apiType)
	switch st {
	case ResolveAmbiguous:
		return newErr(400, "model_ambiguous", "Model '"+req.model+"' matches more than one configured model; use the exact configured name")
	case ResolveUnknown:
		return ErrModelNotFound
	}
	req.model = canonical
	req.log.RequestModel = req.model
	if mt != req.apiType {
		return newErr(400, "model_type_mismatch", "Model '"+req.model+"' is a "+mt+" model and cannot be used with this endpoint")
	}
	if grp.Allowed != nil && !grp.Allowed[req.model] {
		// group names and virtual model are allowed if they are inside an authorized model group
		if mg := req.snap.GroupByName(req.model); mg != nil && grp.ModelGroupNames[mg.Name] {
			// ok
		} else {
			return ErrModelNotAllowed
		}
	}
	if p.KeyModels != nil && !p.KeyModels[req.model] {
		return ErrKeyModelDenied
	}
	if g.quota.exceeded(grp.ID, grp.TokenQuota) {
		return ErrQuotaExceeded
	}
	if e := g.rates.admit("g:"+strconv.FormatUint(uint64(grp.ID), 10), grp.RequestsPerMinute, grp.TokensPerMinute); e != nil {
		req.w.Header().Set("Retry-After", "5")
		return e
	}
	if e := g.rates.admit("k:"+strconv.FormatUint(uint64(p.KeyID), 10), p.KeyRPM, p.KeyTPM); e != nil {
		req.w.Header().Set("Retry-After", "5")
		return e
	}
	return nil
}

// acquire takes gateway, group and key concurrency slots. Returns a release func.
func (g *Gateway) acquire(req *request) (func(), *GatewayError) {
	perf := g.settings.Get().Performance
	timeout := time.Duration(perf.QueueTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := g.gate.acquire(req.r.Context(), timeout); err != nil {
		if ge, ok := err.(*GatewayError); ok {
			return nil, ge
		}
		return nil, ErrQueueTimeout
	}
	grpCtr := g.groups.get(req.group.ID)
	if !grpCtr.tryAcquire(int64(req.group.MaxConcurrency)) {
		g.gate.release()
		return nil, ErrGroupBusy
	}
	keyLimit := req.group.KeyMaxConcurrency
	if req.group.MaxConcurrency > 0 && (keyLimit <= 0 || keyLimit > req.group.MaxConcurrency) {
		keyLimit = req.group.MaxConcurrency
	}
	keyCtr := g.apikeys.get(req.principal.KeyID)
	if !keyCtr.tryAcquire(int64(keyLimit)) {
		grpCtr.release()
		g.gate.release()
		return nil, ErrKeyBusy
	}
	return func() {
		keyCtr.release()
		grpCtr.release()
		g.gate.release()
	}, nil
}

// candidates expands the requested model into an ordered list of concrete models.
func (g *Gateway) candidates(req *request) ([]string, *GatewayError) {
	snap := req.snap
	if snap.VirtualModel != "" && req.model == snap.VirtualModel {
		sr := g.settings.Get().SmartRoute
		res := RouteResult{Label: "simple", Source: "fallback", GroupID: sr.SimpleGroupID}
		if rp := g.router.Load(); rp != nil && *rp != nil {
			res = (*rp).Decide(req.r.Context(), req.id, req.text, req.msgCount)
		}
		if res.GroupID == 0 {
			res.GroupID = sr.SimpleGroupID
		}
		mg := snap.ModelGroups[res.GroupID]
		req.log.RouteLabel = res.Label
		if mg == nil {
			return nil, newErr(503, "route_unconfigured", "Smart routing is enabled but no model group is configured for '"+res.Label+"' requests")
		}
		req.log.ModelGroup = mg.Name
		if g.DecisionLogger != nil {
			sel := ""
			if len(mg.Models) > 0 {
				sel = mg.Models[0]
			}
			g.DecisionLogger(&model.RouteDecision{RequestID: req.id, Label: res.Label, Source: res.Source, Confidence: res.Confidence,
				SelectedModel: sel, ModelGroup: mg.Name, NormalizedText: truncate(res.Normalized, 2000), TopK: res.TopK,
				RequestType: shortProto(req.proto), LatencyMs: res.LatencyMs, CreatedAt: time.Now()})
		}
		return mg.Models, nil
	}
	if mg := snap.GroupByName(req.model); mg != nil {
		req.log.ModelGroup = mg.Name
		return mg.Models, nil
	}
	req.log.ModelGroup = snap.ModelGroupNameFor(req.group, req.model)
	return []string{req.model}, nil
}

// shortProto maps a wire protocol to the short request type shown in the UI.
func shortProto(p string) string {
	switch p {
	case model.ProtoOpenAIResponses:
		return "responses"
	case model.ProtoAnthropicMessages:
		return "messages"
	default:
		return "chat"
	}
}

// pickProto chooses the wire protocol to use against an upstream.
func pickProto(up *Upstream, clientProto string, conversion bool) string {
	if up.HasProtocol(clientProto) {
		return clientProto
	}
	if !conversion || provider.ProtocolType(clientProto) != model.TypeText {
		return ""
	}
	for _, p := range []string{model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropicMessages} {
		if up.HasProtocol(p) {
			return p
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (g *Gateway) HandleChat(w http.ResponseWriter, r *http.Request) {
	g.handleText(w, r, model.ProtoOpenAIChat)
}
func (g *Gateway) HandleResponses(w http.ResponseWriter, r *http.Request) {
	g.handleText(w, r, model.ProtoOpenAIResponses)
}
func (g *Gateway) HandleMessages(w http.ResponseWriter, r *http.Request) {
	g.handleText(w, r, model.ProtoAnthropicMessages)
}
func (g *Gateway) HandleEmbeddings(w http.ResponseWriter, r *http.Request) {
	g.handleSimple(w, r, model.ProtoOpenAIEmbeddings)
}
func (g *Gateway) HandleImages(w http.ResponseWriter, r *http.Request) {
	g.handleSimple(w, r, model.ProtoOpenAIImages)
}

// HandleModels lists models visible to the caller.
func (g *Gateway) HandleModels(w http.ResponseWriter, r *http.Request) {
	p, e := g.authenticate(r)
	if e != nil {
		writeError(w, false, e)
		return
	}
	snap := g.snap.get()
	grp := snap.Groups[p.GroupID]
	if grp == nil || !grp.Enabled {
		grp = snap.DefaultGroup
	}
	out := []map[string]any{}
	for _, mi := range snap.Models {
		if grp != nil && grp.Allowed != nil && !grp.Allowed[mi.Name] {
			if mi.Kind != "group" || !grp.ModelGroupNames[mi.Name] {
				continue
			}
		}
		out = append(out, modelEntry(mi, snap.BuiltAt))
	}
	// Both list envelopes: OpenAI reads object/data, the Anthropic SDK reads data/has_more.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": out, "has_more": false})
}

func (g *Gateway) handleText(w http.ResponseWriter, r *http.Request, proto string) {
	req := g.newRequest(w, r, proto)
	if e := g.prepare(req); e != nil {
		g.fail(req, e)
		return
	}
	req.text, req.msgCount = extractText(proto, req.raw)

	// Concurrency slots are taken before the (potentially expensive) compliance and
	// routing stages so pre-processing is bounded by the same limits as forwarding.
	release, e := g.acquire(req)
	if e != nil {
		g.fail(req, e)
		return
	}
	defer release()

	// content compliance
	if cp := g.checker.Load(); cp != nil && *cp != nil && g.settings.Get().Compliance.Enabled {
		checkText := req.text
		if g.settings.Get().Compliance.CheckSystemPrompt {
			if sys := extractSystem(proto, req.raw); sys != "" {
				checkText = sys + "\n" + checkText
			}
		}
		if checkText == "" {
			checkText = "\x00" // nothing to check; keep the branch structure simple
		}
		v := (*cp).Check(r.Context(), strings.TrimSpace(strings.Trim(checkText, "\x00")))
		if v.Degraded {
			g.Metrics.ComplianceDegraded.Inc()
			now := time.Now().Unix()
			g.Metrics.lastDegraded.Store(now)
			// Record the degradation in the audit log at most once every 10s to avoid floods.
			if last := g.Metrics.lastDegradedLog.Load(); now-last >= 10 && g.Metrics.lastDegradedLog.CompareAndSwap(last, now) && g.AuditLogger != nil {
				g.AuditLogger(&model.AuditLog{RequestID: req.id, UserID: req.principal.UserID, Username: req.principal.Username,
					RequestModel: req.model, Protocol: proto, Action: "audit", RiskLevel: "low", DetectMethod: "degraded",
					Evidence: truncate(v.DegradedReason, 500), StatusCode: 200, Snippet: truncate(req.text, 200), CreatedAt: time.Now()})
			}
			if g.settings.Get().Compliance.OnFailure == "block" && !v.Block {
				g.fail(req, ErrComplianceDown)
				return
			}
		}
		if v.Hit {
			if g.AuditLogger != nil {
				status := 200
				if v.Block {
					status = ErrContentBlocked.Status
				}
				g.AuditLogger(&model.AuditLog{RequestID: req.id, UserID: req.principal.UserID, Username: req.principal.Username,
					RequestModel: req.model, Protocol: proto, Action: v.Action, RiskLevel: v.RiskLevel, DetectMethod: v.DetectMethod,
					PolicyGroupID: v.PolicyID, PolicyGroup: v.PolicyGroup, Evidence: truncate(v.Evidence, 500), Confidence: v.Confidence,
					StatusCode: status, Hits: v.Hits, Snippet: truncate(req.text, 500), CreatedAt: time.Now()})
			}
			if v.Block {
				g.Metrics.ComplianceBlocked.Inc()
				g.fail(req, ErrContentBlocked)
				return
			}
		}
	}

	cands, e := g.candidates(req)
	if e != nil {
		g.fail(req, e)
		return
	}
	g.forward(req, cands)
}

func (g *Gateway) handleSimple(w http.ResponseWriter, r *http.Request, proto string) {
	req := g.newRequest(w, r, proto)
	if e := g.prepare(req); e != nil {
		g.fail(req, e)
		return
	}
	req.stream = false
	cands, e := g.candidates(req)
	if e != nil {
		g.fail(req, e)
		return
	}
	release, e := g.acquire(req)
	if e != nil {
		g.fail(req, e)
		return
	}
	defer release()
	g.forward(req, cands)
}

// forward tries candidate models and accounts in order until one succeeds.
func (g *Gateway) forward(req *request, cands []string) {
	all := g.settings.Get()
	perf := all.Performance
	conversion := all.Basic.ProtocolConversion
	maxTries := perf.MaxRetries
	if maxTries <= 0 {
		maxTries = 3
	}
	cooldown := time.Duration(perf.CooldownSec) * time.Second
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	tries := 0
	var lastMsg string
	var lastStatus int

	var lastRetryAfter time.Duration
	for _, cand := range cands {
		ups := req.snap.UpstreamsFor(cand)
		if len(ups) == 0 {
			ups = req.snap.PassthroughFor(req.apiType) // unmapped name: accounts that take anything
		}
		for _, up := range orderUpstreams(ups) {
			if tries >= maxTries {
				break
			}
			if !g.health.available(up.ID) {
				continue
			}
			proto := pickProto(up, req.proto, conversion)
			if proto == "" {
				continue
			}
			ctr := g.accounts.get(up.ID)
			if !ctr.tryAcquire(int64(up.MaxConcurrency)) {
				continue
			}
			tries++
			upstreamModel := up.mapModel(cand)
			rec := attemptRecord{AccountID: up.ID, AccountName: up.Name, Provider: up.Provider, Protocol: proto, Model: upstreamModel}
			body, dropUsage, err := g.buildBody(req, proto, upstreamModel)
			if err != nil {
				ctr.release()
				rec.Error = "conversion: " + err.Error()
				req.attempts = append(req.attempts, rec)
				lastMsg, lastStatus = rec.Error, 400
				continue
			}

			ctx, cancel := context.WithCancel(req.r.Context())
			if !req.stream && perf.RequestTimeoutSec > 0 {
				ctx, cancel = context.WithTimeout(req.r.Context(), time.Duration(perf.RequestTimeoutSec)*time.Second)
			}
			t0 := time.Now()
			sent := false
			resp, err := g.doUpstream(ctx, &upstreamCall{up: up, proto: proto, body: body, stream: req.stream, headers: req.r.Header, sent: &sent})
			// Older OpenAI-compatible servers reject stream_options; retry once without the injection.
			if err == nil && resp.StatusCode == 400 && dropUsage {
				msg, _ := readErrorBody(resp)
				if strings.Contains(msg, "stream_options") {
					if b2, e2 := stripStreamOptions(body); e2 == nil {
						body, dropUsage = b2, false
						resp, err = g.doUpstream(ctx, &upstreamCall{up: up, proto: proto, body: body, stream: req.stream, headers: req.r.Header, sent: &sent})
					}
				} else {
					resp.Body = io.NopCloser(strings.NewReader(msg))
					resp.ContentLength = int64(len(msg))
				}
			}
			rec.LatencyMs = time.Since(t0).Milliseconds()
			if err != nil {
				cancel()
				ctr.release()
				if req.r.Context().Err() != nil {
					rec.Error = "client disconnected"
					// The client went away, but the upstream may already have processed the request.
					rec.UsageStatus = networkFailureUsage(err, sent)
					req.attempts = append(req.attempts, rec)
					req.log.Result = "client_error"
					req.log.Error = "client disconnected"
					req.log.StatusCode = 499
					req.wrote = true
					g.finish(req)
					return
				}
				rec.Error = err.Error()
				rec.UsageStatus = networkFailureUsage(err, sent)
				req.attempts = append(req.attempts, rec)
				g.Metrics.UpstreamAttempts.With(metrics.Label("outcome", "network")).Inc()
				g.health.fail(up.ID, cooldown, err.Error())
				lastMsg, lastStatus = err.Error(), 502
				continue
			}
			rec.StatusCode = resp.StatusCode
			if resp.StatusCode >= 300 {
				retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
				msg, raw := readErrorBody(resp)
				cancel()
				ctr.release()
				rec.Error = msg
				// Some providers report tokens even on error responses; keep them.
				if u, ok := usageFromJSON(proto, raw); ok && u.PromptTokens+u.CompletionTokens > 0 {
					rec.UsageStatus = model.UsageConfirmed
					rec.PromptTokens, rec.CompletionTokens = int64(u.PromptTokens), int64(u.CompletionTokens)
				} else {
					rec.UsageStatus = httpFailureUsage(resp.StatusCode)
				}
				req.attempts = append(req.attempts, rec)
				req.log.UpstreamLatencyMs += rec.LatencyMs
				if retryable(resp.StatusCode) {
					g.Metrics.UpstreamAttempts.With(metrics.Label("outcome", failureKind(resp.StatusCode))).Inc()
					switch {
					case resp.StatusCode == 404:
						// Model missing at this upstream: try the next account but do not
						// penalise the account, other models on it may be fine.
					case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 402:
						// Credential / billing problem: retrying soon is pointless.
						g.health.failFor(up.ID, credentialCooldown, "credentials rejected: "+msg)
					case resp.StatusCode == 429 && retryAfter > 0:
						lastRetryAfter = retryAfter
						g.health.failFor(up.ID, retryAfter, "rate limited (Retry-After): "+msg)
					default:
						g.health.fail(up.ID, cooldown, msg)
					}
					lastMsg, lastStatus = msg, resp.StatusCode
					continue
				}
				// Non-retryable client error: relay it in the client's format.
				g.health.ok(up.ID)
				req.log.AccountID, req.log.AccountName, req.log.Provider = up.ID, up.Name, up.Provider
				req.log.UpstreamModel, req.log.UpstreamProtocol = upstreamModel, proto
				if proto == req.proto && json.Valid(raw) {
					setUpstreamHeaders(req.w, up, upstreamModel, proto)
					req.w.Header().Set("Content-Type", "application/json")
					req.w.WriteHeader(resp.StatusCode)
					_, _ = req.w.Write(raw)
					req.wrote = true
					req.log.StatusCode, req.log.Error, req.log.Result = resp.StatusCode, msg, "client_error"
					g.finish(req)
					return
				}
				g.fail(req, newErr(resp.StatusCode, "upstream_rejected", msg))
				return
			}

			// Success path. The upstream has processed the request, so until a usable
			// usage block is seen this attempt's consumption is undeterminable.
			g.Metrics.UpstreamAttempts.With(metrics.Label("outcome", "ok")).Inc()
			rec.UsageStatus = model.UsageUnknown
			req.attempts = append(req.attempts, rec)
			req.log.AccountID, req.log.AccountName, req.log.Provider = up.ID, up.Name, up.Provider
			req.log.UpstreamModel, req.log.UpstreamProtocol = upstreamModel, proto
			req.log.FirstByteMs = time.Since(req.start).Milliseconds()
			g.health.ok(up.ID)
			setUpstreamHeaders(req.w, up, upstreamModel, proto)
			g.relay(req, resp, proto, dropUsage, cancel, t0)
			ctr.release()
			return
		}
	}
	if lastStatus == 0 {
		g.fail(req, ErrNoUpstream)
		return
	}
	status := 502
	switch lastStatus {
	case 429:
		status = 429
		if lastRetryAfter > 0 {
			// Let well-behaved clients (all coding agents back off on this) wait the right time.
			req.w.Header().Set("Retry-After", strconv.Itoa(int(lastRetryAfter/time.Second)))
		}
	case 400:
		status = 400 // conversion refused the request (e.g. stateful Responses fields)
	}
	g.fail(req, newErr(status, "upstream_failed", "All upstream attempts failed: "+truncate(lastMsg, 300)))
}

// priceAttempts estimates each attempt's cost with the configured price table and folds
// the sum into the request. Cached prompt tokens belong to the attempt that produced the
// response (the last one with usage). A request is "cost known" only when every attempt
// that consumed tokens had a price.
func (g *Gateway) priceAttempts(req *request) {
	pp := g.pricer.Load()
	if pp == nil || *pp == nil {
		return
	}
	l := req.log
	last := -1
	for i, a := range req.attempts {
		if a.PromptTokens+a.CompletionTokens > 0 {
			last = i
		}
	}
	if last < 0 {
		return
	}
	known := true
	var total int64
	for i := range req.attempts {
		a := &req.attempts[i]
		if a.PromptTokens+a.CompletionTokens == 0 {
			continue
		}
		if i == last {
			a.CachedTokens = l.CachedTokens
		}
		micros, ok := (*pp).Cost(a.Provider, a.Model, a.PromptTokens, a.CompletionTokens, a.CachedTokens)
		a.CostMicros, a.CostKnown = micros, ok
		if !ok {
			known = false
		}
		total += micros
	}
	l.CostMicros, l.CostKnown = total, known
}

// setUpstreamHeaders tells the client which upstream actually served the request, so
// client-side troubleshooting does not need the admin log.
func setUpstreamHeaders(w http.ResponseWriter, up *Upstream, upstreamModel, proto string) {
	h := w.Header()
	h.Set("X-Upstream-Account", up.Name)
	h.Set("X-Upstream-Model", upstreamModel)
	h.Set("X-Upstream-Protocol", proto)
}

// orderUpstreams keeps priority tiers in order (lower first) and, inside a tier, draws
// accounts in weighted-random order, so equal-priority accounts share load by weight and
// a small weight acts as a canary. Input is already sorted by priority.
func orderUpstreams(ups []*Upstream) []*Upstream {
	if len(ups) < 2 {
		return ups
	}
	out := make([]*Upstream, 0, len(ups))
	for i := 0; i < len(ups); {
		j := i + 1
		for j < len(ups) && ups[j].Priority == ups[i].Priority {
			j++
		}
		tier := append([]*Upstream(nil), ups[i:j]...)
		for len(tier) > 0 {
			total := 0
			for _, u := range tier {
				total += max(u.Weight, 1)
			}
			r := rand.IntN(total)
			for k, u := range tier {
				r -= max(u.Weight, 1)
				if r < 0 {
					out = append(out, u)
					tier = append(tier[:k], tier[k+1:]...)
					break
				}
			}
		}
		i = j
	}
	return out
}

// networkFailureUsage classifies a transport error: a request that was never written
// to the upstream (dial / DNS failure, or cancelled before sending) consumed nothing;
// anything after the request went out is undeterminable.
func networkFailureUsage(err error, sent bool) string {
	if !sent {
		return model.UsageNone
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return model.UsageNone
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return model.UsageNone
	}
	return model.UsageUnknown
}

// httpFailureUsage classifies an error response without usage: 4xx means the upstream
// rejected the request before generating; 5xx may have happened after work was done.
func httpFailureUsage(status int) string {
	if status >= 500 {
		return model.UsageUnknown
	}
	return model.UsageNone
}

// usageFromAttempts folds attempt-level usage into the request-level view: every known
// token is summed across attempts (a failed first try still cost tokens), an undeterminable
// attempt makes the whole request "unknown", a cut-short stream makes it "partial", and
// only requests no upstream ever processed are "none". Attempt records are never
// modified, so the fold can be repeated safely.
func usageFromAttempts(attempts []attemptRecord) (status string, prompt, completion int64) {
	var hasUnknown, hasPartial, hasConfirmed bool
	for _, a := range attempts {
		switch a.UsageStatus {
		case model.UsageConfirmed:
			hasConfirmed = true
			prompt += a.PromptTokens
			completion += a.CompletionTokens
		case model.UsagePartial:
			hasPartial = true
			prompt += a.PromptTokens
			completion += a.CompletionTokens
		case model.UsageUnknown:
			hasUnknown = true
		}
	}
	switch {
	case hasUnknown:
		status = model.UsageUnknown
	case hasPartial:
		status = model.UsagePartial
	case hasConfirmed:
		status = model.UsageConfirmed
	default:
		status = model.UsageNone
	}
	return status, prompt, completion
}

// credentialCooldown is applied when an upstream rejects the account's credentials.
const credentialCooldown = 10 * time.Minute

// parseRetryAfter reads an HTTP Retry-After header (seconds or HTTP date), capped at 15 minutes.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	}
	if d <= 0 {
		return 0
	}
	if d > 15*time.Minute {
		d = 15 * time.Minute
	}
	return d
}

func failureKind(status int) string {
	switch {
	case status == 401 || status == 403 || status == 402:
		return "credentials"
	case status == 429:
		return "rate_limited"
	case status == 404:
		return "model_missing"
	case status >= 500:
		return "server_error"
	}
	return "other"
}

// stripStreamOptions removes the injected stream_options field.
func stripStreamOptions(body []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	delete(raw, "stream_options")
	return json.Marshal(raw)
}

// buildBody produces the upstream request body for the chosen protocol.
func (g *Gateway) buildBody(req *request, proto, upstreamModel string) (body []byte, dropUsage bool, err error) {
	if proto == req.proto {
		raw := make(map[string]json.RawMessage, len(req.raw)+1)
		for k, v := range req.raw {
			raw[k] = v
		}
		mb, _ := json.Marshal(upstreamModel)
		raw["model"] = mb
		if proto == model.ProtoOpenAIChat && req.stream && !req.includeUsage {
			raw["stream_options"] = json.RawMessage(`{"include_usage":true}`)
			dropUsage = true
		}
		b, e := json.Marshal(raw)
		return b, dropUsage, e
	}
	// Normalise to a ChatRequest first.
	var chat *convert.ChatRequest
	switch req.proto {
	case model.ProtoOpenAIChat:
		chat = &convert.ChatRequest{}
		if e := json.Unmarshal(req.body, chat); e != nil {
			return nil, false, e
		}
	case model.ProtoAnthropicMessages:
		var ar convert.AnthropicRequest
		if e := json.Unmarshal(req.body, &ar); e != nil {
			return nil, false, e
		}
		chat, err = convert.AnthropicToChatRequest(&ar, upstreamModel)
	case model.ProtoOpenAIResponses:
		if v, ok := req.raw["previous_response_id"]; ok && string(v) != "null" && string(v) != `""` {
			return nil, false, errors.New("previous_response_id needs a native Responses upstream; conversion to another protocol is stateless")
		}
		var rr convert.ResponsesRequest
		if e := json.Unmarshal(req.body, &rr); e != nil {
			return nil, false, e
		}
		chat, err = convert.ResponsesToChatRequest(&rr, upstreamModel)
	}
	if err != nil {
		return nil, false, err
	}
	chat.Model = upstreamModel
	switch proto {
	case model.ProtoOpenAIChat:
		if req.stream {
			chat.StreamOptions = json.RawMessage(`{"include_usage":true}`)
		}
		b, e := json.Marshal(chat)
		return b, false, e
	case model.ProtoAnthropicMessages:
		ar, e := convert.ChatToAnthropicRequest(chat, upstreamModel)
		if e != nil {
			return nil, false, e
		}
		b, e := json.Marshal(ar)
		return b, false, e
	case model.ProtoOpenAIResponses:
		rr, e := convert.ChatToResponsesRequest(chat, upstreamModel)
		if e != nil {
			return nil, false, e
		}
		b, e := json.Marshal(rr)
		return b, false, e
	}
	return nil, false, errors.New("unsupported protocol")
}

// relay streams or copies the upstream response to the client, converting when needed.
func (g *Gateway) relay(req *request, resp *http.Response, upProto string, dropUsage bool, cancel context.CancelFunc, t0 time.Time) {
	defer cancel()
	defer resp.Body.Close()
	perf := g.settings.Get().Performance
	w := req.w
	var dst io.Writer = w
	if g.BodySink != nil && g.BodySink.Enabled() {
		_, respLimit := g.BodySink.Limits()
		req.capture = &capWriter{w: w, limit: respLimit}
		dst = req.capture
	}
	flusher, _ := w.(http.Flusher)
	// Time spent blocked on the client side is measured separately so the log can tell
	// "upstream was slow" from "client / reverse proxy was slow to accept bytes".
	tw := &timedWriter{w: dst}
	dst = tw
	flush := func() {
		if flusher != nil {
			t := time.Now()
			flusher.Flush()
			tw.spent += time.Since(t)
		}
	}
	ct := resp.Header.Get("Content-Type")
	isSSE := strings.HasPrefix(ct, "text/event-stream")

	if req.stream && isSSE {
		g.streamCount.Add(1)
		defer g.streamCount.Add(-1)
		var body io.ReadCloser = resp.Body
		if perf.StreamIdleTimeout > 0 {
			body = newIdleReader(resp.Body, time.Duration(perf.StreamIdleTimeout)*time.Second, cancel)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		req.wrote = true
		flush()

		var usage convert.Usage
		var known bool
		var err error
		var up *convert.Usage
		switch {
		case upProto == req.proto:
			usage, known, err = passthroughStream(body, dst, flush, upProto, dropUsage)
		case upProto == model.ProtoOpenAIChat && req.proto == model.ProtoAnthropicMessages:
			up, err = convert.ChatStreamToAnthropic(body, dst, flush, req.model)
		case upProto == model.ProtoOpenAIChat && req.proto == model.ProtoOpenAIResponses:
			up, err = convert.ChatStreamToResponses(body, dst, flush, req.model)
		case upProto == model.ProtoAnthropicMessages && req.proto == model.ProtoOpenAIChat:
			up, err = convert.AnthropicStreamToChat(body, dst, flush, req.model, req.includeUsage)
		case upProto == model.ProtoOpenAIResponses && req.proto == model.ProtoOpenAIChat:
			up, err = convert.ResponsesStreamToChat(body, dst, flush, req.model, req.includeUsage)
		case upProto == model.ProtoAnthropicMessages && req.proto == model.ProtoOpenAIResponses:
			up, err = chainStream(body, dst, flush, req.model,
				func(r io.Reader, pw io.Writer) (*convert.Usage, error) {
					return convert.AnthropicStreamToChat(r, pw, func() {}, req.model, true)
				},
				func(r io.Reader) (*convert.Usage, error) {
					return convert.ChatStreamToResponses(r, dst, flush, req.model)
				})
		case upProto == model.ProtoOpenAIResponses && req.proto == model.ProtoAnthropicMessages:
			up, err = chainStream(body, dst, flush, req.model,
				func(r io.Reader, pw io.Writer) (*convert.Usage, error) {
					return convert.ResponsesStreamToChat(r, pw, func() {}, req.model, true)
				},
				func(r io.Reader) (*convert.Usage, error) {
					return convert.ChatStreamToAnthropic(r, dst, flush, req.model)
				})
		default:
			usage, known, err = passthroughStream(body, dst, flush, upProto, dropUsage)
		}
		if up != nil {
			usage, known = *up, up.TotalTokens > 0 || up.PromptTokens > 0
		}
		req.log.UpstreamLatencyMs += time.Since(t0).Milliseconds()
		req.log.ClientWriteMs = tw.spent.Milliseconds()
		setUsage(req, usage, known, err == nil)
		switch {
		case err == nil:
			req.log.Result, req.log.StatusCode = "success", 200
		case req.r.Context().Err() != nil:
			req.log.Result, req.log.StatusCode, req.log.Error = "client_error", 499, "client disconnected"
		case errors.Is(err, convert.ErrIncomplete):
			// Bytes already reached the client; record the truncation instead of pretending success.
			req.log.Result, req.log.StatusCode, req.log.Error = "upstream_error", 200, err.Error()
			g.health.fail(req.log.AccountID, time.Duration(perf.CooldownSec)*time.Second, err.Error())
		default:
			req.log.Result, req.log.StatusCode, req.log.Error = "upstream_error", 200, truncate(err.Error(), 500)
		}
		g.finish(req)
		return
	}

	// Non-streaming (or upstream ignored stream flag).
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	req.log.UpstreamLatencyMs += time.Since(t0).Milliseconds()
	if err != nil {
		// Attempt stays "unknown": the upstream answered 2xx, so it consumed the request.
		g.fail(req, newErr(502, "upstream_read_failed", "Failed reading upstream response: "+err.Error()))
		return
	}
	if req.stream && !isSSE {
		// Client wanted a stream but upstream answered with JSON: fall through and send JSON.
		req.stream = false
	}
	// Book the reported usage first so a conversion failure cannot lose it.
	u, ok := usageFromJSON(upProto, raw)
	setUsage(req, u, ok, true)
	out := raw
	if upProto != req.proto {
		out, err = convertResponse(raw, upProto, req.proto, req.model)
		if err != nil {
			g.fail(req, newErr(502, "conversion_failed", "Failed converting upstream response: "+err.Error()))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = dst.Write(out)
	req.wrote = true
	req.log.Result, req.log.StatusCode = "success", 200
	g.finish(req)
}

// chainStream pipes a first conversion stage into a second one.
func chainStream(body io.Reader, w io.Writer, flush func(), model string,
	first func(io.Reader, io.Writer) (*convert.Usage, error),
	second func(io.Reader) (*convert.Usage, error)) (*convert.Usage, error) {
	pr, pw := io.Pipe()
	var firstUsage *convert.Usage
	var firstErr error
	go func() {
		firstUsage, firstErr = first(body, pw)
		pw.Close()
	}()
	u, err := second(pr)
	pr.Close()
	if err == nil && firstErr != nil {
		err = firstErr
	}
	if u == nil || u.TotalTokens == 0 {
		u = firstUsage
	}
	return u, err
}

func convertResponse(raw []byte, upProto, clientProto, model string) ([]byte, error) {
	var chat *convert.ChatResponse
	switch upProto {
	case "openai-completions":
		chat = &convert.ChatResponse{}
		if err := json.Unmarshal(raw, chat); err != nil {
			return nil, err
		}
	case "anthropic-messages":
		var ar convert.AnthropicResponse
		if err := json.Unmarshal(raw, &ar); err != nil {
			return nil, err
		}
		chat = convert.AnthropicToChatResponse(&ar, model)
	case "openai-responses":
		var err error
		chat, err = convert.ResponsesToChatResponse(raw, model)
		if err != nil {
			return nil, err
		}
	}
	chat.Model = model
	switch clientProto {
	case "openai-completions":
		return json.Marshal(chat)
	case "anthropic-messages":
		return json.Marshal(convert.ChatToAnthropicResponse(chat, model))
	case "openai-responses":
		return json.Marshal(convert.ChatToResponsesResponse(chat, model))
	}
	return raw, nil
}

// setUsage records the usage reported by the attempt that produced the response on that
// attempt's record (the last one) and classifies how trustworthy it is. complete is
// false when the upstream stream ended before its terminal event. The request-level
// numbers are derived later in finish() from all attempts.
func setUsage(req *request, u convert.Usage, known bool, complete bool) {
	if len(req.attempts) == 0 {
		return
	}
	a := &req.attempts[len(req.attempts)-1]
	a.PromptTokens = int64(u.PromptTokens)
	a.CompletionTokens = int64(u.CompletionTokens)
	if u.PromptTokensDetails != nil {
		req.log.CachedTokens = int64(u.PromptTokensDetails.CachedTokens)
	}
	total := a.PromptTokens + a.CompletionTokens
	if total == 0 && u.TotalTokens > 0 {
		total = int64(u.TotalTokens)
		a.PromptTokens = total
	}
	switch {
	case known && total > 0 && complete:
		a.UsageStatus = model.UsageConfirmed
	case known && total > 0:
		a.UsageStatus = model.UsagePartial // e.g. prompt tokens seen, output cut short
	default:
		a.UsageStatus = model.UsageUnknown // the upstream processed the request but reported nothing
	}
}

// extractSystem returns system / developer / instructions text for compliance checks.
func extractSystem(proto string, raw map[string]json.RawMessage) string {
	switch proto {
	case model.ProtoAnthropicMessages:
		var s string
		if json.Unmarshal(raw["system"], &s) == nil {
			return s
		}
		var blocks []convert.AnthropicContentBlock
		_ = json.Unmarshal(raw["system"], &blocks)
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
				sb.WriteByte('\n')
			}
		}
		return strings.TrimSpace(sb.String())
	case model.ProtoOpenAIResponses:
		var s string
		_ = json.Unmarshal(raw["instructions"], &s)
		return s
	default:
		var msgs []convert.ChatMessage
		_ = json.Unmarshal(raw["messages"], &msgs)
		var sb strings.Builder
		for _, m := range msgs {
			if m.Role == "system" || m.Role == "developer" {
				var s string
				if json.Unmarshal(m.Content, &s) == nil {
					sb.WriteString(s)
				} else {
					var parts []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					}
					_ = json.Unmarshal(m.Content, &parts)
					for _, p := range parts {
						if p.Type == "text" {
							sb.WriteString(p.Text)
						}
					}
				}
				sb.WriteByte('\n')
			}
		}
		return strings.TrimSpace(sb.String())
	}
}

// extractText pulls the latest user text and the message count for routing/compliance.
func extractText(proto string, raw map[string]json.RawMessage) (string, int) {
	switch proto {
	case model.ProtoAnthropicMessages:
		var msgs []convert.AnthropicMessage
		_ = json.Unmarshal(raw["messages"], &msgs)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role != "user" {
				continue
			}
			var s string
			if json.Unmarshal(msgs[i].Content, &s) == nil {
				return s, len(msgs)
			}
			var blocks []convert.AnthropicContentBlock
			_ = json.Unmarshal(msgs[i].Content, &blocks)
			var sb strings.Builder
			for _, b := range blocks {
				if b.Type == "text" {
					sb.WriteString(b.Text)
					sb.WriteByte('\n')
				}
			}
			if sb.Len() > 0 {
				return strings.TrimSpace(sb.String()), len(msgs)
			}
		}
		return "", len(msgs)
	case model.ProtoOpenAIResponses:
		var s string
		if json.Unmarshal(raw["input"], &s) == nil {
			return s, 1
		}
		var items []struct {
			Role    string          `json:"role"`
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(raw["input"], &items)
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Role != "user" {
				continue
			}
			var cs string
			if json.Unmarshal(items[i].Content, &cs) == nil {
				return cs, len(items)
			}
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(items[i].Content, &parts)
			var sb strings.Builder
			for _, p := range parts {
				if p.Type == "input_text" || p.Type == "text" {
					sb.WriteString(p.Text)
					sb.WriteByte('\n')
				}
			}
			if sb.Len() > 0 {
				return strings.TrimSpace(sb.String()), len(items)
			}
		}
		return "", len(items)
	default:
		var msgs []convert.ChatMessage
		_ = json.Unmarshal(raw["messages"], &msgs)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role != "user" {
				continue
			}
			var s string
			if json.Unmarshal(msgs[i].Content, &s) == nil {
				return s, len(msgs)
			}
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(msgs[i].Content, &parts)
			var sb strings.Builder
			for _, p := range parts {
				if p.Type == "text" {
					sb.WriteString(p.Text)
					sb.WriteByte('\n')
				}
			}
			if sb.Len() > 0 {
				return strings.TrimSpace(sb.String()), len(msgs)
			}
		}
		return "", len(msgs)
	}
}
