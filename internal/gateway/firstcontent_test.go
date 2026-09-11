package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		// Review perf-bd68104: empty deltas, metadata and JSON null are not content.
		{model.ProtoAnthropicMessages, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"\"}}", false},
		{model.ProtoAnthropicMessages, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"abc\"}}", false},
		{model.ProtoAnthropicMessages, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\"}}", true},
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}", false},
		{model.ProtoOpenAIResponses, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"\"}", false},
		{model.ProtoGemini, `data: {"candidates":[{"content":{"parts":[{"functionCall":null}]}}]}`, false},
		{model.ProtoOpenAIChat, `data: {"choices":[{"index":0,"delta":{"tool_calls":null}}]}`, false},
		{model.ProtoOpenAIChat, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"","arguments":""}}]}}]}`, false},
		// Review perf-d199ab8: a tool-call name already sent to the client is content.
		{model.ProtoAnthropicMessages, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"Write\",\"input\":{}}}", true},
		{model.ProtoAnthropicMessages, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"\"}}", false},
		{model.ProtoOpenAIResponses, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"name\":\"shell\",\"arguments\":\"\"}}", true},
		{model.ProtoOpenAIResponses, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}", false},
		// Object-shaped deltas count only by their payload fields; metadata strings never count.
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":{\"text\":\"\"}}", false},
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":{\"type\":\"text\",\"text\":\"\"}}", false},
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":{\"id\":\"msg_1\",\"status\":\"in_progress\",\"text\":\"\"}}", false},
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":{\"type\":\"text\",\"text\":\"h\"}}", true},
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":[{\"kind\":\"text\",\"value\":\"\"}]}", false},
		{model.ProtoOpenAIResponses, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":[{\"kind\":\"text\",\"value\":\"h\"}]}", true},
		{model.ProtoOpenAIResponses, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":{\"arguments\":\"{\"}}", true},
		{model.ProtoOpenAIResponses, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":{\"arguments\":42}}", false},
		// CRLF-delimited streams are parsed too.
		{model.ProtoOpenAIChat, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\r", true},
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
	// CRLF delimiters.
	fired = 0
	fc = &firstContentWriter{w: io.Discard, proto: model.ProtoOpenAIChat, onFirst: func() { fired++ }}
	_, _ = fc.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\r\n\r\n"))
	if fired != 1 {
		t.Fatalf("crlf: fired=%d", fired)
	}
	// A writer that accepts nothing never fires.
	fired = 0
	fc = &firstContentWriter{w: zeroWriter{}, proto: model.ProtoOpenAIChat, onFirst: func() { fired++ }}
	_, _ = fc.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n"))
	if fired != 0 || fc.found {
		t.Fatal("must not fire when the underlying writer accepted no bytes")
	}
}

type zeroWriter struct{}

func (zeroWriter) Write(p []byte) (int, error) { return 0, io.ErrShortWrite }

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
		// Empty and metadata events that must not count as content, then the real thing.
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":null}}]}\n\n")
		fl.Flush()
		time.Sleep(30 * time.Millisecond)
		_, _ = io.WriteString(w, sseOK)
	}))
	defer up.Close()
	e := newE2E(t, up.URL)
	if w := e.chat(t, context.Background(), true); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	l, _ := e.callLog(t)
	if !l.Stream || l.FirstContentMs < l.FirstByteMs+50 || l.QueueWaitMs < 0 {
		t.Fatalf("chat stream timings: first_byte=%d first_content=%d queue=%d", l.FirstByteMs, l.FirstContentMs, l.QueueWaitMs)
	}
	if w := e.call(t, "/v1/messages", `{"model":"m","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil); w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	l, _ = e.lastLog(t, 2)
	if l.FirstContentMs < l.FirstByteMs+50 {
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

type failAfterPrefix struct{ n int }

func (w failAfterPrefix) Write(p []byte) (int, error) { return w.n, io.ErrClosedPipe }

// Review perf-298cdd9: a Write that accepted a complete content event but then failed
// must not record first content; the return values pass through unchanged.
func TestFailedWriteMustNotSetFirstContent(t *testing.T) {
	event := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
	payload := append(append([]byte{}, event...), []byte("data: trailing")...)
	fired := 0
	w := firstContentWriter{w: failAfterPrefix{len(event)}, proto: model.ProtoOpenAIChat, onFirst: func() { fired++ }}
	n, err := w.Write(payload)
	if n != len(event) || err != io.ErrClosedPipe {
		t.Fatalf("write result changed: %d %v", n, err)
	}
	if fired != 0 || w.found {
		t.Fatalf("callback fired %d time on failed Write", fired)
	}
}

func TestResponsesObjectDelta(t *testing.T) {
	if !eventHasContent(model.ProtoOpenAIResponses, []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":{\"text\":\"h\"}}")) {
		t.Fatal("object-shaped delta with content must count")
	}
	if eventHasContent(model.ProtoOpenAIResponses, []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":null}")) {
		t.Fatal("null delta must not count")
	}
}

// Cost of the observer: N empty (non-content) events before the first content event,
// then a long tail of content events that pass straight through.
func BenchmarkFirstContentWriter(b *testing.B) {
	empty := []byte("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\"}}]}\n\n")
	content := []byte("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello world\"}}]}\n\n")
	for _, emptyN := range []int{0, 10, 100} {
		b.Run(fmt.Sprintf("empty=%d", emptyN), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				fc := &firstContentWriter{w: io.Discard, proto: model.ProtoOpenAIChat, onFirst: func() {}}
				for j := 0; j < emptyN; j++ {
					_, _ = fc.Write(empty)
				}
				for j := 0; j < 200; j++ {
					_, _ = fc.Write(content)
				}
			}
		})
	}
	b.Run("plain-writer-200-events", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for j := 0; j < 200; j++ {
				_, _ = io.Discard.Write(content)
			}
		}
	})
}

// A tool call whose name arrives before its arguments: first content is the name event,
// fired once, not the later argument delta (Anthropic client on a chat upstream, where
// the converter emits content_block_start tool_use for the name).
func TestToolNameIsFirstContent(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n")
		fl.Flush()
		time.Sleep(40 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"Write\",\"arguments\":\"\"}}]}}]}\n\n")
		fl.Flush()
		time.Sleep(120 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\\\"a\\\"}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer up.Close()
	e := newE2E(t, up.URL)
	w := e.call(t, "/v1/messages", `{"model":"m","max_tokens":10,"stream":true,"tools":[{"name":"Write","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Write"`) {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	l, _ := e.callLog(t)
	gap := l.FirstContentMs - l.FirstByteMs
	if gap < 40 || gap >= 160 {
		t.Fatalf("first content must be the tool name (~40 ms after headers), not the arguments (~160 ms): gap=%d first_byte=%d first_content=%d", gap, l.FirstByteMs, l.FirstContentMs)
	}
}
