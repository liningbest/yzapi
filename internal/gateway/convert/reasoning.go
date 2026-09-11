package convert

import (
	"encoding/json"
	"sync/atomic"
)

// reasoningToContent switches how converted OpenAI Chat output carries upstream
// thinking: as the de-facto `reasoning_content` field (default) or folded into the
// assistant content as <think>…</think> for clients that only render content.
var reasoningToContent atomic.Bool

func SetReasoningToContent(on bool) { reasoningToContent.Store(on) }
func ReasoningToContent() bool      { return reasoningToContent.Load() }

const thinkOpen, thinkClose = "<think>\n", "\n</think>\n"

// foldReasoning applies the setting to a complete chat message.
func foldReasoning(m *ChatMessage) {
	if m == nil || m.Reasoning == "" || !ReasoningToContent() {
		return
	}
	// Content is a JSON string for plain text; leave multimodal arrays alone.
	var cur string
	if len(m.Content) > 0 && json.Unmarshal(m.Content, &cur) != nil {
		return
	}
	c, _ := json.Marshal(thinkOpen + m.Reasoning + thinkClose + cur)
	m.Content = c
	m.Reasoning = ""
}

// thinkState tracks an open <think> block while converting a stream.
type thinkState struct{ open bool }

// reasoning returns the chat delta payload for a piece of upstream thinking.
func (t *thinkState) reasoning(delta string) map[string]any {
	if !ReasoningToContent() {
		return map[string]any{"reasoning_content": delta}
	}
	if !t.open {
		t.open = true
		return map[string]any{"content": thinkOpen + delta}
	}
	return map[string]any{"content": delta}
}

// text returns the chat delta payload for visible text, closing an open think block.
func (t *thinkState) text(delta string) map[string]any {
	if t.open {
		t.open = false
		return map[string]any{"content": thinkClose + delta}
	}
	return map[string]any{"content": delta}
}

// closing returns the payload that closes a still-open think block at stream end, or nil.
func (t *thinkState) closing() map[string]any {
	if !t.open {
		return nil
	}
	t.open = false
	return map[string]any{"content": thinkClose}
}

// thinkingToEffort maps an Anthropic thinking budget onto an OpenAI reasoning effort.
func thinkingToEffort(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var th struct {
		Type   string `json:"type"`
		Budget int    `json:"budget_tokens"`
	}
	if json.Unmarshal(raw, &th) != nil || th.Type != "enabled" {
		return ""
	}
	switch {
	case th.Budget <= 0:
		return ""
	case th.Budget <= 2048:
		return "low"
	case th.Budget <= 8192:
		return "medium"
	default:
		return "high"
	}
}
