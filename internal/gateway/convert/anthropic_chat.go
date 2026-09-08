package convert

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Anthropic Messages request  ->  OpenAI Chat Completions request
// ---------------------------------------------------------------------------

func AnthropicToChatRequest(in *AnthropicRequest, upstreamModel string) (*ChatRequest, error) {
	out := &ChatRequest{Model: upstreamModel, Stream: in.Stream, Temperature: in.Temperature, TopP: in.TopP}
	if in.MaxTokens > 0 {
		mt := in.MaxTokens
		out.MaxTokens = &mt
	}
	if len(in.StopSequences) > 0 {
		b, _ := json.Marshal(in.StopSequences)
		out.Stop = b
	}
	// system
	if sys := anthropicSystemText(in.System); sys != "" {
		c, _ := json.Marshal(sys)
		out.Messages = append(out.Messages, ChatMessage{Role: "system", Content: c})
	}
	for _, m := range in.Messages {
		msgs, err := anthropicMessageToChat(m)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, msgs...)
	}
	for _, t := range in.Tools {
		if t.Type != "" && t.Type != "custom" && !strings.HasPrefix(t.Type, "tool") {
			// server-side tools (web_search etc.) cannot be converted
			continue
		}
		var ct ChatTool
		ct.Type = "function"
		ct.Function.Name = t.Name
		ct.Function.Description = t.Description
		ct.Function.Parameters = t.InputSchema
		if len(ct.Function.Parameters) == 0 {
			ct.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out.Tools = append(out.Tools, ct)
	}
	if len(in.ToolChoice) > 0 {
		var tc struct {
			Type                   string `json:"type"`
			Name                   string `json:"name"`
			DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
		}
		_ = json.Unmarshal(in.ToolChoice, &tc)
		switch tc.Type {
		case "auto":
			out.ToolChoice = json.RawMessage(`"auto"`)
		case "any":
			out.ToolChoice = json.RawMessage(`"required"`)
		case "none":
			out.ToolChoice = json.RawMessage(`"none"`)
		case "tool":
			b, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": tc.Name}})
			out.ToolChoice = b
		}
		if tc.DisableParallelToolUse != nil && *tc.DisableParallelToolUse {
			f := false
			out.ParallelToolCalls = &f
		}
	}
	if in.Stream {
		out.StreamOptions = json.RawMessage(`{"include_usage":true}`)
	}
	return out, nil
}

func anthropicSystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []AnthropicContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func parseAnthropicContent(raw json.RawMessage) ([]AnthropicContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []AnthropicContentBlock{{Type: "text", Text: s}}, nil
	}
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("invalid message content: %w", err)
	}
	return blocks, nil
}

func anthropicMessageToChat(m AnthropicMessage) ([]ChatMessage, error) {
	blocks, err := parseAnthropicContent(m.Content)
	if err != nil {
		return nil, err
	}
	var out []ChatMessage
	if m.Role == "assistant" {
		msg := ChatMessage{Role: "assistant"}
		var text strings.Builder
		for _, b := range blocks {
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
			case "tool_use":
				var tc ChatToolCall
				tc.ID = b.ID
				tc.Type = "function"
				tc.Function.Name = b.Name
				args := string(b.Input)
				if args == "" {
					args = "{}"
				}
				tc.Function.Arguments = args
				msg.ToolCalls = append(msg.ToolCalls, tc)
			case "thinking":
				msg.Reasoning += b.Thinking
			}
		}
		if text.Len() > 0 || len(msg.ToolCalls) == 0 {
			c, _ := json.Marshal(text.String())
			msg.Content = c
		}
		return []ChatMessage{msg}, nil
	}

	// user role: split tool_result blocks into tool messages, keep rest as one user message.
	var parts []map[string]any
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			if len(parts) > 0 {
				out = append(out, userPartsMessage(parts))
				parts = nil
			}
			out = append(out, ChatMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: toolResultContent(b.Content)})
		case "text":
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		case "image":
			if b.Source != nil {
				url := b.Source.URL
				if b.Source.Type == "base64" {
					url = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
				}
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
			}
		case "document":
			// unsupported; skip silently
		}
	}
	if len(parts) > 0 {
		out = append(out, userPartsMessage(parts))
	}
	if len(out) == 0 {
		out = append(out, ChatMessage{Role: "user", Content: json.RawMessage(`""`)})
	}
	return out, nil
}

func userPartsMessage(parts []map[string]any) ChatMessage {
	if len(parts) == 1 && parts[0]["type"] == "text" {
		c, _ := json.Marshal(parts[0]["text"])
		return ChatMessage{Role: "user", Content: c}
	}
	c, _ := json.Marshal(parts)
	return ChatMessage{Role: "user", Content: c}
}

func toolResultContent(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`""`)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		b, _ := json.Marshal(s)
		return b
	}
	var blocks []AnthropicContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		b, _ := json.Marshal(sb.String())
		return b
	}
	return raw
}

// ---------------------------------------------------------------------------
// OpenAI Chat response  ->  Anthropic Messages response
// ---------------------------------------------------------------------------

func mapFinishToStop(fr string) string {
	switch fr {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func ChatToAnthropicResponse(in *ChatResponse, model string) *AnthropicResponse {
	out := &AnthropicResponse{ID: anthropicID(in.ID), Type: "message", Role: "assistant", Model: model}
	if len(in.Choices) > 0 {
		ch := in.Choices[0]
		if ch.Message != nil {
			if ch.Message.Reasoning != "" {
				out.Content = append(out.Content, AnthropicContentBlock{Type: "thinking", Thinking: ch.Message.Reasoning})
			}
			if txt := chatContentText(ch.Message.Content); txt != "" {
				out.Content = append(out.Content, AnthropicContentBlock{Type: "text", Text: txt})
			}
			for _, tc := range ch.Message.ToolCalls {
				input := json.RawMessage(tc.Function.Arguments)
				if !json.Valid(input) || len(input) == 0 {
					input = json.RawMessage("{}")
				}
				out.Content = append(out.Content, AnthropicContentBlock{Type: "tool_use", ID: toolID(tc.ID), Name: tc.Function.Name, Input: input})
			}
		}
		if ch.FinishReason != nil {
			out.StopReason = strPtr(mapFinishToStop(*ch.FinishReason))
		} else {
			out.StopReason = strPtr("end_turn")
		}
	}
	if out.Content == nil {
		out.Content = []AnthropicContentBlock{}
	}
	if in.Usage != nil {
		out.Usage = AnthropicUsage{InputTokens: in.Usage.PromptTokens, OutputTokens: in.Usage.CompletionTokens}
		if in.Usage.PromptTokensDetails != nil {
			out.Usage.CacheReadInputTokens = in.Usage.PromptTokensDetails.CachedTokens
			out.Usage.InputTokens -= in.Usage.PromptTokensDetails.CachedTokens
			if out.Usage.InputTokens < 0 {
				out.Usage.InputTokens = 0
			}
		}
	}
	return out
}

func chatContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return ""
}

func anthropicID(id string) string {
	if id == "" {
		return "msg_" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if strings.HasPrefix(id, "msg_") {
		return id
	}
	return "msg_" + strings.TrimPrefix(id, "chatcmpl-")
}

func toolID(id string) string {
	if id == "" {
		return fmt.Sprintf("toolu_%d", time.Now().UnixNano())
	}
	return id
}

// ---------------------------------------------------------------------------
// OpenAI Chat SSE  ->  Anthropic Messages SSE
// ---------------------------------------------------------------------------

// ChatStreamToAnthropic converts a streaming chat completion into Anthropic events.
// It returns the final usage observed.
func ChatStreamToAnthropic(r io.Reader, w io.Writer, flush func(), model string) (*Usage, error) {
	rd := NewSSEReader(r)
	var (
		started     bool
		blockIndex  = -1
		textOpen    bool
		thinkOpen   bool
		toolBlocks  = map[int]int{} // tool call index -> content block index
		toolArgs    = map[int]*strings.Builder{}
		stopReason  = "end_turn"
		usage       *Usage
		msgID       = anthropicID("")
		inputTokens int
	)
	emit := func(event string, v any) error {
		b, _ := json.Marshal(v)
		if err := WriteSSE(w, event, string(b)); err != nil {
			return err
		}
		flush()
		return nil
	}
	start := func(u *Usage) error {
		if started {
			return nil
		}
		started = true
		if u != nil {
			inputTokens = u.PromptTokens
		}
		return emit("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": msgID, "type": "message", "role": "assistant", "model": model,
				"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]int{"input_tokens": inputTokens, "output_tokens": 0},
			},
		})
	}
	closeOpen := func() error {
		if textOpen || thinkOpen {
			if err := emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex}); err != nil {
				return err
			}
			textOpen, thinkOpen = false, false
		}
		return nil
	}
	closeTools := func() error {
		for _, bi := range toolBlocks {
			if err := emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": bi}); err != nil {
				return err
			}
		}
		toolBlocks = map[int]int{}
		return nil
	}

	for {
		ev, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return usage, err
		}
		if ev.Data == "[DONE]" {
			break
		}
		var chunk struct {
			ID      string `json:"id"`
			Choices []struct {
				Delta struct {
					Content   string         `json:"content"`
					Reasoning string         `json:"reasoning_content"`
					ToolCalls []ChatToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			_ = start(nil)
			_ = emit("error", map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": chunk.Error.Message}})
			return usage, fmt.Errorf("upstream error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if err := start(chunk.Usage); err != nil {
			return usage, err
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Reasoning != "" {
				if !thinkOpen {
					if err := closeOpen(); err != nil {
						return usage, err
					}
					blockIndex++
					thinkOpen = true
					if err := emit("content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex,
						"content_block": map[string]any{"type": "thinking", "thinking": ""}}); err != nil {
						return usage, err
					}
				}
				if err := emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex,
					"delta": map[string]any{"type": "thinking_delta", "thinking": ch.Delta.Reasoning}}); err != nil {
					return usage, err
				}
			}
			if ch.Delta.Content != "" {
				if !textOpen {
					if err := closeOpen(); err != nil {
						return usage, err
					}
					blockIndex++
					textOpen = true
					if err := emit("content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex,
						"content_block": map[string]any{"type": "text", "text": ""}}); err != nil {
						return usage, err
					}
				}
				if err := emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex,
					"delta": map[string]any{"type": "text_delta", "text": ch.Delta.Content}}); err != nil {
					return usage, err
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				bi, ok := toolBlocks[idx]
				if !ok {
					if err := closeOpen(); err != nil {
						return usage, err
					}
					blockIndex++
					bi = blockIndex
					toolBlocks[idx] = bi
					toolArgs[idx] = &strings.Builder{}
					if err := emit("content_block_start", map[string]any{"type": "content_block_start", "index": bi,
						"content_block": map[string]any{"type": "tool_use", "id": toolID(tc.ID), "name": tc.Function.Name, "input": map[string]any{}}}); err != nil {
						return usage, err
					}
				}
				if tc.Function.Arguments != "" {
					toolArgs[idx].WriteString(tc.Function.Arguments)
					if err := emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": bi,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments}}); err != nil {
						return usage, err
					}
				}
			}
			if ch.FinishReason != nil {
				stopReason = mapFinishToStop(*ch.FinishReason)
			}
		}
	}
	if err := start(nil); err != nil {
		return usage, err
	}
	if err := closeOpen(); err != nil {
		return usage, err
	}
	if err := closeTools(); err != nil {
		return usage, err
	}
	outTokens := 0
	u := map[string]any{"output_tokens": 0}
	if usage != nil {
		outTokens = usage.CompletionTokens
		u["output_tokens"] = outTokens
		u["input_tokens"] = usage.PromptTokens
		if usage.PromptTokensDetails != nil {
			u["cache_read_input_tokens"] = usage.PromptTokensDetails.CachedTokens
		}
	}
	if err := emit("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil}, "usage": u}); err != nil {
		return usage, err
	}
	return usage, emit("message_stop", map[string]any{"type": "message_stop"})
}
