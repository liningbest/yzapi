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
	p, e := g.authenticate(r)
	if e != nil {
		writeError(w, true, e)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeError(w, true, newErr(400, "invalid_body", "Could not read request body"))
		return
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil || raw == nil {
		writeError(w, true, ErrBadJSON)
		return
	}
	var name string
	_ = json.Unmarshal(raw["model"], &name)
	snap := g.snap.get()
	grp := snap.Groups[p.GroupID]
	if grp == nil || !grp.Enabled {
		grp = snap.DefaultGroup
	}
	canonical, _, ok := snap.Resolve(strings.TrimSpace(name), model.TypeText)
	if !ok {
		writeError(w, true, ErrModelNotFound)
		return
	}
	if grp != nil && grp.Allowed != nil && !grp.Allowed[canonical] {
		writeError(w, true, ErrModelNotAllowed)
		return
	}
	cands := []string{canonical}
	if mg := snap.GroupByName(canonical); mg != nil {
		cands = mg.Models
	}
	for _, cand := range cands {
		ups := snap.UpstreamsFor(cand)
		if len(ups) == 0 {
			ups = snap.PassthroughFor(model.TypeText)
		}
		for _, up := range ups {
			if !up.HasProtocol(model.ProtoAnthropicMessages) || !g.health.available(up.ID) {
				continue
			}
			mb, _ := json.Marshal(up.mapModel(cand))
			raw["model"] = mb
			out, _ := json.Marshal(raw)
			if g.proxyCountTokens(w, r, up, out) {
				return
			}
		}
	}
	// No Anthropic upstream: a rough estimate keeps clients functional and is labelled as such.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Token-Count-Estimated", "true")
	_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": len(body) / 4})
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
	canonical, _, ok := snap.Resolve(id, "")
	if !ok || (grp != nil && grp.Allowed != nil && !grp.Allowed[canonical] && !grp.ModelGroupNames[canonical]) {
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
	// Pass-through name: report it as available.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(modelEntry(ModelInfo{Name: canonical, Type: model.TypeText, Kind: "model"}, snap.BuiltAt))
}

// modelEntry carries both OpenAI (object/created/owned_by) and Anthropic
// (display_name/created_at) fields so either SDK's model listing parses it.
func modelEntry(mi ModelInfo, at time.Time) map[string]any {
	owner := mi.Provider
	if owner == "" {
		owner = "yzapi"
	}
	return map[string]any{
		"id": mi.Name, "object": "model", "created": at.Unix(), "owned_by": owner,
		"type": mi.Type, "display_name": mi.Name, "created_at": at.UTC().Format(time.RFC3339),
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
