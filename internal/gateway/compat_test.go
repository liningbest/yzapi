package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"yzapi/internal/model"
)

// Client-compatibility fixtures: request shapes as real coding clients send them, checked
// for verbatim pass-through on same-protocol upstreams and for the essentials on
// converted ones. Keep them close to the wire formats of the actual tools.

// recorder is a mock upstream that stores every request it received.
type recorder struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	paths   []string
	respond func(w http.ResponseWriter, r *http.Request, body []byte)
	srv     *httptest.Server
}

func newRecorder(respond func(w http.ResponseWriter, r *http.Request, body []byte)) *recorder {
	rec := &recorder{respond: respond}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, m)
		rec.headers = append(rec.headers, r.Header.Clone())
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		rec.respond(w, r, b)
	}))
	return rec
}

func (rec *recorder) last() (map[string]any, http.Header, string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := len(rec.bodies)
	if n == 0 {
		return nil, nil, ""
	}
	return rec.bodies[n-1], rec.headers[n-1], rec.paths[n-1]
}

func respondJSON(status int, body string) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

const anthropicOK = `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":3}}`
const responsesOK = `{"id":"resp_1","object":"response","status":"completed","model":"gpt","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}`

func (e *e2e) call(t *testing.T, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(context.Background())
	r.Header.Set("Authorization", "Bearer "+e.key)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	switch {
	case strings.HasSuffix(path, "/count_tokens"):
		e.g.HandleCountTokens(w, r)
	case strings.HasSuffix(path, "/messages"):
		e.g.HandleMessages(w, r)
	case strings.HasSuffix(path, "/responses"):
		e.g.HandleResponses(w, r)
	case strings.HasSuffix(path, "/embeddings"):
		e.g.HandleEmbeddings(w, r)
	default:
		e.g.HandleChat(w, r)
	}
	return w
}

// Claude Code: system as content blocks with cache_control, tools, metadata, thinking,
// anthropic-beta header. Same-protocol upstream must receive it verbatim except model.
func TestCompatClaudeCodePassthrough(t *testing.T) {
	up := newRecorder(respondJSON(200, anthropicOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages},
		Mappings: map[string]string{"claude-sonnet-4-5": "claude-sonnet-4-5-20250929"}})
	body := `{"model":"claude-sonnet-4-5","max_tokens":32000,"system":[{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"list files"}]},{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"sig=="},{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls"}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"a.go"}]}],` +
		`"tools":[{"name":"Bash","description":"run","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}],` +
		`"metadata":{"user_id":"u1"},"thinking":{"type":"enabled","budget_tokens":1024},"temperature":1,"stream":false,"x_future_field":{"a":1}}`
	w := e.call(t, "/v1/messages", body, map[string]string{"anthropic-beta": "interleaved-thinking-2025-05-14", "anthropic-version": "2023-06-01"})
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got, hdr, path := up.last()
	var want map[string]any
	_ = json.Unmarshal([]byte(body), &want)
	want["model"] = "claude-sonnet-4-5-20250929"
	if !jsonEqual(got, want) {
		t.Fatalf("upstream body differs from client body\n got=%v\nwant=%v", got, want)
	}
	if hdr.Get("anthropic-beta") != "interleaved-thinking-2025-05-14" || hdr.Get("x-api-key") != "upstream-secret" || path != "/messages" {
		t.Fatalf("headers/path: beta=%q key=%q path=%q", hdr.Get("anthropic-beta"), hdr.Get("x-api-key"), path)
	}
}

// Codex CLI: Responses API with instructions, input items, function tools, reasoning,
// store=false, prompt_cache_key, include. Native Responses upstream gets it verbatim.
func TestCompatCodexPassthrough(t *testing.T) {
	up := newRecorder(respondJSON(200, responsesOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "openai", Type: "text", Protocols: []string{model.ProtoOpenAIResponses},
		Mappings: map[string]string{"gpt-5-codex": "gpt-5-codex"}})
	body := `{"model":"gpt-5-codex","instructions":"You are Codex.","input":[{"role":"user","content":[{"type":"input_text","text":"fix tests"}]},` +
		`{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{\"cmd\":[\"ls\"]}"},{"type":"function_call_output","call_id":"call_1","output":"a.go"}],` +
		`"tools":[{"type":"function","name":"shell","strict":false,"parameters":{"type":"object"}}],"tool_choice":"auto","parallel_tool_calls":false,` +
		`"reasoning":{"effort":"medium","summary":"auto"},"store":false,"stream":false,"prompt_cache_key":"sess-1","include":["reasoning.encrypted_content"],"text":{"verbosity":"medium"}}`
	w := e.call(t, "/v1/responses", body, nil)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got, _, path := up.last()
	var want map[string]any
	_ = json.Unmarshal([]byte(body), &want)
	if !jsonEqual(got, want) || path != "/responses" {
		t.Fatalf("upstream body differs (path %s)\n got=%v\nwant=%v", path, got, want)
	}
}

// Codex against a Chat-only upstream: stateless requests convert; a stateful
// previous_response_id must be refused with a clear 400, not silently dropped.
func TestCompatCodexConversionLimits(t *testing.T) {
	up := newRecorder(respondJSON(200, ok200))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "deepseek", Type: "text", Protocols: []string{model.ProtoOpenAIChat},
		Mappings: map[string]string{"gpt-5-codex": "deepseek-chat"}})
	if w := e.call(t, "/v1/responses", `{"model":"gpt-5-codex","input":"hi","store":false}`, nil); w.Code != 200 {
		t.Fatalf("stateless conversion: %d %s", w.Code, w.Body.String())
	}
	if got, _, _ := up.last(); got["model"] != "deepseek-chat" || got["messages"] == nil {
		t.Fatalf("converted body: %v", got)
	}
	w := e.call(t, "/v1/responses", `{"model":"gpt-5-codex","input":"hi","previous_response_id":"resp_9"}`, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "previous_response_id") {
		t.Fatalf("stateful conversion must be refused clearly: %d %s", w.Code, w.Body.String())
	}
}

// OpenCode / Cline style Chat with a tool-call round trip, converted to an Anthropic
// upstream: tool ids and results must survive the conversion.
func TestCompatChatToolRoundTripToAnthropic(t *testing.T) {
	up := newRecorder(respondJSON(200, anthropicOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages},
		Mappings: map[string]string{"claude-sonnet-4-5": "claude-sonnet-4-5"}})
	body := `{"model":"anthropic/claude-sonnet-4-5","stream":false,"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"ls"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]},` +
		`{"role":"tool","tool_call_id":"call_abc","content":"a.go"}],"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}],"stream_options":{"include_usage":true}}`
	w := e.call(t, "/v1/chat/completions", body, nil)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got, _, _ := up.last()
	raw, _ := json.Marshal(got)
	s := string(raw)
	for _, want := range []string{`"tool_use"`, `"id":"call_abc"`, `"tool_result"`, `"tool_use_id":"call_abc"`, `"name":"bash"`, `"system":`} {
		if !strings.Contains(s, want) {
			t.Fatalf("converted Anthropic body lacks %s: %s", want, s)
		}
	}
	if got["model"] != "claude-sonnet-4-5" {
		t.Fatalf("vendor-prefixed model not resolved: %v", got["model"])
	}
}

// Model name spellings clients actually send must all reach the same mapping;
// ambiguous or unknown names must not.
func TestCompatModelNameResolution(t *testing.T) {
	up := newRecorder(respondJSON(200, ok200))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "openai", Type: "text", Protocols: []string{model.ProtoOpenAIChat},
		Mappings: map[string]string{"claude-sonnet-4-5": "up-sonnet", "gpt-5-codex": "up-codex", "gpt-5": "up-5", "gpt-5-mini": "up-5-mini"}})
	cases := map[string]string{
		"claude-sonnet-4-5":           "up-sonnet",
		"Claude-Sonnet-4-5":           "up-sonnet",
		"anthropic/claude-sonnet-4-5": "up-sonnet",
		"claude-sonnet-4-5-20250929":  "up-sonnet",
		"claude-sonnet-4-5-latest":    "up-sonnet",
		"openai/gpt-5-codex":          "up-codex",
		"gpt-5-codex-2025-09-15":      "up-codex",
		"gpt-5-mini-2025-08-07":       "up-5-mini", // longest configured prefix wins over gpt-5
		"models/gpt-5":                "up-5",
	}
	for in, want := range cases {
		w := e.call(t, "/v1/chat/completions", `{"model":"`+in+`","messages":[{"role":"user","content":"hi"}]}`, nil)
		if w.Code != 200 {
			t.Fatalf("%s: status %d %s", in, w.Code, w.Body.String())
		}
		if got, _, _ := up.last(); got["model"] != want {
			t.Fatalf("%s: upstream model %v want %s", in, got["model"], want)
		}
	}
	for _, in := range []string{"gpt-4o", "claude", "sonnet-4-5"} {
		if w := e.call(t, "/v1/chat/completions", `{"model":"`+in+`","messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 404 {
			t.Fatalf("%s must be rejected, got %d", in, w.Code)
		}
	}
}

// A pass-through account accepts any name and forwards it unchanged; explicit
// mappings elsewhere still win, and group allow-lists still apply.
func TestCompatPassthroughAccount(t *testing.T) {
	mapped := newRecorder(respondJSON(200, ok200))
	defer mapped.srv.Close()
	any := newRecorder(respondJSON(200, ok200))
	defer any.srv.Close()
	e := newE2EAccounts(t,
		acctSpec{URL: mapped.srv.URL, Provider: "openai", Type: "text", Protocols: []string{model.ProtoOpenAIChat}, Mappings: map[string]string{"gpt-5": "up-5"}},
		acctSpec{URL: any.srv.URL, Provider: "minimax", Type: "text", Protocols: []string{model.ProtoOpenAIChat}, Passthrough: true})
	if w := e.call(t, "/v1/chat/completions", `{"model":"MiniMax-M2","messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 200 {
		t.Fatalf("passthrough: %d %s", w.Code, w.Body.String())
	}
	if got, _, _ := any.last(); got["model"] != "MiniMax-M2" {
		t.Fatalf("passthrough must forward the name unchanged: %v", got["model"])
	}
	if w := e.call(t, "/v1/chat/completions", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if got, _, _ := mapped.last(); got["model"] != "up-5" {
		t.Fatalf("explicit mapping must win: %v", got["model"])
	}
	if w := e.call(t, "/v1/embeddings", `{"model":"text-embedding-3","input":"x"}`, nil); w.Code == 200 {
		t.Fatal("a text pass-through account must not serve embedding requests")
	}
	// Restrict bob's group to a model group: pass-through names are no longer allowed.
	mg := model.ModelGroup{Name: "only5", Type: "text", Models: model.StringList{"gpt-5"}}
	e.db.Create(&mg)
	e.db.Exec("INSERT INTO user_group_model_groups (user_group_id, model_group_id) VALUES (1, ?)", mg.ID)
	if err := e.g.Reload(); err != nil {
		t.Fatal(err)
	}
	if w := e.call(t, "/v1/chat/completions", `{"model":"MiniMax-M2","messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 403 {
		t.Fatalf("allow-listed group must not reach pass-through: %d %s", w.Code, w.Body.String())
	}
}

// count_tokens is forwarded to an Anthropic upstream with the mapped model, or estimated.
func TestCompatCountTokens(t *testing.T) {
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.URL.Path == "/messages/count_tokens" {
			respondJSON(200, `{"input_tokens":4242}`)(w, r, nil)
			return
		}
		respondJSON(200, anthropicOK)(w, r, nil)
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages},
		Mappings: map[string]string{"claude-sonnet-4-5": "claude-sonnet-4-5-20250929"}})
	w := e.call(t, "/v1/messages/count_tokens", `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`, map[string]string{"anthropic-beta": "token-counting-2024-11-01"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "4242") {
		t.Fatalf("forwarded count: %d %s", w.Code, w.Body.String())
	}
	got, hdr, _ := up.last()
	if got["model"] != "claude-sonnet-4-5-20250929" || hdr.Get("anthropic-beta") != "token-counting-2024-11-01" {
		t.Fatalf("count_tokens forward: model=%v beta=%q", got["model"], hdr.Get("anthropic-beta"))
	}
	chat := newRecorder(respondJSON(200, ok200))
	defer chat.srv.Close()
	e2 := newE2EAccounts(t, chatSpec(chat.srv.URL))
	w = e2.call(t, "/v1/messages/count_tokens", `{"model":"m","messages":[{"role":"user","content":"hello world"}]}`, nil)
	if w.Code != 200 || w.Header().Get("X-Token-Count-Estimated") != "true" || !strings.Contains(w.Body.String(), "input_tokens") {
		t.Fatalf("estimate: %d %s %v", w.Code, w.Body.String(), w.Header())
	}
}

// Model listing carries both SDKs' fields; single-model GET resolves aliases; CORS
// preflight succeeds; the api-key header authenticates.
func TestCompatModelsCorsApiKey(t *testing.T) {
	up := newRecorder(respondJSON(200, ok200))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "openai", Type: "text", Protocols: []string{model.ProtoOpenAIChat}, Mappings: map[string]string{"gpt-5": "gpt-5"}})
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("api-key", e.key)
	w := httptest.NewRecorder()
	e.g.HandleModels(w, r)
	var list struct {
		Object  string           `json:"object"`
		HasMore bool             `json:"has_more"`
		Data    []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list.Data) != 1 || list.Data[0]["display_name"] != "gpt-5" || list.Data[0]["owned_by"] != "openai" {
		t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodGet, "/v1/models/openai/gpt-5", nil)
	r.Header.Set("Authorization", "Bearer "+e.key)
	w = httptest.NewRecorder()
	e.g.HandleModel(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"gpt-5"`) {
		t.Fatalf("model by alias: %d %s", w.Code, w.Body.String())
	}
	h := CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	r = httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	r.Header.Set("Origin", "https://ide.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "anthropic-version") {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
}

// A final 429 carries the upstream's Retry-After so clients back off correctly.
func TestCompatRetryAfterPropagated(t *testing.T) {
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Retry-After", "7")
		respondJSON(429, `{"error":{"message":"rate limited"}}`)(w, r, nil)
	})
	defer up.srv.Close()
	e := newE2E(t, up.srv.URL)
	w := e.chat(t, context.Background(), false)
	if w.Code != 429 || w.Header().Get("Retry-After") != "7" {
		t.Fatalf("status %d retry-after %q", w.Code, w.Header().Get("Retry-After"))
	}
}

// Anthropic streaming pass-through: ping events and a very large single tool-input
// delta line must arrive intact and in order.
func TestCompatAnthropicStreamPingsAndLargeLines(t *testing.T) {
	big := strings.Repeat("x", 300*1024)
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n" +
		": keep-alive comment\n\n" +
		"event: ping\ndata: {\"type\":\"ping\"}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"Write\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"content\\\":\\\"" + big + "\\\"}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":9}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, stream)
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "anthropic", Type: "text", Protocols: []string{model.ProtoAnthropicMessages}, Mappings: map[string]string{"c": "c"}})
	w := e.call(t, "/v1/messages", `{"model":"c","stream":true,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, nil)
	out := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	for _, ev := range []string{"event: message_start", "event: ping", "event: content_block_delta", "event: message_stop"} {
		if !strings.Contains(out, ev) {
			t.Fatalf("missing %s in client stream", ev)
		}
	}
	if !strings.Contains(out, big) {
		t.Fatal("large tool-input delta was truncated")
	}
	if strings.Index(out, "event: ping") > strings.Index(out, "event: content_block_start") {
		t.Fatal("event order changed")
	}
	l, _ := e.callLog(t)
	if l.UsageStatus != model.UsageConfirmed || l.PromptTokens != 5 || l.CompletionTokens != 9 {
		t.Fatalf("usage from stream: %s %d/%d", l.UsageStatus, l.PromptTokens, l.CompletionTokens)
	}
}

func jsonEqual(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Event streams must never be requested compressed: gzip batches tokens into blocks and
// delays them. Non-streaming requests keep Go's default negotiation.
func TestCompatStreamRequestsRefuseCompression(t *testing.T) {
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, sseOK)
			return
		}
		respondJSON(200, ok200)(w, r, nil)
	})
	defer up.srv.Close()
	e := newE2E(t, up.srv.URL)
	if w := e.chat(t, context.Background(), true); w.Code != 200 {
		t.Fatalf("stream status %d", w.Code)
	}
	if _, hdr, _ := up.last(); hdr.Get("Accept-Encoding") != "identity" {
		t.Fatalf("streaming upstream request must send Accept-Encoding: identity, got %q", hdr.Get("Accept-Encoding"))
	}
	if w := e.chat(t, context.Background(), false); w.Code != 200 {
		t.Fatalf("json status %d", w.Code)
	}
	if _, hdr, _ := up.last(); hdr.Get("Accept-Encoding") == "identity" {
		t.Fatal("non-streaming requests should keep compression negotiation")
	}
}
