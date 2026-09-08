package convert

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// OpenAI Chat request  ->  Anthropic Messages request
// ---------------------------------------------------------------------------

func ChatToAnthropicRequest(in *ChatRequest, upstreamModel string) (*AnthropicRequest, error) {
	out := &AnthropicRequest{Model: upstreamModel, Stream: in.Stream, Temperature: in.Temperature, TopP: in.TopP}
	switch {
	case in.MaxCompletionTokens != nil && *in.MaxCompletionTokens > 0:
		out.MaxTokens = *in.MaxCompletionTokens
	case in.MaxTokens != nil && *in.MaxTokens > 0:
		out.MaxTokens = *in.MaxTokens
	default:
		out.MaxTokens = 8192
	}
	if len(in.Stop) > 0 {
		var s string
		var arr []string
		if json.Unmarshal(in.Stop, &s) == nil {
			arr = []string{s}
		} else {
			_ = json.Unmarshal(in.Stop, &arr)
		}
		out.StopSequences = arr
	}
	if in.ReasoningEffort != "" {
		budget := map[string]int{"low": 1024, "medium": 4096, "high": 16000}[in.ReasoningEffort]
		if budget > 0 {
			if out.MaxTokens <= budget {
				out.MaxTokens = budget + 4096
			}
			b, _ := json.Marshal(map[string]any{"type": "enabled", "budget_tokens": budget})
			out.Thinking = b
		}
	}

	var systemParts []string
	var msgs []AnthropicMessage
	appendMsg := func(role string, blocks []AnthropicContentBlock) {
		if len(blocks) == 0 {
			return
		}
		// Anthropic requires alternating roles; merge consecutive same-role messages.
		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			var prev []AnthropicContentBlock
			_ = json.Unmarshal(msgs[n-1].Content, &prev)
			prev = append(prev, blocks...)
			b, _ := json.Marshal(prev)
			msgs[n-1].Content = b
			return
		}
		b, _ := json.Marshal(blocks)
		msgs = append(msgs, AnthropicMessage{Role: role, Content: b})
	}

	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			systemParts = append(systemParts, chatContentText(m.Content))
		case "user":
			blocks, err := chatUserContentToBlocks(m.Content)
			if err != nil {
				return nil, err
			}
			appendMsg("user", blocks)
		case "assistant":
			var blocks []AnthropicContentBlock
			if txt := chatContentText(m.Content); txt != "" {
				blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: txt})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Function.Arguments)
				if !json.Valid(input) || len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, AnthropicContentBlock{Type: "tool_use", ID: toolID(tc.ID), Name: tc.Function.Name, Input: input})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: " "})
			}
			appendMsg("assistant", blocks)
		case "tool", "function":
			content := chatContentText(m.Content)
			cb, _ := json.Marshal(content)
			appendMsg("user", []AnthropicContentBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: cb}})
		}
	}
	if len(systemParts) > 0 {
		b, _ := json.Marshal(strings.Join(systemParts, "\n\n"))
		out.System = b
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no user messages to send")
	}
	if msgs[0].Role != "user" {
		b, _ := json.Marshal([]AnthropicContentBlock{{Type: "text", Text: "(continue)"}})
		msgs = append([]AnthropicMessage{{Role: "user", Content: b}}, msgs...)
	}
	out.Messages = msgs

	for _, t := range in.Tools {
		if t.Type != "function" {
			continue
		}
		params := t.Function.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out.Tools = append(out.Tools, AnthropicTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: params})
	}
	if len(in.ToolChoice) > 0 && len(out.Tools) > 0 {
		var s string
		if json.Unmarshal(in.ToolChoice, &s) == nil {
			switch s {
			case "auto":
				out.ToolChoice = json.RawMessage(`{"type":"auto"}`)
			case "required":
				out.ToolChoice = json.RawMessage(`{"type":"any"}`)
			case "none":
				out.Tools = nil
			}
		} else {
			var obj struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(in.ToolChoice, &obj) == nil && obj.Function.Name != "" {
				b, _ := json.Marshal(map[string]string{"type": "tool", "name": obj.Function.Name})
				out.ToolChoice = b
			}
		}
		if in.ParallelToolCalls != nil && !*in.ParallelToolCalls && len(out.ToolChoice) > 0 {
			var tc map[string]any
			_ = json.Unmarshal(out.ToolChoice, &tc)
			tc["disable_parallel_tool_use"] = true
			out.ToolChoice, _ = json.Marshal(tc)
		}
	}
	return out, nil
}

func chatUserContentToBlocks(raw json.RawMessage) ([]AnthropicContentBlock, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			s = " "
		}
		return []AnthropicContentBlock{{Type: "text", Text: s}}, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("invalid user content: %w", err)
	}
	var blocks []AnthropicContentBlock
	for _, p := range parts {
		switch p.Type {
		case "text":
			blocks = append(blocks, AnthropicContentBlock{Type: "text", Text: p.Text})
		case "image_url":
			if p.ImageURL == nil {
				continue
			}
			b := AnthropicContentBlock{Type: "image"}
			b.Source = &struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type,omitempty"`
				Data      string `json:"data,omitempty"`
				URL       string `json:"url,omitempty"`
			}{}
			if strings.HasPrefix(p.ImageURL.URL, "data:") {
				meta, data, _ := strings.Cut(strings.TrimPrefix(p.ImageURL.URL, "data:"), ",")
				b.Source.Type = "base64"
				b.Source.MediaType = strings.TrimSuffix(meta, ";base64")
				b.Source.Data = data
			} else {
				b.Source.Type = "url"
				b.Source.URL = p.ImageURL.URL
			}
			blocks = append(blocks, b)
		}
	}
	return blocks, nil
}

// ---------------------------------------------------------------------------
// Anthropic response  ->  OpenAI Chat response
// ---------------------------------------------------------------------------

func mapStopToFinish(sr string) string {
	switch sr {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func AnthropicToChatResponse(in *AnthropicResponse, model string) *ChatResponse {
	msg := &ChatMessage{Role: "assistant"}
	var text strings.Builder
	for _, b := range in.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "thinking":
			msg.Reasoning += b.Thinking
		case "tool_use":
			var tc ChatToolCall
			tc.ID = b.ID
			tc.Type = "function"
			tc.Function.Name = b.Name
			tc.Function.Arguments = string(b.Input)
			if tc.Function.Arguments == "" {
				tc.Function.Arguments = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	c, _ := json.Marshal(text.String())
	msg.Content = c
	fr := "stop"
	if in.StopReason != nil {
		fr = mapStopToFinish(*in.StopReason)
	}
	u := &Usage{
		PromptTokens:     in.Usage.InputTokens + in.Usage.CacheReadInputTokens + in.Usage.CacheCreationInputTokens,
		CompletionTokens: in.Usage.OutputTokens,
	}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	if in.Usage.CacheReadInputTokens > 0 {
		u.PromptTokensDetails = &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: in.Usage.CacheReadInputTokens}
	}
	return &ChatResponse{
		ID: chatID(in.ID), Object: "chat.completion", Created: time.Now().Unix(), Model: model,
		Choices: []ChatChoice{{Index: 0, Message: msg, FinishReason: &fr}},
		Usage:   u,
	}
}

func chatID(id string) string {
	if id == "" {
		return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	return "chatcmpl-" + strings.TrimPrefix(id, "msg_")
}

// ---------------------------------------------------------------------------
// Anthropic SSE  ->  OpenAI Chat SSE
// ---------------------------------------------------------------------------

func AnthropicStreamToChat(r io.Reader, w io.Writer, flush func(), model string, includeUsage bool) (*Usage, error) {
	rd := NewSSEReader(r)
	id := chatID("")
	created := time.Now().Unix()
	usage := &Usage{}
	blockTool := map[int]int{} // content block index -> tool call index
	nextTool := 0
	finish := "stop"
	sentRole := false

	emitChunk := func(delta map[string]any, finishReason *string, u *Usage) error {
		if !sentRole {
			delta["role"] = "assistant"
			sentRole = true
		}
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finishReason}},
		}
		if u != nil {
			chunk["usage"] = u
		}
		b, _ := json.Marshal(chunk)
		if err := WriteSSE(w, "", string(b)); err != nil {
			return err
		}
		flush()
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
		var e struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				ID    string         `json:"id"`
				Usage AnthropicUsage `json:"usage"`
			} `json:"message"`
			ContentBlock *AnthropicContentBlock `json:"content_block"`
			Delta        *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *AnthropicUsage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(ev.Data), &e) != nil {
			continue
		}
		switch e.Type {
		case "message_start":
			if e.Message != nil {
				id = chatID(e.Message.ID)
				usage.PromptTokens = e.Message.Usage.InputTokens + e.Message.Usage.CacheReadInputTokens + e.Message.Usage.CacheCreationInputTokens
				if e.Message.Usage.CacheReadInputTokens > 0 {
					usage.PromptTokensDetails = &struct {
						CachedTokens int `json:"cached_tokens"`
					}{CachedTokens: e.Message.Usage.CacheReadInputTokens}
				}
			}
			if err := emitChunk(map[string]any{"content": ""}, nil, nil); err != nil {
				return usage, err
			}
		case "content_block_start":
			if e.ContentBlock != nil && e.ContentBlock.Type == "tool_use" {
				ti := nextTool
				nextTool++
				blockTool[e.Index] = ti
				tc := map[string]any{"index": ti, "id": e.ContentBlock.ID, "type": "function",
					"function": map[string]any{"name": e.ContentBlock.Name, "arguments": ""}}
				if err := emitChunk(map[string]any{"tool_calls": []any{tc}}, nil, nil); err != nil {
					return usage, err
				}
			}
		case "content_block_delta":
			if e.Delta == nil {
				continue
			}
			switch e.Delta.Type {
			case "text_delta":
				if err := emitChunk(map[string]any{"content": e.Delta.Text}, nil, nil); err != nil {
					return usage, err
				}
			case "thinking_delta":
				if err := emitChunk(map[string]any{"reasoning_content": e.Delta.Thinking}, nil, nil); err != nil {
					return usage, err
				}
			case "input_json_delta":
				ti := blockTool[e.Index]
				tc := map[string]any{"index": ti, "function": map[string]any{"arguments": e.Delta.PartialJSON}}
				if err := emitChunk(map[string]any{"tool_calls": []any{tc}}, nil, nil); err != nil {
					return usage, err
				}
			}
		case "message_delta":
			if e.Delta != nil && e.Delta.StopReason != "" {
				finish = mapStopToFinish(e.Delta.StopReason)
			}
			if e.Usage != nil {
				usage.CompletionTokens = e.Usage.OutputTokens
				if e.Usage.InputTokens > 0 {
					usage.PromptTokens = e.Usage.InputTokens + e.Usage.CacheReadInputTokens + e.Usage.CacheCreationInputTokens
				}
			}
		case "error":
			msg := "upstream error"
			if e.Error != nil {
				msg = e.Error.Message
			}
			b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": "api_error"}})
			_ = WriteSSE(w, "", string(b))
			flush()
			return usage, fmt.Errorf("upstream error: %s", msg)
		case "message_stop":
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			f := finish
			if err := emitChunk(map[string]any{}, &f, nil); err != nil {
				return usage, err
			}
			if includeUsage {
				chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
					"choices": []any{}, "usage": usage}
				b, _ := json.Marshal(chunk)
				if err := WriteSSE(w, "", string(b)); err != nil {
					return usage, err
				}
			}
			if err := WriteSSE(w, "", "[DONE]"); err != nil {
				return usage, err
			}
			flush()
			return usage, nil
		}
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	_ = WriteSSE(w, "", "[DONE]")
	flush()
	return usage, ErrIncomplete
}
