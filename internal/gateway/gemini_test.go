package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yzapi/internal/model"
)

const geminiOK = `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3,"totalTokenCount":15,"cachedContentTokenCount":4},"modelVersion":"gemini-2.5-pro"}`

// Gemini CLI: systemInstruction, contents with functionCall/functionResponse, tools with
// functionDeclarations, toolConfig, generationConfig with thinkingConfig. A native Gemini
// upstream gets the body verbatim, the model in the URL and only x-goog-api-key.
func TestGeminiPassthroughNativeUpstream(t *testing.T) {
	up := newRecorder(respondJSON(200, geminiOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "gemini", Type: "text", Protocols: []string{model.ProtoGemini},
		Mappings: map[string]string{"gemini-2.5-pro": "gemini-2.5-pro-preview"}})
	body := `{"systemInstruction":{"parts":[{"text":"You are Gemini CLI."}]},` +
		`"contents":[{"role":"user","parts":[{"text":"list files"}]},{"role":"model","parts":[{"functionCall":{"name":"run_shell_command","args":{"command":"ls"}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"name":"run_shell_command","response":{"output":"a.go"}}}]}],` +
		`"tools":[{"functionDeclarations":[{"name":"run_shell_command","description":"run","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}]}],` +
		`"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}},"generationConfig":{"temperature":0,"thinkingConfig":{"thinkingBudget":-1,"includeThoughts":true}},"x_future":{"a":1}}`
	r := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:generateContent", strings.NewReader(body))
	r.Header.Set("x-goog-api-key", e.key)
	w := httptest.NewRecorder()
	e.g.HandleGemini(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got, hdr, path := up.last()
	var want map[string]any
	_ = json.Unmarshal([]byte(body), &want)
	if !jsonEqual(got, want) {
		t.Fatalf("upstream body differs from client body\n got=%v\nwant=%v", got, want)
	}
	if path != "/models/gemini-2.5-pro-preview:generateContent" {
		t.Fatalf("path %q", path)
	}
	if hdr.Get("x-goog-api-key") != "upstream-secret" || hdr.Get("Authorization") != "" {
		t.Fatalf("auth headers: goog=%q authz=%q", hdr.Get("x-goog-api-key"), hdr.Get("Authorization"))
	}
	if !strings.Contains(w.Body.String(), `"candidates"`) {
		t.Fatalf("response not Gemini-shaped: %s", w.Body.String())
	}
	log, atts := e.callLog(t)
	if log.PromptTokens != 12 || log.CompletionTokens != 3 || log.CachedTokens != 4 || log.ClientProtocol != model.ProtoGemini || log.RequestModel != "gemini-2.5-pro" {
		t.Fatalf("log %+v", log)
	}
	if len(atts) != 1 || atts[0].Protocol != model.ProtoGemini || atts[0].Model != "gemini-2.5-pro-preview" {
		t.Fatalf("attempts %+v", atts)
	}

	// Streaming: URL decides, alt=sse, and the stream's finishReason marks completion.
	up.respond = func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hel\"}]},\"index\":0}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"lo\"}]},\"finishReason\":\"STOP\",\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2,\"totalTokenCount\":7}}\n\n")
	}
	r = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse&key="+e.key, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	w = httptest.NewRecorder()
	e.g.HandleGemini(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream status %d ct=%q %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	_, _, path = up.last()
	if path != "/models/gemini-2.5-pro-preview:streamGenerateContent" {
		t.Fatalf("stream path %q", path)
	}
	log, _ = e.lastLog(t, 2)
	if !log.Stream || log.PromptTokens != 5 || log.CompletionTokens != 2 || log.Result != "success" {
		t.Fatalf("stream log %+v", log)
	}
}

// Gemini client on an OpenAI-only upstream: converted both ways, tool calls included.
func TestGeminiClientToChatUpstream(t *testing.T) {
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, body []byte) {
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if s, _ := req["stream"].(bool); s {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"he\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Beijing\\\"}\"}}]}}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4,\"total_tokens\":13}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		respondJSON(200, `{"id":"x","object":"chat.completion","model":"gpt","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Beijing\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`)(w, r, body)
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "openai", Type: "text", Protocols: []string{model.ProtoOpenAIChat},
		Mappings: map[string]string{"gemini-2.5-pro": "gpt-5"}})
	body := `{"systemInstruction":{"parts":[{"text":"be brief"}]},"contents":[{"role":"user","parts":[{"text":"weather in Beijing?"}]}],` +
		`"tools":[{"functionDeclarations":[{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]}],"generationConfig":{"maxOutputTokens":100,"temperature":0.5}}`
	w := e.call(t, "/v1beta/models/gemini-2.5-pro:generateContent", body, nil)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got, _, path := up.last()
	if path != "/chat/completions" || got["model"] != "gpt-5" {
		t.Fatalf("upstream path=%q model=%v", path, got["model"])
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "be brief" {
		t.Fatalf("messages %v", msgs)
	}
	if got["max_tokens"] != float64(100) || got["temperature"] != 0.5 || len(got["tools"].([]any)) != 1 {
		t.Fatalf("params %v", got)
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []map[string]any `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata map[string]any `json:"usageMetadata"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Candidates) != 1 {
		t.Fatalf("response %s", w.Body.String())
	}
	fc, _ := out.Candidates[0].Content.Parts[0]["functionCall"].(map[string]any)
	if fc == nil || fc["name"] != "get_weather" || fc["args"].(map[string]any)["city"] != "Beijing" || out.Candidates[0].FinishReason != "STOP" {
		t.Fatalf("candidate %+v", out.Candidates[0])
	}
	if out.UsageMetadata["promptTokenCount"] != float64(9) || out.UsageMetadata["candidatesTokenCount"] != float64(4) {
		t.Fatalf("usage %v", out.UsageMetadata)
	}
	log, _ := e.callLog(t)
	if log.PromptTokens != 9 || log.CompletionTokens != 4 {
		t.Fatalf("log %+v", log)
	}

	// Streaming conversion: text chunk, then the buffered function call with the finish.
	w = e.call(t, "/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse", body, nil)
	if w.Code != 200 {
		t.Fatalf("stream status %d %s", w.Code, w.Body.String())
	}
	events := strings.Split(strings.TrimSpace(w.Body.String()), "\n\n")
	if len(events) != 2 || !strings.Contains(events[0], `"text":"he"`) || !strings.Contains(events[1], `"functionCall"`) ||
		!strings.Contains(events[1], `"finishReason":"STOP"`) || !strings.Contains(events[1], `"promptTokenCount":9`) {
		t.Fatalf("stream events: %q", events)
	}
	got, _, _ = up.last()
	if got["stream"] != true || got["stream_options"] == nil {
		t.Fatalf("upstream stream body %v", got)
	}
	log, _ = e.lastLog(t, 2)
	if !log.Stream || log.PromptTokens != 9 || log.CompletionTokens != 4 || log.Result != "success" {
		t.Fatalf("stream log %+v", log)
	}
}

// OpenAI chat client (Cline, OpenCode) on a native Gemini upstream.
func TestChatClientToGeminiUpstream(t *testing.T) {
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, body []byte) {
		if strings.Contains(r.URL.Path, ":streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"plan\",\"thought\":true}]},\"index\":0}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\",\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2,\"thoughtsTokenCount\":3,\"totalTokenCount\":10}}\n\n")
			return
		}
		respondJSON(200, geminiOK)(w, r, body)
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "gemini", Type: "text", Protocols: []string{model.ProtoGemini},
		Mappings: map[string]string{"g": "gemini-2.5-flash"}})
	body := `{"model":"g","messages":[{"role":"system","content":"sys"},{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_9","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},{"role":"tool","tool_call_id":"call_9","content":"{\"ok\":true}"}],` +
		`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"tool_choice":"required","max_tokens":50,"response_format":{"type":"json_object"}}`
	w := e.call(t, "/v1/chat/completions", body, nil)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	got, hdr, path := up.last()
	if path != "/models/gemini-2.5-flash:generateContent" || hdr.Get("x-goog-api-key") != "upstream-secret" {
		t.Fatalf("path=%q key=%q", path, hdr.Get("x-goog-api-key"))
	}
	if _, has := got["model"]; has {
		t.Fatalf("model must not be in a Gemini body: %v", got)
	}
	contents := got["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents %v", contents)
	}
	userParts := contents[0].(map[string]any)["parts"].([]any)
	if len(userParts) != 2 || userParts[1].(map[string]any)["inlineData"].(map[string]any)["mimeType"] != "image/png" {
		t.Fatalf("user parts %v", userParts)
	}
	fr := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "f" || fr["response"].(map[string]any)["ok"] != true {
		t.Fatalf("functionResponse %v", fr)
	}
	gc := got["generationConfig"].(map[string]any)
	if gc["maxOutputTokens"] != float64(50) || gc["responseMimeType"] != "application/json" {
		t.Fatalf("generationConfig %v", gc)
	}
	if got["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"] != "ANY" {
		t.Fatalf("toolConfig %v", got["toolConfig"])
	}
	if got["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"] != "sys" {
		t.Fatalf("systemInstruction %v", got["systemInstruction"])
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Choices) != 1 || out.Choices[0].Message.Content != "hi" || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("response %s", w.Body.String())
	}
	if out.Usage["prompt_tokens"] != float64(12) || out.Usage["completion_tokens"] != float64(3) {
		t.Fatalf("usage %v", out.Usage)
	}
	log, _ := e.callLog(t)
	if log.PromptTokens != 12 || log.CompletionTokens != 3 || log.CachedTokens != 4 {
		t.Fatalf("log %+v", log)
	}

	// Streaming: thought parts become reasoning_content, thoughts count as output tokens.
	w = e.call(t, "/v1/chat/completions", `{"model":"g","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != 200 {
		t.Fatalf("stream status %d %s", w.Code, w.Body.String())
	}
	s := w.Body.String()
	if !strings.Contains(s, `"reasoning_content":"plan"`) || !strings.Contains(s, `"content":"hello"`) || !strings.Contains(s, `"finish_reason":"stop"`) || !strings.HasSuffix(strings.TrimSpace(s), "data: [DONE]") {
		t.Fatalf("stream %s", s)
	}
	log, _ = e.lastLog(t, 2)
	if !log.Stream || log.PromptTokens != 5 || log.CompletionTokens != 5 || log.Result != "success" {
		t.Fatalf("stream log %+v", log)
	}
}

// Anthropic client (Claude Code) on a native Gemini upstream goes through chat chunks.
func TestAnthropicClientToGeminiUpstreamStream(t *testing.T) {
	up := newRecorder(func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\",\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":2,\"totalTokenCount\":7}}\n\n")
	})
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "gemini", Type: "text", Protocols: []string{model.ProtoGemini}, Mappings: map[string]string{"g": "gemini-2.5-flash"}})
	w := e.call(t, "/v1/messages", `{"model":"g","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	s := w.Body.String()
	if !strings.Contains(s, "event: message_start") || !strings.Contains(s, `"text":"hello"`) || !strings.Contains(s, "event: message_stop") {
		t.Fatalf("stream %s", s)
	}
	log, _ := e.callLog(t)
	if log.PromptTokens != 5 || log.CompletionTokens != 2 || log.Result != "success" {
		t.Fatalf("log %+v", log)
	}
}

// Gemini surface: model listing / lookup shape, countTokens estimate, Gemini error format.
func TestGeminiModelsAndErrors(t *testing.T) {
	up := newRecorder(respondJSON(200, geminiOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, acctSpec{URL: up.srv.URL, Provider: "gemini", Type: "text", Protocols: []string{model.ProtoGemini}, Mappings: map[string]string{"gemini-2.5-pro": "gemini-2.5-pro"}})
	get := func(path, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if key != "" {
			r.Header.Set("x-goog-api-key", key)
		}
		w := httptest.NewRecorder()
		e.g.HandleGemini(w, r)
		return w
	}
	w := get("/v1beta/models", e.key)
	var list struct {
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Models) != 1 || list.Models[0].Name != "models/gemini-2.5-pro" || len(list.Models[0].Methods) != 3 {
		t.Fatalf("models %d %s", w.Code, w.Body.String())
	}
	if w = get("/v1beta/models/gemini-2.5-pro", e.key); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"models/gemini-2.5-pro"`) {
		t.Fatalf("model %d %s", w.Code, w.Body.String())
	}
	if w = get("/v1beta/models/nope", e.key); w.Code != 404 || !strings.Contains(w.Body.String(), `"status":"NOT_FOUND"`) {
		t.Fatalf("missing model %d %s", w.Code, w.Body.String())
	}
	if w = get("/v1beta/models", ""); w.Code != 401 || !strings.Contains(w.Body.String(), `"status":"UNAUTHENTICATED"`) || !strings.Contains(w.Body.String(), `"code":401`) {
		t.Fatalf("unauth %d %s", w.Code, w.Body.String())
	}
	w = e.call(t, "/v1beta/models/gemini-2.5-pro:countTokens", `{"contents":[{"role":"user","parts":[{"text":"hello world"}]}]}`, nil)
	var ct struct {
		TotalTokens int `json:"totalTokens"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ct) != nil || ct.TotalTokens <= 0 {
		t.Fatalf("countTokens %d %s", w.Code, w.Body.String())
	}
	if w = e.call(t, "/v1beta/models/unknown-model:generateContent", `{"contents":[]}`, nil); w.Code != 404 || !strings.Contains(w.Body.String(), `"status":"NOT_FOUND"`) {
		t.Fatalf("unknown model %d %s", w.Code, w.Body.String())
	}
	if w = e.call(t, "/v1beta/models/gemini-2.5-pro:frobnicate", `{}`, nil); w.Code != 404 {
		t.Fatalf("unknown action %d %s", w.Code, w.Body.String())
	}
}
