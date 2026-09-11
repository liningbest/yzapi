package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"yzapi/internal/model"
)

// firstContentWriter watches the SSE events written to the client and calls onFirst once,
// when the first event that carries generated content (text, thinking or a tool call
// delta) goes out. Role announcements, empty deltas, pings and usage-only events do not
// count. After the first hit every Write passes straight through, so the cost is a JSON
// parse per event only until content appears. Events are delimited by a blank line; a
// Write that ends mid-event is buffered until the event is complete.
type firstContentWriter struct {
	w       io.Writer
	proto   string // client wire protocol: decides what "content" looks like
	onFirst func()
	found   bool
	buf     []byte
}

func (f *firstContentWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if f.found || n == 0 {
		return n, err
	}
	f.buf = append(f.buf, p[:n]...)
	for !f.found {
		i := bytes.Index(f.buf, []byte("\n\n"))
		if i < 0 {
			break
		}
		ev := f.buf[:i]
		f.buf = f.buf[i+2:]
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
		if event == "" {
			var e struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			event = e.Type
		}
		return event == "content_block_delta"
	case model.ProtoOpenAIResponses:
		if event == "" {
			var e struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(data), &e)
			event = e.Type
		}
		switch event {
		case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
			"response.function_call_arguments.delta", "response.refusal.delta":
			return true
		}
		return false
	case model.ProtoGemini:
		var e struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text         string          `json:"text"`
						FunctionCall json.RawMessage `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal([]byte(data), &e) != nil {
			return false
		}
		for _, c := range e.Candidates {
			for _, p := range c.Content.Parts {
				if p.Text != "" || len(p.FunctionCall) > 0 {
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
			if (c.Delta.Content != nil && *c.Delta.Content != "") || c.Delta.Reasoning != "" || c.Delta.Reasoning2 != "" ||
				(len(c.Delta.ToolCalls) > 0 && string(c.Delta.ToolCalls) != "null" && string(c.Delta.ToolCalls) != "[]") {
				return true
			}
		}
		return false
	}
}
