package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"yzapi/internal/model"
)

func TestEventHasContentPerProtocol(t *testing.T) {
	cases := []struct {
		proto string
		ev    string
		want  bool
	}{
		{model.ProtoOpenAIChat, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`, false},
		{model.ProtoOpenAIChat, `data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}`, true},
		{model.ProtoOpenAIChat, `data: {"choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`, true},
		{model.ProtoOpenAIChat, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f"}}]}}]}`, true},
		{model.ProtoOpenAIChat, `data: {"choices":[],"usage":{"prompt_tokens":1}}`, false},
		{model.ProtoOpenAIChat, `data: [DONE]`, false},
		{model.ProtoAnthropicMessages, "event: message_start\ndata: {\"type\":\"message_start\"}", false},
		{model.ProtoAnthropicMessages, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\",\"text\":\"\"}}", false},
		{model.ProtoAnthropicMessages, "event: ping\ndata: {\"type\":\"ping\"}", false},
		{model.ProtoAnthropicMessages, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"t\"}}", true},
		{model.ProtoOpenAIResponses, "event: response.created\ndata: {\"type\":\"response.created\"}", false},
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"h\"}", true},
		{model.ProtoOpenAIResponses, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\"}", true},
		{model.ProtoGemini, `data: {"candidates":[{"content":{"role":"model","parts":[]}}]}`, false},
		{model.ProtoGemini, `data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`, true},
		{model.ProtoGemini, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"f"}}]}}]}`, true},
	}
	for _, c := range cases {
		if got := eventHasContent(c.proto, []byte(c.ev)); got != c.want {
			t.Errorf("%s %q: got %v want %v", c.proto, c.ev, got, c.want)
		}
	}
}

// The writer fires once, on the first content event, even when events arrive split
// across writes, and passes bytes through unchanged.
func TestFirstContentWriterFiresOnce(t *testing.T) {
	var out bytes.Buffer
	fired := 0
	fc := &firstContentWriter{w: &out, proto: model.ProtoOpenAIChat, onFirst: func() { fired++ }}
	parts := []string{"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n", "data: {\"choices\":[{\"index\":0,\"delta\":{\"con",
		"tent\":\"hi\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"there\"}}]}\n\n", "data: [DONE]\n\n"}
	for _, p := range parts {
		if _, err := fc.Write([]byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	if fired != 1 || !fc.found || fc.buf != nil {
		t.Fatalf("fired=%d found=%v buf=%q", fired, fc.found, fc.buf)
	}
	if out.String() != parts[0]+parts[1]+parts[2]+parts[3] {
		t.Fatal("bytes must pass through unchanged")
	}
}

// End to end: a stream records first_content_ms after the upstream headers, for a
// same-protocol chat stream and for an Anthropic client on a chat upstream; a
// non-stream request records it when the body goes out; queue_wait_ms is recorded.
func TestTimingsRecorded(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n")
		fl.Flush()
		time.Sleep(30 * time.Millisecond) // headers and the role chunk are out; content follows later
		_, _ = io.WriteString(w, sseOK)
	}))
	defer up.Close()
	e := newE2E(t, up.URL)
	if w := e.chat(t, context.Background(), true); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	l, _ := e.callLog(t)
	if !l.Stream || l.FirstContentMs < l.FirstByteMs+20 || l.QueueWaitMs < 0 {
		t.Fatalf("chat stream timings: first_byte=%d first_content=%d queue=%d", l.FirstByteMs, l.FirstContentMs, l.QueueWaitMs)
	}
	if w := e.call(t, "/v1/messages", `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	l, _ = e.lastLog(t, 2)
	if l.FirstContentMs < l.FirstByteMs+20 {
		t.Fatalf("converted stream timings: first_byte=%d first_content=%d", l.FirstByteMs, l.FirstContentMs)
	}
	up2 := jsonUpstream(200, ok200)
	defer up2.Close()
	e2 := newE2E(t, up2.URL)
	if w := e2.chat(t, context.Background(), false); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	l, _ = e2.callLog(t)
	if l.FirstContentMs < l.FirstByteMs {
		t.Fatalf("non-stream timings: first_byte=%d first_content=%d", l.FirstByteMs, l.FirstContentMs)
	}
}
