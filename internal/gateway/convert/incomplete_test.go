package convert

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A truncated upstream stream must never be turned into a successful terminal event.

func TestChatStreamToResponsesIncomplete(t *testing.T) {
	sse := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}` + "\n\n"
	var out bytes.Buffer
	_, err := ChatStreamToResponses(strings.NewReader(sse), &out, func() {}, "gpt")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err=%v want ErrIncomplete", err)
	}
	s := out.String()
	if strings.Contains(s, "event: response.completed") {
		t.Fatalf("must not emit response.completed on truncation:\n%s", s)
	}
	if !strings.Contains(s, "event: response.failed") || !strings.Contains(s, `"status":"failed"`) {
		t.Fatalf("expected response.failed with status failed:\n%s", s)
	}
}

func TestChatStreamToAnthropicIncomplete(t *testing.T) {
	sse := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}` + "\n\n"
	var out bytes.Buffer
	_, err := ChatStreamToAnthropic(strings.NewReader(sse), &out, func() {}, "claude")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err=%v want ErrIncomplete", err)
	}
	s := out.String()
	if strings.Contains(s, "event: message_stop") {
		t.Fatalf("must not emit message_stop on truncation:\n%s", s)
	}
	if !strings.Contains(s, "event: error") {
		t.Fatalf("expected error event:\n%s", s)
	}
}

func TestAnthropicStreamToChatIncomplete(t *testing.T) {
	sse := "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n"
	var out bytes.Buffer
	_, err := AnthropicStreamToChat(strings.NewReader(sse), &out, func() {}, "gpt", false)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err=%v want ErrIncomplete", err)
	}
	s := out.String()
	if strings.Contains(s, "[DONE]") {
		t.Fatalf("must not emit [DONE] on truncation:\n%s", s)
	}
	if !strings.Contains(s, `"upstream_incomplete"`) {
		t.Fatalf("expected error chunk:\n%s", s)
	}
}

func TestResponsesStreamToChatIncomplete(t *testing.T) {
	sse := "event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","delta":"Hi"}` + "\n\n"
	var out bytes.Buffer
	_, err := ResponsesStreamToChat(strings.NewReader(sse), &out, func() {}, "gpt", false)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err=%v want ErrIncomplete", err)
	}
	s := out.String()
	if strings.Contains(s, "[DONE]") || !strings.Contains(s, `"upstream_incomplete"`) {
		t.Fatalf("expected error chunk and no [DONE]:\n%s", s)
	}
}
