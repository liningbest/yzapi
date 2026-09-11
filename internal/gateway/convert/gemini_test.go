package convert

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestGeminiToChatRequestRoundTrip(t *testing.T) {
	src := `{"systemInstruction":{"parts":[{"text":"sys"}]},"contents":[
	 {"role":"user","parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]},
	 {"role":"model","parts":[{"text":"thinking","thought":true},{"functionCall":{"name":"f","args":{"a":1}}}]},
	 {"role":"user","parts":[{"functionResponse":{"name":"f","response":{"ok":true}}},{"text":"next"}]}],
	 "tools":[{"functionDeclarations":[{"name":"f","description":"d","parameters":{"type":"object"}}]}],
	 "toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["f"]}},
	 "generationConfig":{"temperature":0.2,"topP":0.9,"maxOutputTokens":64,"stopSequences":["END"],"responseMimeType":"application/json","responseSchema":{"type":"object"},"thinkingConfig":{"thinkingBudget":16000}}}`
	var in GeminiRequest
	if err := json.Unmarshal([]byte(src), &in); err != nil {
		t.Fatal(err)
	}
	chat, err := GeminiToChatRequest(&in, "m")
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{}
	for _, m := range chat.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,user" {
		t.Fatalf("roles %v", roles)
	}
	as := chat.Messages[2]
	if len(as.ToolCalls) != 1 || as.ToolCalls[0].Function.Name != "f" || as.ToolCalls[0].Function.Arguments != `{"a":1}` || as.Reasoning != "thinking" {
		t.Fatalf("assistant %+v", as)
	}
	if chat.Messages[3].ToolCallID != as.ToolCalls[0].ID {
		t.Fatalf("tool id %q != %q", chat.Messages[3].ToolCallID, as.ToolCalls[0].ID)
	}
	if *chat.MaxTokens != 64 || *chat.Temperature != 0.2 || chat.ReasoningEffort != "high" || string(chat.Stop) != `["END"]` {
		t.Fatalf("params %+v", chat)
	}
	if !strings.Contains(string(chat.ToolChoice), `"name":"f"`) || !strings.Contains(string(chat.ResponseFormat), "json_schema") {
		t.Fatalf("tool_choice=%s response_format=%s", chat.ToolChoice, chat.ResponseFormat)
	}

	// Back to Gemini: the structure is preserved.
	back, err := ChatToGeminiRequest(chat)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(back)
	var out GeminiRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.SystemInstruction == nil || out.SystemInstruction.Parts[0].Text != "sys" || len(out.Contents) != 4 {
		t.Fatalf("back %s", b)
	}
	if out.Contents[0].Parts[1].InlineData == nil || out.Contents[0].Parts[1].InlineData.MimeType != "image/png" {
		t.Fatalf("inline data lost: %s", b)
	}
	if out.Contents[1].Role != "model" || out.Contents[1].Parts[0].Thought != true || out.Contents[1].Parts[1].FunctionCall == nil {
		t.Fatalf("model turn: %s", b)
	}
	if out.Contents[2].Parts[0].FunctionResponse == nil || out.Contents[2].Parts[0].FunctionResponse.Name != "f" || out.Contents[3].Parts[0].Text != "next" {
		t.Fatalf("function response turn: %s", b)
	}
	if out.ToolConfig.FunctionCallingConfig.Mode != "ANY" || out.ToolConfig.FunctionCallingConfig.AllowedFunctionNames[0] != "f" {
		t.Fatalf("toolConfig: %s", b)
	}
	gc := out.GenerationConfig
	if *gc.MaxOutputTokens != 64 || gc.ResponseMimeType != "application/json" || len(gc.ResponseSchema) == 0 || gc.ThinkingConfig == nil || *gc.ThinkingConfig.ThinkingBudget != 24576 {
		t.Fatalf("generationConfig: %s", b)
	}
}

func TestChatToGeminiToolResultsGrouped(t *testing.T) {
	var chat ChatRequest
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"go"},
	 {"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"a","arguments":"{}"}},{"id":"c2","type":"function","function":{"name":"b","arguments":"not json"}}]},
	 {"role":"tool","tool_call_id":"c1","content":"plain text"},{"role":"tool","tool_call_id":"c2","content":"[1,2]"}]}`), &chat); err != nil {
		t.Fatal(err)
	}
	out, err := ChatToGeminiRequest(&chat)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var g GeminiRequest
	_ = json.Unmarshal(b, &g)
	if len(g.Contents) != 3 || len(g.Contents[2].Parts) != 2 {
		t.Fatalf("two tool results must share one user turn: %s", b)
	}
	if g.Contents[2].Parts[0].FunctionResponse.Name != "a" || !strings.Contains(string(g.Contents[2].Parts[0].FunctionResponse.Response), `"result":"plain text"`) {
		t.Fatalf("first response: %s", b)
	}
	if !strings.Contains(string(g.Contents[2].Parts[1].FunctionResponse.Response), `"result":[1,2]`) {
		t.Fatalf("array result wrapped: %s", b)
	}
	if !strings.Contains(string(g.Contents[1].Parts[1].FunctionCall.Args), `{}`) {
		t.Fatalf("bad args fall back to empty object: %s", b)
	}
}

func TestGeminiResponseConversions(t *testing.T) {
	raw := `{"candidates":[{"content":{"role":"model","parts":[{"text":"why","thought":true},{"text":"hi "},{"text":"there"},{"functionCall":{"name":"f","args":{"x":1}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3,"totalTokenCount":15,"cachedContentTokenCount":4}}`
	chat, err := GeminiToChatResponse([]byte(raw), "m")
	if err != nil {
		t.Fatal(err)
	}
	msg := chat.Choices[0].Message
	var content string
	_ = json.Unmarshal(msg.Content, &content)
	if content != "hi there" || msg.Reasoning != "why" || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Arguments != `{"x":1}` || *chat.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("chat %+v content=%q", msg, content)
	}
	if chat.Usage.PromptTokens != 10 || chat.Usage.CompletionTokens != 5 || chat.Usage.PromptTokensDetails.CachedTokens != 4 {
		t.Fatalf("usage %+v", chat.Usage)
	}
	out := ChatToGeminiResponse(chat, "m")
	b, _ := json.Marshal(out)
	var g GeminiResponse
	_ = json.Unmarshal(b, &g)
	parts := g.Candidates[0].Content.Parts
	if len(parts) != 3 || !parts[0].Thought || parts[1].Text != "hi there" || parts[2].FunctionCall == nil || g.Candidates[0].FinishReason != "STOP" {
		t.Fatalf("gemini %s", b)
	}
	if g.UsageMetadata.PromptTokenCount != 10 || g.UsageMetadata.CachedContentTokenCount != 4 {
		t.Fatalf("usage %s", b)
	}
	if _, err := GeminiToChatResponse([]byte(`{"error":{"code":400,"message":"bad","status":"INVALID_ARGUMENT"}}`), "m"); err == nil {
		t.Fatal("error body must fail conversion")
	}
	// MAX_TOKENS and safety map to length / content_filter.
	for fr, want := range map[string]string{"MAX_TOKENS": "length", "SAFETY": "content_filter"} {
		c, _ := GeminiToChatResponse([]byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"`+fr+`"}]}`), "m")
		if *c.Choices[0].FinishReason != want {
			t.Fatalf("%s -> %s", fr, *c.Choices[0].FinishReason)
		}
	}
}

func TestGeminiStreamToChat(t *testing.T) {
	src := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"t\",\"thought\":true}]},\"index\":0}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]},\"index\":0}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"f\",\"args\":{\"q\":1}}}]},\"finishReason\":\"STOP\",\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n"
	var out bytes.Buffer
	u, err := GeminiStreamToChat(strings.NewReader(src), &out, func() {}, "m", true)
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, `"reasoning_content":"t"`) || !strings.Contains(s, `"content":"a"`) || !strings.Contains(s, `"name":"f"`) ||
		!strings.Contains(s, `"finish_reason":"tool_calls"`) || !strings.HasSuffix(strings.TrimSpace(s), "data: [DONE]") {
		t.Fatalf("stream %s", s)
	}
	if u == nil || u.PromptTokens != 3 || u.CompletionTokens != 2 {
		t.Fatalf("usage %+v", u)
	}
	// No finishReason: incomplete.
	out.Reset()
	if _, err := GeminiStreamToChat(strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}]}\n\n"), &out, func() {}, "m", false); err != ErrIncomplete {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
	// Error event.
	out.Reset()
	if _, err := GeminiStreamToChat(strings.NewReader("data: {\"error\":{\"code\":429,\"message\":\"quota\"}}\n\n"), &out, func() {}, "m", false); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("want error, got %v", err)
	}
}

func TestChatStreamToGemini(t *testing.T) {
	src := "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"r\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	var out bytes.Buffer
	u, err := ChatStreamToGemini(strings.NewReader(src), &out, func() {}, "m")
	if err != nil {
		t.Fatal(err)
	}
	events := strings.Split(strings.TrimSpace(out.String()), "\n\n")
	if len(events) != 3 || !strings.Contains(events[0], `"thought":true`) || !strings.Contains(events[1], `"text":"hi"`) ||
		!strings.Contains(events[2], `"args":{"a":1}`) || !strings.Contains(events[2], `"finishReason":"STOP"`) || !strings.Contains(events[2], `"promptTokenCount":3`) {
		t.Fatalf("events %q", events)
	}
	// The usage chunk arrives after the finish chunk in OpenAI streams; the finish event is
	// held back so usageMetadata rides on it.
	if u == nil || u.PromptTokens != 3 {
		t.Fatalf("usage %+v", u)
	}
	out.Reset()
	if _, err := ChatStreamToGemini(strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n"), &out, func() {}, "m"); err != ErrIncomplete {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
}
