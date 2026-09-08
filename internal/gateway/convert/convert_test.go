package convert

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicToChatRequest(t *testing.T) {
	in := `{"model":"claude","max_tokens":100,"system":[{"type":"text","text":"sys"}],
	  "messages":[
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":[{"type":"text","text":"calling"},{"type":"tool_use","id":"t1","name":"f","input":{"a":1}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"42"},{"type":"text","text":"next"}]}
	  ],
	  "tools":[{"name":"f","description":"d","input_schema":{"type":"object"}}],
	  "tool_choice":{"type":"any"},"stream":true}`
	var ar AnthropicRequest
	if err := json.Unmarshal([]byte(in), &ar); err != nil {
		t.Fatal(err)
	}
	out, err := AnthropicToChatRequest(&ar, "gpt")
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "gpt" || !out.Stream || *out.MaxTokens != 100 {
		t.Fatalf("basic fields wrong: %+v", out)
	}
	roles := []string{}
	for _, m := range out.Messages {
		roles = append(roles, m.Role)
	}
	want := []string{"system", "user", "assistant", "tool", "user"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles=%v want %v", roles, want)
	}
	if out.Messages[2].ToolCalls[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("tool args: %s", out.Messages[2].ToolCalls[0].Function.Arguments)
	}
	if out.Messages[3].ToolCallID != "t1" {
		t.Fatalf("tool_call_id")
	}
	if string(out.ToolChoice) != `"required"` {
		t.Fatalf("tool_choice=%s", out.ToolChoice)
	}
	if !bytes.Contains(out.StreamOptions, []byte("include_usage")) {
		t.Fatalf("stream_options missing")
	}
}

func TestChatToAnthropicRequestMergesRoles(t *testing.T) {
	in := `{"model":"m","messages":[
	  {"role":"system","content":"s1"},{"role":"system","content":"s2"},
	  {"role":"user","content":"a"},{"role":"user","content":[{"type":"text","text":"b"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
	  {"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},
	  {"role":"tool","tool_call_id":"c1","content":"ok"}],
	  "tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"tool_choice":"auto","max_completion_tokens":50}`
	var cr ChatRequest
	if err := json.Unmarshal([]byte(in), &cr); err != nil {
		t.Fatal(err)
	}
	out, err := ChatToAnthropicRequest(&cr, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if out.MaxTokens != 50 {
		t.Fatalf("max_tokens=%d", out.MaxTokens)
	}
	var sys string
	_ = json.Unmarshal(out.System, &sys)
	if sys != "s1\n\ns2" {
		t.Fatalf("system=%q", sys)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("messages=%d want 3 (merged user, assistant, tool_result user)", len(out.Messages))
	}
	var first []AnthropicContentBlock
	_ = json.Unmarshal(out.Messages[0].Content, &first)
	if len(first) != 3 || first[2].Type != "image" || first[2].Source.MediaType != "image/png" {
		t.Fatalf("merged user blocks: %+v", first)
	}
	var last []AnthropicContentBlock
	_ = json.Unmarshal(out.Messages[2].Content, &last)
	if last[0].Type != "tool_result" || last[0].ToolUseID != "c1" {
		t.Fatalf("tool_result: %+v", last)
	}
	if string(out.ToolChoice) != `{"type":"auto"}` {
		t.Fatalf("tool_choice=%s", out.ToolChoice)
	}
}

func TestChatStreamToAnthropic(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"f","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":1}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"
	var out bytes.Buffer
	u, err := ChatStreamToAnthropic(strings.NewReader(sse), &out, func() {}, "claude")
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"event: message_start", `"text":"Hel"`, `"type":"text_delta"`, `"id":"call_1"`, `"name":"f"`, `"type":"tool_use"`,
		`"partial_json":"{\"a\":1}"`, `"type":"input_json_delta"`, `"stop_reason":"tool_use"`, "event: message_stop"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if u == nil || u.CompletionTokens != 3 {
		t.Fatalf("usage=%+v", u)
	}
	// content_block_stop for text must precede tool block start
	if strings.Index(s, `event: content_block_stop`) > strings.Index(s, `"type":"tool_use"`) {
		t.Fatalf("text block not closed before tool block")
	}
}

func TestAnthropicStreamToChat(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":7,"output_tokens":0,"cache_read_input_tokens":2}}}`,
		"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}`,
		"event: content_block_start\ndata: " + `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"f","input":{}}}`,
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"x\":"}}`,
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"2}"}}`,
		"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		"event: message_stop\ndata: " + `{"type":"message_stop"}`,
	}, "\n\n") + "\n\n"
	var out bytes.Buffer
	u, err := AnthropicStreamToChat(strings.NewReader(sse), &out, func() {}, "gpt", true)
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{`"role":"assistant"`, `"content":"Hi"`, `"id":"tu_1"`, `"arguments":"{\"x\":"`, `"finish_reason":"tool_calls"`, `"usage"`, "data: [DONE]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if u.PromptTokens != 9 || u.CompletionTokens != 4 || u.PromptTokensDetails == nil || u.PromptTokensDetails.CachedTokens != 2 {
		t.Fatalf("usage=%+v", u)
	}
}

func TestResponsesRoundTrip(t *testing.T) {
	in := `{"model":"m","instructions":"be nice","input":[{"role":"user","content":[{"type":"input_text","text":"q"}]},
	  {"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"o"}],
	  "tools":[{"type":"function","name":"f","parameters":{"type":"object"}}],"max_output_tokens":10}`
	var rr ResponsesRequest
	if err := json.Unmarshal([]byte(in), &rr); err != nil {
		t.Fatal(err)
	}
	cr, err := ResponsesToChatRequest(&rr, "gpt")
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Messages) != 4 || cr.Messages[0].Role != "system" || cr.Messages[2].ToolCalls[0].ID != "c1" || cr.Messages[3].Role != "tool" {
		t.Fatalf("messages: %+v", cr.Messages)
	}
	fr := "tool_calls"
	resp := &ChatResponse{ID: "chatcmpl-9", Choices: []ChatChoice{{Message: &ChatMessage{Role: "assistant", Content: json.RawMessage(`""`),
		ToolCalls: []ChatToolCall{{ID: "c2", Type: "function"}}}, FinishReason: &fr}}, Usage: &Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}}
	out := ChatToResponsesResponse(resp, "gpt")
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"type":"function_call"`) || !strings.Contains(string(b), `"call_id":"c2"`) {
		t.Fatalf("responses output: %s", b)
	}
}

func TestSSEReader(t *testing.T) {
	rd := NewSSEReader(strings.NewReader("event: a\ndata: 1\ndata: 2\n\n: comment\n\ndata: x\n"))
	ev, err := rd.Next()
	if err != nil || ev.Event != "a" || ev.Data != "1\n2" {
		t.Fatalf("ev=%+v err=%v", ev, err)
	}
	ev, err = rd.Next()
	if err != nil || ev.Data != "x" {
		t.Fatalf("ev=%+v err=%v", ev, err)
	}
}
