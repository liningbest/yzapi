package convert

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ---- Google Gemini generateContent ----
//
// Gemini has no message ids for tool calls: functionCall parts are matched to
// functionResponse parts by function name, in order. When converting to/from OpenAI
// Chat we synthesise ids ("call_<name>_<n>") and keep a name<->id mapping per request.

type GeminiPart struct {
	Text       string `json:"text,omitempty"`
	Thought    bool   `json:"thought,omitempty"`
	InlineData *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData,omitempty"`
	FileData *struct {
		MimeType string `json:"mimeType,omitempty"`
		FileURI  string `json:"fileUri"`
	} `json:"fileData,omitempty"`
	FunctionCall *struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args,omitempty"`
	} `json:"functionCall,omitempty"`
	FunctionResponse *struct {
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response,omitempty"`
	} `json:"functionResponse,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type GeminiRequest struct {
	Contents          []GeminiContent `json:"contents"`
	SystemInstruction *GeminiContent  `json:"systemInstruction,omitempty"`
	Tools             []struct {
		FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
	} `json:"tools,omitempty"`
	ToolConfig *struct {
		FunctionCallingConfig *struct {
			Mode                 string   `json:"mode,omitempty"`
			AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
		} `json:"functionCallingConfig,omitempty"`
	} `json:"toolConfig,omitempty"`
	GenerationConfig *struct {
		Temperature      *float64        `json:"temperature,omitempty"`
		TopP             *float64        `json:"topP,omitempty"`
		TopK             *int            `json:"topK,omitempty"`
		MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
		StopSequences    []string        `json:"stopSequences,omitempty"`
		ResponseMimeType string          `json:"responseMimeType,omitempty"`
		ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
		ThinkingConfig   *struct {
			ThinkingBudget  *int `json:"thinkingBudget,omitempty"`
			IncludeThoughts bool `json:"includeThoughts,omitempty"`
		} `json:"thinkingConfig,omitempty"`
	} `json:"generationConfig,omitempty"`
}

type GeminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}

type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
	Index        int           `json:"index"`
}

type GeminiResponse struct {
	Candidates    []GeminiCandidate `json:"candidates"`
	UsageMetadata *GeminiUsage      `json:"usageMetadata,omitempty"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
	Error         *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

func geminiPartsText(parts []GeminiPart) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Text != "" && !p.Thought {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// GeminiToChatRequest converts a generateContent request into an OpenAI chat request.
func GeminiToChatRequest(in *GeminiRequest, upstreamModel string) (*ChatRequest, error) {
	out := &ChatRequest{Model: upstreamModel}
	if in.SystemInstruction != nil {
		if s := geminiPartsText(in.SystemInstruction.Parts); s != "" {
			c, _ := json.Marshal(s)
			out.Messages = append(out.Messages, ChatMessage{Role: "system", Content: c})
		}
	}
	callIDs := map[string][]string{} // function name -> pending synthetic ids (FIFO)
	seq := 0
	for _, ct := range in.Contents {
		switch ct.Role {
		case "model":
			msg := ChatMessage{Role: "assistant"}
			var text strings.Builder
			for _, p := range ct.Parts {
				switch {
				case p.FunctionCall != nil:
					seq++
					id := fmt.Sprintf("call_%s_%d", p.FunctionCall.Name, seq)
					callIDs[p.FunctionCall.Name] = append(callIDs[p.FunctionCall.Name], id)
					var tc ChatToolCall
					tc.ID, tc.Type = id, "function"
					tc.Function.Name = p.FunctionCall.Name
					tc.Function.Arguments = string(p.FunctionCall.Args)
					if tc.Function.Arguments == "" {
						tc.Function.Arguments = "{}"
					}
					msg.ToolCalls = append(msg.ToolCalls, tc)
				case p.Thought:
					msg.Reasoning += p.Text
				case p.Text != "":
					text.WriteString(p.Text)
				}
			}
			if text.Len() > 0 || len(msg.ToolCalls) == 0 {
				c, _ := json.Marshal(text.String())
				msg.Content = c
			}
			out.Messages = append(out.Messages, msg)
		default: // user (or "function" in older payloads)
			var parts []map[string]any
			var pendingUser []GeminiPart
			flushUser := func() {
				if len(pendingUser) == 0 {
					return
				}
				onlyText := true
				for _, p := range pendingUser {
					if p.InlineData != nil || p.FileData != nil {
						onlyText = false
					}
				}
				if onlyText {
					c, _ := json.Marshal(geminiPartsText(pendingUser))
					out.Messages = append(out.Messages, ChatMessage{Role: "user", Content: c})
				} else {
					parts = parts[:0]
					for _, p := range pendingUser {
						switch {
						case p.InlineData != nil:
							parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + p.InlineData.MimeType + ";base64," + p.InlineData.Data}})
						case p.FileData != nil:
							parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": p.FileData.FileURI}})
						case p.Text != "":
							parts = append(parts, map[string]any{"type": "text", "text": p.Text})
						}
					}
					c, _ := json.Marshal(parts)
					out.Messages = append(out.Messages, ChatMessage{Role: "user", Content: c})
				}
				pendingUser = nil
			}
			for _, p := range ct.Parts {
				if p.FunctionResponse == nil {
					pendingUser = append(pendingUser, p)
					continue
				}
				flushUser()
				name := p.FunctionResponse.Name
				id := ""
				if q := callIDs[name]; len(q) > 0 {
					id, callIDs[name] = q[0], q[1:]
				} else {
					seq++
					id = fmt.Sprintf("call_%s_%d", name, seq)
				}
				resp := p.FunctionResponse.Response
				if len(resp) == 0 {
					resp = json.RawMessage(`{}`)
				}
				// Tool results are strings in chat; keep the JSON text verbatim.
				c, _ := json.Marshal(string(resp))
				out.Messages = append(out.Messages, ChatMessage{Role: "tool", ToolCallID: id, Content: c})
			}
			flushUser()
		}
	}
	for _, t := range in.Tools {
		for _, fd := range t.FunctionDeclarations {
			var ct ChatTool
			ct.Type = "function"
			ct.Function.Name, ct.Function.Description, ct.Function.Parameters = fd.Name, fd.Description, fd.Parameters
			out.Tools = append(out.Tools, ct)
		}
	}
	if in.ToolConfig != nil && in.ToolConfig.FunctionCallingConfig != nil {
		fc := in.ToolConfig.FunctionCallingConfig
		switch strings.ToUpper(fc.Mode) {
		case "NONE":
			out.ToolChoice = json.RawMessage(`"none"`)
		case "ANY":
			if len(fc.AllowedFunctionNames) == 1 {
				b, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": fc.AllowedFunctionNames[0]}})
				out.ToolChoice = b
			} else {
				out.ToolChoice = json.RawMessage(`"required"`)
			}
		case "AUTO":
			out.ToolChoice = json.RawMessage(`"auto"`)
		}
	}
	if gc := in.GenerationConfig; gc != nil {
		out.Temperature, out.TopP, out.MaxTokens = gc.Temperature, gc.TopP, gc.MaxOutputTokens
		if len(gc.StopSequences) > 0 {
			b, _ := json.Marshal(gc.StopSequences)
			out.Stop = b
		}
		if strings.EqualFold(gc.ResponseMimeType, "application/json") {
			if len(gc.ResponseSchema) > 0 {
				b, _ := json.Marshal(map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "response", "schema": gc.ResponseSchema}})
				out.ResponseFormat = b
			} else {
				out.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
			}
		}
		if gc.ThinkingConfig != nil && gc.ThinkingConfig.ThinkingBudget != nil {
			switch b := *gc.ThinkingConfig.ThinkingBudget; {
			case b < 0:
				out.ReasoningEffort = "medium" // dynamic
			case b == 0:
			case b <= 2048:
				out.ReasoningEffort = "low"
			case b <= 8192:
				out.ReasoningEffort = "medium"
			default:
				out.ReasoningEffort = "high"
			}
		}
	}
	return out, nil
}

// chatContentParts splits an OpenAI content value into Gemini parts.
func chatContentParts(raw json.RawMessage) []map[string]any {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []map[string]any{{"text": s}}
	}
	var items []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	_ = json.Unmarshal(raw, &items)
	var out []map[string]any
	for _, it := range items {
		switch it.Type {
		case "text":
			out = append(out, map[string]any{"text": it.Text})
		case "image_url":
			if it.ImageURL == nil {
				continue
			}
			u := it.ImageURL.URL
			if strings.HasPrefix(u, "data:") {
				if i := strings.Index(u, ";base64,"); i > 0 {
					out = append(out, map[string]any{"inlineData": map[string]string{"mimeType": u[5:i], "data": u[i+8:]}})
					continue
				}
			}
			out = append(out, map[string]any{"fileData": map[string]string{"fileUri": u}})
		}
	}
	return out
}

// ChatToGeminiRequest converts an OpenAI chat request into a generateContent body.
func ChatToGeminiRequest(in *ChatRequest) (map[string]any, error) {
	out := map[string]any{}
	var system []map[string]any
	var contents []map[string]any
	idName := map[string]string{} // synthetic or client tool_call id -> function name
	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			system = append(system, chatContentParts(m.Content)...)
		case "user":
			if parts := chatContentParts(m.Content); len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "user", "parts": parts})
			}
		case "assistant":
			parts := chatContentParts(m.Content)
			if m.Reasoning != "" {
				parts = append([]map[string]any{{"text": m.Reasoning, "thought": true}}, parts...)
			}
			for _, tc := range m.ToolCalls {
				idName[tc.ID] = tc.Function.Name
				var args any = map[string]any{}
				if tc.Function.Arguments != "" {
					var v any
					if json.Unmarshal([]byte(tc.Function.Arguments), &v) == nil {
						args = v
					}
				}
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": tc.Function.Name, "args": args}})
			}
			if len(parts) > 0 {
				contents = append(contents, map[string]any{"role": "model", "parts": parts})
			}
		case "tool":
			name := idName[m.ToolCallID]
			if name == "" {
				name = m.ToolCallID
			}
			var s string
			var resp any
			if json.Unmarshal(m.Content, &s) == nil {
				var v any
				if json.Unmarshal([]byte(s), &v) == nil {
					if obj, ok := v.(map[string]any); ok {
						resp = obj
					} else {
						resp = map[string]any{"result": v}
					}
				} else {
					resp = map[string]any{"result": s}
				}
			} else {
				resp = map[string]any{"result": string(m.Content)}
			}
			part := map[string]any{"functionResponse": map[string]any{"name": name, "response": resp}}
			// Gemini wants consecutive function responses in one user turn.
			if n := len(contents); n > 0 && contents[n-1]["role"] == "user" {
				if ps, ok := contents[n-1]["parts"].([]map[string]any); ok && len(ps) > 0 && ps[len(ps)-1]["functionResponse"] != nil {
					contents[n-1]["parts"] = append(ps, part)
					continue
				}
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]any{part}})
		}
	}
	if len(system) > 0 {
		out["systemInstruction"] = map[string]any{"parts": system}
	}
	if contents == nil {
		contents = []map[string]any{}
	}
	out["contents"] = contents
	if len(in.Tools) > 0 {
		var decls []map[string]any
		for _, t := range in.Tools {
			d := map[string]any{"name": t.Function.Name}
			if t.Function.Description != "" {
				d["description"] = t.Function.Description
			}
			if len(t.Function.Parameters) > 0 {
				d["parameters"] = json.RawMessage(t.Function.Parameters)
			}
			decls = append(decls, d)
		}
		out["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	if len(in.ToolChoice) > 0 {
		var s string
		cfg := map[string]any{}
		if json.Unmarshal(in.ToolChoice, &s) == nil {
			switch s {
			case "none":
				cfg["mode"] = "NONE"
			case "required":
				cfg["mode"] = "ANY"
			default:
				cfg["mode"] = "AUTO"
			}
		} else {
			var tc struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(in.ToolChoice, &tc) == nil && tc.Function.Name != "" {
				cfg["mode"], cfg["allowedFunctionNames"] = "ANY", []string{tc.Function.Name}
			}
		}
		if len(cfg) > 0 {
			out["toolConfig"] = map[string]any{"functionCallingConfig": cfg}
		}
	}
	gc := map[string]any{}
	if in.Temperature != nil {
		gc["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		gc["topP"] = *in.TopP
	}
	if in.MaxTokens != nil {
		gc["maxOutputTokens"] = *in.MaxTokens
	}
	if in.MaxCompletionTokens != nil {
		gc["maxOutputTokens"] = *in.MaxCompletionTokens
	}
	if len(in.Stop) > 0 {
		var one string
		var many []string
		if json.Unmarshal(in.Stop, &one) == nil {
			gc["stopSequences"] = []string{one}
		} else if json.Unmarshal(in.Stop, &many) == nil {
			gc["stopSequences"] = many
		}
	}
	if len(in.ResponseFormat) > 0 {
		var rf struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if json.Unmarshal(in.ResponseFormat, &rf) == nil && strings.HasPrefix(rf.Type, "json") {
			gc["responseMimeType"] = "application/json"
			if rf.JSONSchema != nil && len(rf.JSONSchema.Schema) > 0 {
				gc["responseSchema"] = json.RawMessage(rf.JSONSchema.Schema)
			}
		}
	}
	if in.ReasoningEffort != "" {
		budget := map[string]int{"low": 1024, "medium": 8192, "high": 24576}[in.ReasoningEffort]
		if budget > 0 {
			gc["thinkingConfig"] = map[string]any{"thinkingBudget": budget, "includeThoughts": true}
		}
	}
	if len(gc) > 0 {
		out["generationConfig"] = gc
	}
	return out, nil
}

func geminiFinishToChat(fr string, hasTools bool) string {
	switch strings.ToUpper(fr) {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return "content_filter"
	}
	if hasTools {
		return "tool_calls"
	}
	return "stop"
}

func chatFinishToGemini(fr string) string {
	switch fr {
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	}
	return "STOP"
}

func geminiUsageToChat(u *GeminiUsage) *Usage {
	if u == nil {
		return nil
	}
	out := &Usage{PromptTokens: u.PromptTokenCount, CompletionTokens: u.CandidatesTokenCount + u.ThoughtsTokenCount, TotalTokens: u.TotalTokenCount}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	if u.CachedContentTokenCount > 0 {
		out.PromptTokensDetails = &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: u.CachedContentTokenCount}
	}
	return out
}

func chatUsageToGemini(u *Usage) *GeminiUsage {
	if u == nil {
		return nil
	}
	g := &GeminiUsage{PromptTokenCount: u.PromptTokens, CandidatesTokenCount: u.CompletionTokens, TotalTokenCount: u.TotalTokens}
	if u.PromptTokensDetails != nil {
		g.CachedContentTokenCount = u.PromptTokensDetails.CachedTokens
	}
	return g
}

// GeminiToChatResponse converts a generateContent response into an OpenAI chat response.
func GeminiToChatResponse(raw []byte, model string) (*ChatResponse, error) {
	var in GeminiResponse
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if in.Error != nil {
		return nil, fmt.Errorf("upstream error: %s", in.Error.Message)
	}
	msg := &ChatMessage{Role: "assistant"}
	fr := "stop"
	var text strings.Builder
	if len(in.Candidates) > 0 {
		c := in.Candidates[0]
		n := 0
		for _, p := range c.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				n++
				var tc ChatToolCall
				tc.ID, tc.Type = fmt.Sprintf("call_%s_%d", p.FunctionCall.Name, n), "function"
				tc.Function.Name = p.FunctionCall.Name
				tc.Function.Arguments = string(p.FunctionCall.Args)
				if tc.Function.Arguments == "" {
					tc.Function.Arguments = "{}"
				}
				msg.ToolCalls = append(msg.ToolCalls, tc)
			case p.Thought:
				msg.Reasoning += p.Text
			default:
				text.WriteString(p.Text)
			}
		}
		fr = geminiFinishToChat(c.FinishReason, len(msg.ToolCalls) > 0)
	}
	cb, _ := json.Marshal(text.String())
	msg.Content = cb
	foldReasoning(msg)
	return &ChatResponse{ID: "chatcmpl-gemini", Object: "chat.completion", Model: model,
		Choices: []ChatChoice{{Index: 0, Message: msg, FinishReason: &fr}}, Usage: geminiUsageToChat(in.UsageMetadata)}, nil
}

// ChatToGeminiResponse converts an OpenAI chat response into a generateContent response.
func ChatToGeminiResponse(in *ChatResponse, model string) map[string]any {
	var parts []map[string]any
	fr := "STOP"
	if len(in.Choices) > 0 {
		ch := in.Choices[0]
		if ch.Message != nil {
			if ch.Message.Reasoning != "" {
				parts = append(parts, map[string]any{"text": ch.Message.Reasoning, "thought": true})
			}
			var s string
			if json.Unmarshal(ch.Message.Content, &s) == nil && s != "" {
				parts = append(parts, map[string]any{"text": s})
			}
			for _, tc := range ch.Message.ToolCalls {
				var args any = map[string]any{}
				var v any
				if json.Unmarshal([]byte(tc.Function.Arguments), &v) == nil {
					args = v
				}
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": tc.Function.Name, "args": args}})
			}
		}
		if ch.FinishReason != nil {
			fr = chatFinishToGemini(*ch.FinishReason)
		}
	}
	if parts == nil {
		parts = []map[string]any{}
	}
	out := map[string]any{"candidates": []map[string]any{{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": fr, "index": 0}},
		"modelVersion": model}
	if u := chatUsageToGemini(in.Usage); u != nil {
		out["usageMetadata"] = u
	}
	return out
}

// GeminiStreamToChat converts a streamGenerateContent SSE stream into OpenAI chat chunks.
func GeminiStreamToChat(r io.Reader, w io.Writer, flush func(), model string, includeUsage bool) (*Usage, error) {
	rd := NewSSEReader(r)
	var usage *Usage
	started, done := false, false
	var think thinkState
	toolIdx := 0
	emit := func(delta map[string]any, finish *string, u *Usage) error {
		if !started {
			delta["role"] = "assistant"
			started = true
		}
		chunk := map[string]any{"id": "chatcmpl-gemini", "object": "chat.completion.chunk", "model": model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}}
		if u != nil && includeUsage {
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
		var chunk GeminiResponse
		if json.Unmarshal([]byte(ev.Data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			_ = emit(map[string]any{}, nil, nil)
			return usage, &StreamError{Message: chunk.Error.Message}
		}
		if chunk.UsageMetadata != nil {
			usage = geminiUsageToChat(chunk.UsageMetadata)
		}
		if len(chunk.Candidates) == 0 {
			continue
		}
		c := chunk.Candidates[0]
		hasTools := false
		for _, p := range c.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				hasTools = true
				args := string(p.FunctionCall.Args)
				if args == "" {
					args = "{}"
				}
				idx := toolIdx
				toolIdx++
				tc := map[string]any{"index": idx, "id": fmt.Sprintf("call_%s_%d", p.FunctionCall.Name, idx+1), "type": "function",
					"function": map[string]any{"name": p.FunctionCall.Name, "arguments": args}}
				if err := emit(map[string]any{"tool_calls": []map[string]any{tc}}, nil, nil); err != nil {
					return usage, err
				}
			case p.Thought:
				if err := emit(think.reasoning(p.Text), nil, nil); err != nil {
					return usage, err
				}
			case p.Text != "":
				if err := emit(think.text(p.Text), nil, nil); err != nil {
					return usage, err
				}
			}
		}
		if c.FinishReason != "" {
			if cl := think.closing(); cl != nil {
				if err := emit(cl, nil, nil); err != nil {
					return usage, err
				}
			}
			fr := geminiFinishToChat(c.FinishReason, hasTools || toolIdx > 0)
			if err := emit(map[string]any{}, &fr, usage); err != nil {
				return usage, err
			}
			done = true
		}
	}
	if !done {
		return usage, ErrIncomplete
	}
	if usage != nil && includeUsage {
		b, _ := json.Marshal(map[string]any{"id": "chatcmpl-gemini", "object": "chat.completion.chunk", "model": model, "choices": []any{}, "usage": usage})
		_ = WriteSSE(w, "", string(b))
	}
	if err := WriteSSE(w, "", "[DONE]"); err != nil {
		return usage, err
	}
	flush()
	return usage, nil
}

// StreamError carries an upstream error surfaced inside a stream.
type StreamError struct{ Message string }

func (e *StreamError) Error() string { return "upstream error event: " + e.Message }

// ChatStreamToGemini converts OpenAI chat chunks into a streamGenerateContent SSE stream.
// Function calls are buffered until the finish chunk because Gemini emits whole calls, and
// the finish event itself is held until the usage chunk (which OpenAI sends after it) or
// the end of the stream, so usageMetadata rides on the final event as Gemini clients expect.
func ChatStreamToGemini(r io.Reader, w io.Writer, flush func(), model string) (*Usage, error) {
	rd := NewSSEReader(r)
	var usage *Usage
	type pendingCall struct{ name, args string }
	calls := map[int]*pendingCall{}
	order := []int{}
	done := false
	emit := func(parts []map[string]any, finish string, u *Usage) error {
		cand := map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "index": 0}
		if finish != "" {
			cand["finishReason"] = finish
		}
		out := map[string]any{"candidates": []map[string]any{cand}, "modelVersion": model}
		if u != nil {
			out["usageMetadata"] = chatUsageToGemini(u)
		}
		b, _ := json.Marshal(out)
		if err := WriteSSE(w, "", string(b)); err != nil {
			return err
		}
		flush()
		return nil
	}
	var finishParts []map[string]any
	finishReason := ""
	flushFinish := func() error {
		if finishReason == "" {
			return nil
		}
		fr := finishReason
		finishReason = ""
		return emit(finishParts, fr, usage)
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
			if err := flushFinish(); err != nil {
				return usage, err
			}
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
			return usage, &StreamError{Message: chunk.Error.Message}
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
			if err := flushFinish(); err != nil {
				return usage, err
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta.Reasoning != "" {
			if err := emit([]map[string]any{{"text": ch.Delta.Reasoning, "thought": true}}, "", nil); err != nil {
				return usage, err
			}
		}
		if ch.Delta.Content != "" {
			if err := emit([]map[string]any{{"text": ch.Delta.Content}}, "", nil); err != nil {
				return usage, err
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			pc, ok := calls[idx]
			if !ok {
				pc = &pendingCall{}
				calls[idx] = pc
				order = append(order, idx)
			}
			if tc.Function.Name != "" {
				pc.name = tc.Function.Name
			}
			pc.args += tc.Function.Arguments
		}
		if ch.FinishReason != nil {
			var parts []map[string]any
			for _, idx := range order {
				pc := calls[idx]
				var args any = map[string]any{}
				var v any
				if json.Unmarshal([]byte(pc.args), &v) == nil {
					args = v
				}
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": pc.name, "args": args}})
			}
			if parts == nil {
				parts = []map[string]any{}
			}
			finishParts, finishReason = parts, chatFinishToGemini(*ch.FinishReason)
			done = true
		}
	}
	if !done {
		return usage, ErrIncomplete
	}
	if err := flushFinish(); err != nil {
		return usage, err
	}
	return usage, nil
}
