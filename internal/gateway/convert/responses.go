package convert

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// OpenAI Responses request  ->  Chat Completions request
// ---------------------------------------------------------------------------

func ResponsesToChatRequest(in *ResponsesRequest, upstreamModel string) (*ChatRequest, error) {
	out := &ChatRequest{Model: upstreamModel, Stream: in.Stream, Temperature: in.Temperature, TopP: in.TopP, User: in.User}
	if in.MaxOutputTokens != nil {
		out.MaxTokens = in.MaxOutputTokens
	}
	if in.Reasoning != nil && in.Reasoning.Effort != "" {
		out.ReasoningEffort = in.Reasoning.Effort
	}
	if in.Instructions != "" {
		c, _ := json.Marshal(in.Instructions)
		out.Messages = append(out.Messages, ChatMessage{Role: "system", Content: c})
	}
	// input: string or array of items
	var s string
	if json.Unmarshal(in.Input, &s) == nil {
		c, _ := json.Marshal(s)
		out.Messages = append(out.Messages, ChatMessage{Role: "user", Content: c})
	} else {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(in.Input, &items); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
		for _, it := range items {
			var typ, role string
			_ = json.Unmarshal(it["type"], &typ)
			_ = json.Unmarshal(it["role"], &role)
			switch {
			case typ == "function_call":
				var name, args, callID string
				_ = json.Unmarshal(it["name"], &name)
				_ = json.Unmarshal(it["arguments"], &args)
				_ = json.Unmarshal(it["call_id"], &callID)
				var tc ChatToolCall
				tc.ID, tc.Type = callID, "function"
				tc.Function.Name, tc.Function.Arguments = name, args
				// merge into previous assistant message if any
				if n := len(out.Messages); n > 0 && out.Messages[n-1].Role == "assistant" {
					out.Messages[n-1].ToolCalls = append(out.Messages[n-1].ToolCalls, tc)
				} else {
					out.Messages = append(out.Messages, ChatMessage{Role: "assistant", ToolCalls: []ChatToolCall{tc}})
				}
			case typ == "function_call_output":
				var callID string
				_ = json.Unmarshal(it["call_id"], &callID)
				outRaw := it["output"]
				var os string
				if json.Unmarshal(outRaw, &os) != nil {
					os = string(outRaw)
				}
				c, _ := json.Marshal(os)
				out.Messages = append(out.Messages, ChatMessage{Role: "tool", ToolCallID: callID, Content: c})
			case typ == "message" || typ == "" || role != "":
				if role == "" {
					role = "user"
				}
				if role == "developer" {
					role = "system"
				}
				content := responsesContentToChat(it["content"])
				out.Messages = append(out.Messages, ChatMessage{Role: role, Content: content})
			case typ == "reasoning":
				// drop
			}
		}
	}
	for _, raw := range in.Tools {
		var t struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Strict      *bool           `json:"strict"`
		}
		if json.Unmarshal(raw, &t) != nil || t.Type != "function" {
			continue
		}
		var ct ChatTool
		ct.Type = "function"
		ct.Function.Name = t.Name
		ct.Function.Description = t.Description
		ct.Function.Parameters = t.Parameters
		if len(ct.Function.Parameters) == 0 {
			ct.Function.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out.Tools = append(out.Tools, ct)
	}
	if len(in.ToolChoice) > 0 {
		var s string
		if json.Unmarshal(in.ToolChoice, &s) == nil {
			out.ToolChoice = in.ToolChoice
		} else {
			var obj struct {
				Type string `json:"type"`
				Name string `json:"name"`
			}
			if json.Unmarshal(in.ToolChoice, &obj) == nil && obj.Type == "function" {
				b, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": obj.Name}})
				out.ToolChoice = b
			}
		}
	}
	out.ParallelToolCalls = in.ParallelToolCalls
	if len(in.Text) > 0 {
		var t struct {
			Format json.RawMessage `json:"format"`
		}
		if json.Unmarshal(in.Text, &t) == nil && len(t.Format) > 0 {
			var f struct {
				Type   string          `json:"type"`
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
				Strict *bool           `json:"strict"`
			}
			if json.Unmarshal(t.Format, &f) == nil {
				switch f.Type {
				case "json_object":
					out.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
				case "json_schema":
					b, _ := json.Marshal(map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": f.Name, "schema": f.Schema, "strict": f.Strict}})
					out.ResponseFormat = b
				}
			}
		}
	}
	if in.Stream {
		out.StreamOptions = json.RawMessage(`{"include_usage":true}`)
	}
	return out, nil
}

func responsesContentToChat(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`""`)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		b, _ := json.Marshal(s)
		return b
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return json.RawMessage(`""`)
	}
	var outParts []map[string]any
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			outParts = append(outParts, map[string]any{"type": "text", "text": p.Text})
		case "input_image":
			outParts = append(outParts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": p.ImageURL}})
		}
	}
	if len(outParts) == 1 && outParts[0]["type"] == "text" {
		b, _ := json.Marshal(outParts[0]["text"])
		return b
	}
	b, _ := json.Marshal(outParts)
	return b
}

// ---------------------------------------------------------------------------
// Chat response  ->  Responses response
// ---------------------------------------------------------------------------

type respUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func toRespUsage(u *Usage) *respUsage {
	if u == nil {
		return nil
	}
	r := &respUsage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
	if u.PromptTokensDetails != nil {
		r.InputTokensDetails.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	return r
}

func respID(id string) string {
	if id == "" {
		return fmt.Sprintf("resp_%d", time.Now().UnixNano())
	}
	return "resp_" + strings.TrimPrefix(id, "chatcmpl-")
}

func ChatToResponsesResponse(in *ChatResponse, model string) map[string]any {
	id := respID(in.ID)
	var output []any
	status := "completed"
	if len(in.Choices) > 0 && in.Choices[0].Message != nil {
		m := in.Choices[0].Message
		if m.Reasoning != "" {
			output = append(output, map[string]any{"type": "reasoning", "id": "rs_" + id[5:], "summary": []any{map[string]any{"type": "summary_text", "text": m.Reasoning}}})
		}
		if txt := chatContentText(m.Content); txt != "" || len(m.ToolCalls) == 0 {
			output = append(output, map[string]any{
				"type": "message", "id": "msg_" + id[5:], "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": txt, "annotations": []any{}}},
			})
		}
		for i, tc := range m.ToolCalls {
			output = append(output, map[string]any{
				"type": "function_call", "id": fmt.Sprintf("fc_%s_%d", id[5:], i), "call_id": tc.ID,
				"name": tc.Function.Name, "arguments": tc.Function.Arguments, "status": "completed",
			})
		}
		if in.Choices[0].FinishReason != nil && *in.Choices[0].FinishReason == "length" {
			status = "incomplete"
		}
	}
	if output == nil {
		output = []any{}
	}
	out := map[string]any{
		"id": id, "object": "response", "created_at": time.Now().Unix(), "status": status, "model": model,
		"output": output, "error": nil, "incomplete_details": nil, "parallel_tool_calls": true,
	}
	if status == "incomplete" {
		out["incomplete_details"] = map[string]string{"reason": "max_output_tokens"}
	}
	if in.Usage != nil {
		out["usage"] = toRespUsage(in.Usage)
	}
	return out
}

// ---------------------------------------------------------------------------
// Chat SSE  ->  Responses SSE
// ---------------------------------------------------------------------------

func ChatStreamToResponses(r io.Reader, w io.Writer, flush func(), model string) (*Usage, error) {
	rd := NewSSEReader(r)
	id := respID("")
	seq := 0
	emit := func(event string, v map[string]any) error {
		v["type"] = event
		v["sequence_number"] = seq
		seq++
		b, _ := json.Marshal(v)
		if err := WriteSSE(w, event, string(b)); err != nil {
			return err
		}
		flush()
		return nil
	}
	base := map[string]any{"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "in_progress", "model": model, "output": []any{}}
	if err := emit("response.created", map[string]any{"response": base}); err != nil {
		return nil, err
	}
	if err := emit("response.in_progress", map[string]any{"response": base}); err != nil {
		return nil, err
	}

	var (
		usage      *Usage
		outIndex   = -1
		msgOpen    bool
		msgID      string
		text       strings.Builder
		reasoning  strings.Builder
		reasonOpen bool
		reasonIdx  int
		tools      = map[int]*toolState{}
		outputs    []any
		finish     string
		done       bool
	)
	closeMsg := func() error {
		if !msgOpen {
			return nil
		}
		msgOpen = false
		full := text.String()
		if err := emit("response.output_text.done", map[string]any{"item_id": msgID, "output_index": outIndex, "content_index": 0, "text": full}); err != nil {
			return err
		}
		part := map[string]any{"type": "output_text", "text": full, "annotations": []any{}}
		if err := emit("response.content_part.done", map[string]any{"item_id": msgID, "output_index": outIndex, "content_index": 0, "part": part}); err != nil {
			return err
		}
		item := map[string]any{"id": msgID, "type": "message", "status": "completed", "role": "assistant", "content": []any{part}}
		outputs = append(outputs, item)
		return emit("response.output_item.done", map[string]any{"output_index": outIndex, "item": item})
	}
	closeReason := func() error {
		if !reasonOpen {
			return nil
		}
		reasonOpen = false
		item := map[string]any{"id": "rs_" + id[5:], "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": reasoning.String()}}}
		outputs = append(outputs, item)
		return emit("response.output_item.done", map[string]any{"output_index": reasonIdx, "item": item})
	}
	closeTools := func() error {
		for _, ts := range tools {
			if err := emit("response.function_call_arguments.done", map[string]any{"item_id": ts.itemID, "output_index": ts.index, "arguments": ts.args.String()}); err != nil {
				return err
			}
			item := map[string]any{"id": ts.itemID, "type": "function_call", "status": "completed", "call_id": ts.callID, "name": ts.name, "arguments": ts.args.String()}
			outputs = append(outputs, item)
			if err := emit("response.output_item.done", map[string]any{"output_index": ts.index, "item": item}); err != nil {
				return err
			}
		}
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
			done = true
			break
		}
		var chunk struct {
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
		if json.Unmarshal([]byte(ev.Data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			_ = emit("error", map[string]any{"code": "upstream_error", "message": chunk.Error.Message})
			return usage, fmt.Errorf("upstream error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Reasoning != "" {
				if !reasonOpen {
					outIndex++
					reasonIdx = outIndex
					reasonOpen = true
					item := map[string]any{"id": "rs_" + id[5:], "type": "reasoning", "summary": []any{}}
					if err := emit("response.output_item.added", map[string]any{"output_index": outIndex, "item": item}); err != nil {
						return usage, err
					}
				}
				reasoning.WriteString(ch.Delta.Reasoning)
				if err := emit("response.reasoning_summary_text.delta", map[string]any{"item_id": "rs_" + id[5:], "output_index": reasonIdx, "summary_index": 0, "delta": ch.Delta.Reasoning}); err != nil {
					return usage, err
				}
			}
			if ch.Delta.Content != "" {
				if err := closeReason(); err != nil {
					return usage, err
				}
				if !msgOpen {
					outIndex++
					msgOpen = true
					msgID = fmt.Sprintf("msg_%s_%d", id[5:], outIndex)
					item := map[string]any{"id": msgID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
					if err := emit("response.output_item.added", map[string]any{"output_index": outIndex, "item": item}); err != nil {
						return usage, err
					}
					if err := emit("response.content_part.added", map[string]any{"item_id": msgID, "output_index": outIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}); err != nil {
						return usage, err
					}
				}
				text.WriteString(ch.Delta.Content)
				if err := emit("response.output_text.delta", map[string]any{"item_id": msgID, "output_index": outIndex, "content_index": 0, "delta": ch.Delta.Content}); err != nil {
					return usage, err
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				ts, ok := tools[idx]
				if !ok {
					if err := closeReason(); err != nil {
						return usage, err
					}
					if err := closeMsg(); err != nil {
						return usage, err
					}
					outIndex++
					ts = &toolState{index: outIndex, itemID: fmt.Sprintf("fc_%s_%d", id[5:], outIndex), callID: tc.ID, name: tc.Function.Name}
					if ts.callID == "" {
						ts.callID = fmt.Sprintf("call_%d", time.Now().UnixNano())
					}
					tools[idx] = ts
					item := map[string]any{"id": ts.itemID, "type": "function_call", "status": "in_progress", "call_id": ts.callID, "name": ts.name, "arguments": ""}
					if err := emit("response.output_item.added", map[string]any{"output_index": outIndex, "item": item}); err != nil {
						return usage, err
					}
				}
				if tc.Function.Arguments != "" {
					ts.args.WriteString(tc.Function.Arguments)
					if err := emit("response.function_call_arguments.delta", map[string]any{"item_id": ts.itemID, "output_index": ts.index, "delta": tc.Function.Arguments}); err != nil {
						return usage, err
					}
				}
			}
			if ch.FinishReason != nil {
				finish = *ch.FinishReason
			}
		}
	}
	if err := closeReason(); err != nil {
		return usage, err
	}
	if err := closeMsg(); err != nil {
		return usage, err
	}
	if err := closeTools(); err != nil {
		return usage, err
	}
	if outputs == nil {
		outputs = []any{}
	}
	status := "completed"
	final := map[string]any{"id": id, "object": "response", "created_at": base["created_at"], "status": status, "model": model, "output": outputs, "error": nil, "incomplete_details": nil}
	if finish == "length" {
		final["status"] = "incomplete"
		final["incomplete_details"] = map[string]string{"reason": "max_output_tokens"}
	}
	if usage != nil {
		final["usage"] = toRespUsage(usage)
	}
	if !done {
		// The upstream closed the stream without finishing: tell the client the response
		// failed instead of fabricating a successful completion.
		final["status"] = "failed"
		final["error"] = map[string]any{"code": "upstream_incomplete", "message": ErrIncomplete.Error()}
		_ = emit("response.failed", map[string]any{"response": final})
		_ = emit("error", map[string]any{"code": "upstream_incomplete", "message": ErrIncomplete.Error()})
		return usage, ErrIncomplete
	}
	return usage, emit("response.completed", map[string]any{"response": final})
}

type toolState struct {
	index  int
	itemID string
	callID string
	name   string
	args   strings.Builder
}

// ---------------------------------------------------------------------------
// Chat request -> Responses request (upstream only speaks Responses)
// ---------------------------------------------------------------------------

func ChatToResponsesRequest(in *ChatRequest, upstreamModel string) (map[string]any, error) {
	out := map[string]any{"model": upstreamModel, "stream": in.Stream}
	if in.MaxTokens != nil {
		out["max_output_tokens"] = *in.MaxTokens
	}
	if in.MaxCompletionTokens != nil {
		out["max_output_tokens"] = *in.MaxCompletionTokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if in.ReasoningEffort != "" {
		out["reasoning"] = map[string]string{"effort": in.ReasoningEffort}
	}
	var instructions []string
	var input []any
	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			instructions = append(instructions, chatContentText(m.Content))
		case "user":
			var s string
			if json.Unmarshal(m.Content, &s) == nil {
				input = append(input, map[string]any{"role": "user", "content": s})
			} else {
				var parts []map[string]any
				_ = json.Unmarshal(m.Content, &parts)
				var conv []map[string]any
				for _, p := range parts {
					switch p["type"] {
					case "text":
						conv = append(conv, map[string]any{"type": "input_text", "text": p["text"]})
					case "image_url":
						if iu, ok := p["image_url"].(map[string]any); ok {
							conv = append(conv, map[string]any{"type": "input_image", "image_url": iu["url"]})
						}
					}
				}
				input = append(input, map[string]any{"role": "user", "content": conv})
			}
		case "assistant":
			if txt := chatContentText(m.Content); txt != "" {
				input = append(input, map[string]any{"role": "assistant", "content": []map[string]any{{"type": "output_text", "text": txt}}})
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Function.Name, "arguments": tc.Function.Arguments})
			}
		case "tool":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": chatContentText(m.Content)})
		}
	}
	if len(instructions) > 0 {
		out["instructions"] = strings.Join(instructions, "\n\n")
	}
	out["input"] = input
	if len(in.Tools) > 0 {
		var tools []map[string]any
		for _, t := range in.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": t.Function.Name, "description": t.Function.Description, "parameters": t.Function.Parameters})
		}
		out["tools"] = tools
	}
	if len(in.ToolChoice) > 0 {
		var s string
		if json.Unmarshal(in.ToolChoice, &s) == nil {
			out["tool_choice"] = s
		} else {
			var obj struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(in.ToolChoice, &obj) == nil {
				out["tool_choice"] = map[string]string{"type": "function", "name": obj.Function.Name}
			}
		}
	}
	return out, nil
}

// ResponsesToChatResponse converts a non-streaming Responses object to a chat completion.
func ResponsesToChatResponse(raw []byte, model string) (*ChatResponse, error) {
	var in struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Summary   []struct {
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
		Usage             *respUsage `json:"usage"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	msg := &ChatMessage{Role: "assistant"}
	var text strings.Builder
	for _, o := range in.Output {
		switch o.Type {
		case "message":
			for _, c := range o.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			var tc ChatToolCall
			tc.ID, tc.Type = o.CallID, "function"
			tc.Function.Name, tc.Function.Arguments = o.Name, o.Arguments
			msg.ToolCalls = append(msg.ToolCalls, tc)
		case "reasoning":
			for _, s := range o.Summary {
				msg.Reasoning += s.Text
			}
		}
	}
	c, _ := json.Marshal(text.String())
	msg.Content = c
	foldReasoning(msg)
	fr := "stop"
	if len(msg.ToolCalls) > 0 {
		fr = "tool_calls"
	}
	if in.IncompleteDetails != nil && in.IncompleteDetails.Reason == "max_output_tokens" {
		fr = "length"
	}
	out := &ChatResponse{ID: chatID(strings.TrimPrefix(in.ID, "resp_")), Object: "chat.completion", Created: time.Now().Unix(), Model: model,
		Choices: []ChatChoice{{Index: 0, Message: msg, FinishReason: &fr}}}
	if in.Usage != nil {
		out.Usage = &Usage{PromptTokens: in.Usage.InputTokens, CompletionTokens: in.Usage.OutputTokens, TotalTokens: in.Usage.TotalTokens}
		if in.Usage.InputTokensDetails.CachedTokens > 0 {
			out.Usage.PromptTokensDetails = &struct {
				CachedTokens int `json:"cached_tokens"`
			}{CachedTokens: in.Usage.InputTokensDetails.CachedTokens}
		}
	}
	return out, nil
}

// ResponsesStreamToChat converts Responses SSE into chat completion chunks.
func ResponsesStreamToChat(r io.Reader, w io.Writer, flush func(), model string, includeUsage bool) (*Usage, error) {
	rd := NewSSEReader(r)
	id := chatID("")
	created := time.Now().Unix()
	sentRole := false
	var usage *Usage
	toolIdx := map[string]int{}
	emitChunk := func(delta map[string]any, finish *string) error {
		if !sentRole {
			delta["role"] = "assistant"
			sentRole = true
		}
		chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}}
		b, _ := json.Marshal(chunk)
		if err := WriteSSE(w, "", string(b)); err != nil {
			return err
		}
		flush()
		return nil
	}
	var think thinkState
	hasTool := false
	for {
		ev, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return usage, err
		}
		var e struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Item  *struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			} `json:"item"`
			ItemID   string `json:"item_id"`
			Response *struct {
				Status            string     `json:"status"`
				Usage             *respUsage `json:"usage"`
				IncompleteDetails *struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(ev.Data), &e) != nil {
			continue
		}
		switch e.Type {
		case "response.output_text.delta":
			if err := emitChunk(think.text(e.Delta), nil); err != nil {
				return usage, err
			}
		case "response.reasoning_summary_text.delta":
			if err := emitChunk(think.reasoning(e.Delta), nil); err != nil {
				return usage, err
			}
		case "response.output_item.added":
			if e.Item != nil && e.Item.Type == "function_call" {
				hasTool = true
				ti := len(toolIdx)
				toolIdx[e.Item.ID] = ti
				tc := map[string]any{"index": ti, "id": e.Item.CallID, "type": "function", "function": map[string]any{"name": e.Item.Name, "arguments": ""}}
				if err := emitChunk(map[string]any{"tool_calls": []any{tc}}, nil); err != nil {
					return usage, err
				}
			}
		case "response.function_call_arguments.delta":
			ti := toolIdx[e.ItemID]
			tc := map[string]any{"index": ti, "function": map[string]any{"arguments": e.Delta}}
			if err := emitChunk(map[string]any{"tool_calls": []any{tc}}, nil); err != nil {
				return usage, err
			}
		case "response.completed", "response.incomplete", "response.failed":
			if cl := think.closing(); cl != nil {
				if err := emitChunk(cl, nil); err != nil {
					return usage, err
				}
			}
			finish := "stop"
			if hasTool {
				finish = "tool_calls"
			}
			if e.Response != nil {
				if e.Response.IncompleteDetails != nil && e.Response.IncompleteDetails.Reason == "max_output_tokens" {
					finish = "length"
				}
				if e.Response.Usage != nil {
					usage = &Usage{PromptTokens: e.Response.Usage.InputTokens, CompletionTokens: e.Response.Usage.OutputTokens, TotalTokens: e.Response.Usage.TotalTokens}
				}
			}
			if err := emitChunk(map[string]any{}, &finish); err != nil {
				return usage, err
			}
			if includeUsage && usage != nil {
				b, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{}, "usage": usage})
				_ = WriteSSE(w, "", string(b))
			}
			_ = WriteSSE(w, "", "[DONE]")
			flush()
			return usage, nil
		case "error":
			b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": e.Message, "type": "api_error"}})
			_ = WriteSSE(w, "", string(b))
			flush()
			return usage, fmt.Errorf("upstream error: %s", e.Message)
		}
	}
	// Upstream ended without response.completed: surface an error chunk to the client.
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": ErrIncomplete.Error(), "type": "upstream_incomplete"}})
	_ = WriteSSE(w, "", string(b))
	flush()
	return usage, ErrIncomplete
}
