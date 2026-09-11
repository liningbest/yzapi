package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"yzapi/internal/model"
)

// firstContentWriter watches the SSE events written to the client and calls onFirst once,
// after the Write that completed the first event carrying generated content (non-empty
// text, thinking text or tool-call name / arguments) has been accepted by the underlying
// writer. Role announcements, empty deltas, signatures, pings and usage-only events do
// not count. After the first hit every Write passes straight through, so the cost is a
// JSON parse per event only until content appears. Events are delimited by a blank line
// ("\n\n" or "\r\n\r\n"); a Write that ends mid-event is buffered until complete.
// It is used on the SSE branch only; a non-streaming body is timed by the relay itself.
type firstContentWriter struct {
	w       io.Writer
	proto   string // client wire protocol: decides what "content" looks like
	onFirst func()
	found   bool
	buf     []byte
}

func (f *firstContentWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if f.found || n == 0 || err != nil {
		// Only a Write that returned success counts (a partial write that then failed is
		// not "content accepted by the connection"); the stream is over after an error.
		return n, err
	}
	f.buf = append(f.buf, p[:n]...)
	for !f.found {
		i, sep := bytes.Index(f.buf, []byte("\n\n")), 2
		if j := bytes.Index(f.buf, []byte("\r\n\r\n")); j >= 0 && (i < 0 || j < i) {
			i, sep = j, 4
		}
		if i < 0 {
			break
		}
		ev := f.buf[:i]
		f.buf = f.buf[i+sep:]
		if eventHasContent(f.proto, ev) {
			f.found = true
			f.buf = nil
			if f.onFirst != nil {
				f.onFirst()
			}
		}
	}
	if len(f.buf) > 1<<20 { // never let a malformed stream grow the buffer without bound
		f.buf = nil
	}
	return n, err
}

// eventHasContent reports whether one SSE event (without its trailing blank line)
// carries generated content in the given client protocol.
func eventHasContent(proto string, raw []byte) bool {
	event, data := "", ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			if data != "" {
				data += "\n"
			}
			data += strings.TrimSpace(line[5:])
		}
	}
	if data == "" || data == "[DONE]" {
		return false
	}
	switch proto {
	case model.ProtoAnthropicMessages:
		// Only a delta that carries text, thinking text or tool-input JSON counts;
		// signatures and other metadata deltas do not, nor does an empty text delta.
		var e struct {
			Type  string `json:"type"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &e) != nil {
			return false
		}
		if event == "" {
			event = e.Type
		}
		if event != "content_block_delta" {
			return false
		}
		switch e.Delta.Type {
		case "text_delta":
			return e.Delta.Text != ""
		case "thinking_delta":
			return e.Delta.Thinking != ""
		case "input_json_delta":
			return e.Delta.PartialJSON != ""
		}
		return false
	case model.ProtoOpenAIResponses:
		var e struct {
			Type  string          `json:"type"`
			Delta json.RawMessage `json:"delta"` // a string; tolerate an object/array shape too
		}
		if json.Unmarshal([]byte(data), &e) != nil {
			return false
		}
		if event == "" {
			event = e.Type
		}
		switch event {
		case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
			"response.function_call_arguments.delta", "response.refusal.delta":
			var s string
			if json.Unmarshal(e.Delta, &s) == nil {
				return s != ""
			}
			d := strings.TrimSpace(string(e.Delta))
			return d != "" && d != "null" && d != "{}" && d != "[]"
		}
		return false
	case model.ProtoGemini:
		var e struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text         string `json:"text"`
						FunctionCall *struct {
							Name string `json:"name"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal([]byte(data), &e) != nil {
			return false
		}
		for _, c := range e.Candidates {
			for _, p := range c.Content.Parts {
				if p.Text != "" || (p.FunctionCall != nil && p.FunctionCall.Name != "") {
					return true
				}
			}
		}
		return false
	default: // openai chat completions
		var e struct {
			Choices []struct {
				Delta struct {
					Content    *string         `json:"content"`
					Reasoning  string          `json:"reasoning_content"`
					Reasoning2 string          `json:"reasoning"`
					ToolCalls  json.RawMessage `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &e) != nil {
			return false
		}
		for _, c := range e.Choices {
			if (c.Delta.Content != nil && *c.Delta.Content != "") || c.Delta.Reasoning != "" || c.Delta.Reasoning2 != "" {
				return true
			}
			var calls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if len(c.Delta.ToolCalls) > 0 && json.Unmarshal(c.Delta.ToolCalls, &calls) == nil {
				for _, tc := range calls {
					if tc.Function.Name != "" || tc.Function.Arguments != "" {
						return true
					}
				}
			}
		}
		return false
	}
}
