package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yzapi/internal/model"
)

const jevOK = `{"model":"jev-1.13","answers":{"q1":{"type":"noul","noul":0.91,"confidence":0.8}},"usage":{"input_tokens":21,"output_tokens":4}}`

func customSpec(url string, endpoints ...string) acctSpec {
	return acctSpec{URL: url, Provider: "typesafe", Type: model.TypeCustom, Protocols: []string{model.ProtoCustomJSON},
		Mappings: map[string]string{"jev": "jev-1.13"}, Endpoints: endpoints}
}

func (e *e2e) custom(path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.g.HandleCustom(w, r)
	return w
}

// A TypeSafe-style request is forwarded verbatim to <base_url>/<path> with only the model
// rewritten and the account key injected; the typed answer comes back untouched and the
// Responses-style usage names are metered.
func TestCustomEndpointPassthrough(t *testing.T) {
	up := newRecorder(respondJSON(200, jevOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"))
	body := `{"model":"jev","state":{"temp":31,"room":"kitchen"},"questions":{"q1":{"type":"noul","instructions":"Is it hot?"}},"x_future":[1,2]}`
	w := e.custom("/v1/systemone", e.key, body)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got, hdr, path := up.last()
	var want map[string]any
	_ = json.Unmarshal([]byte(body), &want)
	want["model"] = "jev-1.13"
	if !jsonEqual(got, want) {
		t.Fatalf("upstream body\n got=%v\nwant=%v", got, want)
	}
	if path != "/systemone" {
		t.Fatalf("path %q", path)
	}
	if hdr.Get("Authorization") != "Bearer upstream-secret" || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", hdr)
	}
	if w.Body.String() != jevOK+"\n" && strings.TrimSpace(w.Body.String()) != jevOK {
		t.Fatalf("response not passed through: %s", w.Body.String())
	}
	if w.Header().Get("X-Upstream-Model") != "jev-1.13" || w.Header().Get("X-Upstream-Protocol") != model.ProtoCustomJSON {
		t.Fatalf("upstream headers %v", w.Header())
	}
	log, atts := e.callLog(t)
	if log.APIType != model.TypeCustom || log.ClientProtocol != model.ProtoCustomJSON || log.UpstreamProtocol != model.ProtoCustomJSON ||
		log.RequestModel != "jev" || log.UpstreamModel != "jev-1.13" || log.Result != "success" || log.StatusCode != 200 {
		t.Fatalf("log %+v", log)
	}
	if log.PromptTokens != 21 || log.CompletionTokens != 4 || log.UsageStatus != model.UsageConfirmed {
		t.Fatalf("usage %+v", log)
	}
	if len(atts) != 1 || atts[0].Protocol != model.ProtoCustomJSON || atts[0].Model != "jev-1.13" || atts[0].UsageStatus != model.UsageConfirmed {
		t.Fatalf("attempts %+v", atts)
	}
}

// Path normalisation: a trailing slash and the stored key are the same endpoint; a
// path no custom account declares is 404 for an authenticated caller and 401 for an
// unauthenticated one, and neither reaches an upstream.
func TestCustomEndpointPathsAndUnknown(t *testing.T) {
	up := newRecorder(respondJSON(200, jevOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"))
	body := `{"model":"jev","state":"s","questions":{}}`
	if w := e.custom("/v1/systemone/", e.key, body); w.Code != 200 {
		t.Fatalf("trailing slash: %d %s", w.Code, w.Body.String())
	}
	if _, _, path := up.last(); path != "/systemone" {
		t.Fatalf("path %q", path)
	}
	w := e.custom("/v1/nothing-here", e.key, body)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"not_found"`) {
		t.Fatalf("unknown path: %d %s", w.Code, w.Body.String())
	}
	if w := e.custom("/v1/nothing-here", "", body); w.Code != 401 {
		t.Fatalf("unauthenticated unknown path: %d %s", w.Code, w.Body.String())
	}
	if w := e.custom("/v1/systemone", "wrong-key", body); w.Code != 401 {
		t.Fatalf("bad key: %d", w.Code)
	}
	if w := e.custom("/v1/", e.key, body); w.Code != 404 {
		t.Fatalf("bare /v1/: %d", w.Code)
	}
	if n := len(up.paths); n != 1 {
		t.Fatalf("upstream calls %d, want 1", n)
	}
	for in, want := range map[string]string{"/v1/systemone": "/systemone", "/v1/systemone/": "/systemone", "/v1/a/b/": "/a/b", "/v1/": "", "/v1": "", "/v2/x": "", "/api/x": ""} {
		if got := CustomEndpointPath(in); got != want {
			t.Fatalf("CustomEndpointPath(%q)=%q want %q", in, got, want)
		}
	}
}

// A custom model is refused on the chat endpoint, and a chat model is refused on a custom
// path: the account type decides the entry point.
func TestCustomModelTypeMismatch(t *testing.T) {
	up := newRecorder(respondJSON(200, jevOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"), chatSpec(up.srv.URL))
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"jev","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer "+e.key)
	w := httptest.NewRecorder()
	e.g.HandleChat(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "model_type_mismatch") {
		t.Fatalf("custom model on chat: %d %s", w.Code, w.Body.String())
	}
	if w := e.custom("/v1/systemone", e.key, `{"model":"m","state":"s","questions":{}}`); w.Code != 400 || !strings.Contains(w.Body.String(), "model_type_mismatch") {
		t.Fatalf("chat model on custom path: %d %s", w.Code, w.Body.String())
	}
	if w := e.custom("/v1/systemone", e.key, `{"state":"s","questions":{}}`); w.Code != 400 || !strings.Contains(w.Body.String(), "missing_model") {
		t.Fatalf("missing model: %d %s", w.Code, w.Body.String())
	}
	if n := len(up.paths); n != 0 {
		t.Fatalf("upstream calls %d, want 0", n)
	}
}

// Only accounts that declare the path are candidates: with two custom accounts serving
// the same model on different paths, each path reaches its own account. A passthrough
// custom account takes unmapped model names.
func TestCustomEndpointSelectsDeclaringAccount(t *testing.T) {
	a := newRecorder(respondJSON(200, jevOK))
	defer a.srv.Close()
	b := newRecorder(respondJSON(200, `{"ok":true,"usage":{"prompt_tokens":7,"completion_tokens":1}}`))
	defer b.srv.Close()
	pass := acctSpec{URL: b.srv.URL, Provider: "custom-json", Type: model.TypeCustom, Protocols: []string{model.ProtoCustomJSON},
		Mappings: map[string]string{"jev": "jev-b"}, Passthrough: true, Endpoints: []string{"/rerank"}}
	e := newE2EAccounts(t, customSpec(a.srv.URL, "/systemone"), pass)
	if w := e.custom("/v1/systemone", e.key, `{"model":"jev","state":"s","questions":{}}`); w.Code != 200 {
		t.Fatalf("systemone: %d %s", w.Code, w.Body.String())
	}
	if w := e.custom("/v1/rerank", e.key, `{"model":"jev","query":"q"}`); w.Code != 200 {
		t.Fatalf("rerank: %d %s", w.Code, w.Body.String())
	}
	if len(a.paths) != 1 || a.paths[0] != "/systemone" || len(b.paths) != 1 || b.paths[0] != "/rerank" {
		t.Fatalf("routing a=%v b=%v", a.paths, b.paths)
	}
	// Unmapped name on the passthrough account, on its own path only.
	if w := e.custom("/v1/rerank", e.key, `{"model":"rerank-v2","query":"q"}`); w.Code != 200 {
		t.Fatalf("passthrough: %d %s", w.Code, w.Body.String())
	}
	got, _, _ := b.last()
	if got["model"] != "rerank-v2" {
		t.Fatalf("passthrough model %v", got["model"])
	}
	log, _ := e.lastLog(t, 3)
	if log.PromptTokens != 7 || log.CompletionTokens != 1 || log.UpstreamModel != "rerank-v2" || log.UsageStatus != model.UsageConfirmed {
		t.Fatalf("passthrough log %+v", log)
	}
	// The passthrough account does not declare /systemone, so the unmapped name has no
	// candidate there: 503 no_available_upstream, and no upstream is called.
	if w := e.custom("/v1/systemone", e.key, `{"model":"rerank-v2","query":"q"}`); w.Code != 503 || !strings.Contains(w.Body.String(), "no_available_upstream") {
		t.Fatalf("passthrough model on the other path: %d %s", w.Code, w.Body.String())
	}
	if len(a.paths) != 1 || len(b.paths) != 2 {
		t.Fatalf("upstream calls a=%v b=%v", a.paths, b.paths)
	}
}

// Upstream failures: a 5xx cools the account down and, with no other candidate, ends as a
// 502; a 4xx is relayed unchanged with its status; a 2xx without usage is metered unknown.
func TestCustomEndpointUpstreamErrors(t *testing.T) {
	up := newRecorder(respondJSON(500, `{"error":{"message":"boom"}}`))
	defer up.srv.Close()
	e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"))
	body := `{"model":"jev","state":"s","questions":{}}`
	if w := e.custom("/v1/systemone", e.key, body); w.Code != 502 {
		t.Fatalf("5xx: %d %s", w.Code, w.Body.String())
	}
	log, atts := e.lastLog(t, 1)
	if log.Result != "upstream_error" || len(atts) != 1 || atts[0].StatusCode != 500 {
		t.Fatalf("5xx log %+v %+v", log, atts)
	}
	e.g.health.ok(atts[0].AccountID) // clear the cooldown for the next case
	up.respond = respondJSON(422, `{"error":{"message":"questions must not be empty","type":"invalid_request"}}`)
	w := e.custom("/v1/systemone", e.key, body)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "questions must not be empty") {
		t.Fatalf("4xx relay: %d %s", w.Code, w.Body.String())
	}
	up.respond = respondJSON(200, `{"answers":{}}`)
	if w := e.custom("/v1/systemone", e.key, body); w.Code != 200 {
		t.Fatalf("no usage: %d %s", w.Code, w.Body.String())
	}
	log, atts = e.lastLog(t, 3)
	if log.Result != "success" || log.UsageStatus != model.UsageUnknown || len(atts) != 1 || atts[0].UsageStatus != model.UsageUnknown {
		t.Fatalf("no-usage log %+v %+v", log, atts)
	}
}

// EndpointsFor is what the user console's guide shows: the union of the paths of every
// enabled account mapping the model, sorted; nothing for other model types.
func TestCustomEndpointsForModel(t *testing.T) {
	a := newRecorder(respondJSON(200, jevOK))
	defer a.srv.Close()
	b := newRecorder(respondJSON(200, jevOK))
	defer b.srv.Close()
	e := newE2EAccounts(t, customSpec(a.srv.URL, "/systemone", "/rank"), customSpec(b.srv.URL, "/rerank", "/rank"), chatSpec(b.srv.URL))
	snap := e.g.Snapshot()
	if got := snap.EndpointsFor("jev"); strings.Join(got, ",") != "/rank,/rerank,/systemone" {
		t.Fatalf("EndpointsFor(jev) = %v", got)
	}
	if got := snap.EndpointsFor("m"); len(got) != 0 {
		t.Fatalf("EndpointsFor(m) = %v", got)
	}
	if got := snap.EndpointsFor("nope"); len(got) != 0 {
		t.Fatalf("EndpointsFor(nope) = %v", got)
	}
}
