package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"yzapi/internal/model"
)

// Compatibility endpoints for the long tail of coding clients: Anthropic token
// counting, single-model lookup and CORS preflight. Everything here is best-effort
// and must never break the main data-plane paths.

// HandleCountTokens serves POST /v1/messages/count_tokens. When an Anthropic-protocol
// account can serve the model the request is forwarded verbatim (with the mapped
// model), otherwise a size-based estimate is returned so clients that require the
// endpoint (Claude Code's context meter) keep working.
func (g *Gateway) HandleCountTokens(w http.ResponseWriter, r *http.Request) {
	// Same front half as a generation request: auth, body limit and memory budget,
	// model resolution, group authorisation (incl. model groups), then the same
	// concurrency slots. Counting must not be a way around the data-plane limits.
	req := g.newRequest(w, r, model.ProtoAnthropicMessages)
	defer func() {
		if req.reserved > 0 {
			g.bodyBudget.release(req.reserved)
		}
	}()
	if e := g.prepare(req); e != nil {
		writeError(w, true, e)
		return
	}
	release, e := g.acquire(req)
	if e != nil {
		writeError(w, true, e)
		return
	}
	defer release()
	cands := []string{req.model}
	if mg := req.snap.GroupByName(req.model); mg != nil {
		cands = mg.Models
	}
	for _, cand := range cands {
		ups := req.snap.UpstreamsFor(cand)
		if len(ups) == 0 {
			ups = req.snap.PassthroughFor(model.TypeText)
		}
		for _, up := range ups {
			if !up.HasProtocol(model.ProtoAnthropicMessages) || !g.health.available(up.ID) {
				continue
			}
			// Same per-account slot as a generation request: a full account is skipped,
			// never sent one more request.
			ctr := g.accounts.get(up.ID)
			if !ctr.tryAcquire(int64(up.MaxConcurrency)) {
				continue
			}
			raw := make(map[string]json.RawMessage, len(req.raw))
			for k, v := range req.raw {
				raw[k] = v
			}
			mb, _ := json.Marshal(up.mapModel(cand))
			raw["model"] = mb
			out, _ := json.Marshal(raw)
			served := g.proxyCountTokens(w, r, up, out)
			ctr.release()
			if served {
				return
			}
		}
	}
	// No Anthropic upstream: a rough size-based estimate (not a metered value) keeps
	// clients functional and is labelled as such.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Token-Count-Estimated", "true")
	_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": len(req.body) / 4})
}

func (g *Gateway) proxyCountTokens(w http.ResponseWriter, r *http.Request, up *Upstream, body []byte) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, up.BaseURL+"/messages/count_tokens", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", up.APIKey)
	req.Header.Set("Authorization", "Bearer "+up.APIKey)
	ver := r.Header.Get("anthropic-version")
	if ver == "" {
		ver = "2023-06-01"
	}
	req.Header.Set("anthropic-version", ver)
	if b := r.Header.Get("anthropic-beta"); b != "" {
		req.Header.Set("anthropic-beta", b)
	}
	resp, err := g.client.Load().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode >= 500 || resp.StatusCode == 404 {
		return false // try the next account / fall back to the estimate
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
	return true
}

// HandleModel serves GET /v1/models/{id} in the OpenAI shape.
func (g *Gateway) HandleModel(w http.ResponseWriter, r *http.Request) {
	p, e := g.authenticate(r)
	if e != nil {
		writeError(w, false, e)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	snap := g.snap.get()
	grp := snap.Groups[p.GroupID]
	if grp == nil || !grp.Enabled {
		grp = snap.DefaultGroup
	}
	canonical, typ, st := snap.ResolveDetail(id, "")
	if st == ResolveAmbiguous {
		writeError(w, false, newErr(400, "model_ambiguous", "Model '"+id+"' matches more than one configured model"))
		return
	}
	if st != ResolveFound || (grp != nil && grp.Allowed != nil && !grp.Allowed[canonical] && !grp.ModelGroupNames[canonical]) {
		writeError(w, false, ErrModelNotFound)
		return
	}
	for _, mi := range snap.Models {
		if mi.Name == canonical {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(modelEntry(mi, snap.BuiltAt))
			return
		}
	}
	// Pass-through name: report it with the type of the account that would serve it.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(modelEntry(ModelInfo{Name: canonical, Type: typ, Kind: "model"}, snap.BuiltAt))
}

// modelEntry carries both OpenAI (object/created/owned_by) and Anthropic
// (type="model"/display_name/created_at) fields so either SDK's model listing parses
// it. The gateway's own category (text / embedding / image) is exposed as model_type.
func modelEntry(mi ModelInfo, at time.Time) map[string]any {
	owner := mi.Provider
	if owner == "" {
		owner = "yzapi"
	}
	return map[string]any{
		"id": mi.Name, "object": "model", "created": at.Unix(), "owned_by": owner,
		"type": "model", "display_name": mi.Name, "created_at": at.UTC().Format(time.RFC3339),
		"model_type": mi.Type, "kind": mi.Kind,
	}
}

// CORS lets browser-based clients (web IDEs, playgrounds) call the data plane directly.
// Credentials are bearer keys in headers, so a wildcard origin does not widen access.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, x-api-key, api-key, anthropic-version, anthropic-beta, OpenAI-Beta, OpenAI-Organization, OpenAI-Project, X-Requested-With")
			h.Set("Access-Control-Expose-Headers", "X-Request-Id, Retry-After, X-Token-Count-Estimated")
			h.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
