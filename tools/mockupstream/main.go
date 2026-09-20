// Command mockupstream is a tiny OpenAI/Anthropic-compatible server for local testing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var (
	addr  = flag.String("addr", "127.0.0.1:9911", "listen address")
	delay = flag.Duration("delay", 20*time.Millisecond, "delay between stream chunks")
	fail  = flag.Int("fail", 0, "HTTP status to return for every request (0 = healthy)")
)

func main() {
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"object": "list", "data": []map[string]any{
			{"id": "mock-mini", "object": "model"}, {"id": "mock-pro", "object": "model"}, {"id": "mock-embed", "object": "model"},
		}})
	})
	mux.HandleFunc("/v1/chat/completions", chat)
	mux.HandleFunc("/v1/messages", messages)
	mux.HandleFunc("/v1/embeddings", embeddings)
	mux.HandleFunc("/v1/responses", responses)
	mux.HandleFunc("/v1beta/models/", gemini) // /v1beta/models/{model}:generateContent | :streamGenerateContent
	// Non-chat JSON API in the shape of TypeSafe's POST /v1/systemone: typed answers plus
	// Responses-style usage names. Echoes the model so tests can check the rewrite.
	mux.HandleFunc("/v1/systemone", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &in)
		answers := map[string]any{}
		for id := range in.Questions {
			answers[id] = map[string]any{"type": "noul", "noul": 0.91, "confidence": 0.8}
		}
		writeJSON(w, map[string]any{"model": in.Model, "answers": answers, "usage": map[string]any{"input_tokens": 21, "output_tokens": 4}})
	})
	mux.HandleFunc("/v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"created": time.Now().Unix(), "data": []map[string]any{{"url": "https://example.com/mock.png"}}})
	})
	log.Printf("mock upstream listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, logReq(mux)))
}

func logReq(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *fail > 0 {
			w.WriteHeader(*fail)
			fmt.Fprintf(w, `{"error":{"message":"mock failure %d","type":"server_error"}}`, *fail)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && r.Header.Get("x-api-key") == "" && r.Header.Get("x-goog-api-key") == "" && r.URL.Query().Get("key") == "" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"missing api key","type":"authentication_error"}}`)
			return
		}
		log.Printf("%s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func lastUser(msgs []map[string]any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] == "user" {
			switch c := msgs[i]["content"].(type) {
			case string:
				return c
			case []any:
				for _, p := range c {
					if m, ok := p.(map[string]any); ok && m["type"] == "text" {
						return fmt.Sprint(m["text"])
					}
				}
			}
		}
	}
	return ""
}

func chat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model         string           `json:"model"`
		Messages      []map[string]any `json:"messages"`
		Stream        bool             `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
		Tools []any `json:"tools"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"bad json","type":"invalid_request_error"}}`)
		return
	}
	if req.Model == "" {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"model required","type":"invalid_request_error"}}`)
		return
	}
	prompt := lastUser(req.Messages)
	reply := fmt.Sprintf("[%s] echo: %s", req.Model, prompt)
	wantTool := len(req.Tools) > 0 && strings.Contains(strings.ToLower(prompt), "weather")
	usage := map[string]any{"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19, "prompt_tokens_details": map[string]int{"cached_tokens": 4}}
	id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	if !req.Stream {
		msg := map[string]any{"role": "assistant", "content": reply}
		finish := "stop"
		if wantTool {
			msg["content"] = nil
			msg["tool_calls"] = []map[string]any{{"id": "call_1", "type": "function", "function": map[string]string{"name": "get_weather", "arguments": `{"city":"Beijing"}`}}}
			finish = "tool_calls"
		}
		writeJSON(w, map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": req.Model,
			"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}}, "usage": usage})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fl := w.(http.Flusher)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
		time.Sleep(*delay)
	}
	base := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	send(base(map[string]any{"role": "assistant", "content": ""}, nil))
	if wantTool {
		send(base(map[string]any{"tool_calls": []map[string]any{{"index": 0, "id": "call_1", "type": "function", "function": map[string]string{"name": "get_weather", "arguments": ""}}}}, nil))
		send(base(map[string]any{"tool_calls": []map[string]any{{"index": 0, "function": map[string]string{"arguments": `{"city":`}}}}, nil))
		send(base(map[string]any{"tool_calls": []map[string]any{{"index": 0, "function": map[string]string{"arguments": `"Beijing"}`}}}}, nil))
		send(base(map[string]any{}, "tool_calls"))
	} else {
		for _, word := range strings.SplitAfter(reply, " ") {
			send(base(map[string]any{"content": word}, nil))
		}
		send(base(map[string]any{}, "stop"))
	}
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model, "choices": []any{}, "usage": usage})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	fl.Flush()
}

func messages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad json"}}`)
		return
	}
	prompt := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			switch c := req.Messages[i].Content.(type) {
			case string:
				prompt = c
			case []any:
				for _, p := range c {
					if m, ok := p.(map[string]any); ok && m["type"] == "text" {
						prompt = fmt.Sprint(m["text"])
					}
				}
			}
			break
		}
	}
	reply := fmt.Sprintf("[anthropic:%s] echo: %s", req.Model, prompt)
	id := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	if !req.Stream {
		writeJSON(w, map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model,
			"content": []map[string]any{{"type": "text", "text": reply}}, "stop_reason": "end_turn", "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 6, "cache_read_input_tokens": 3}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	send := func(ev string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, b)
		fl.Flush()
		time.Sleep(*delay)
	}
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": req.Model, "content": []any{}, "stop_reason": nil, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}})
	send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	for _, word := range strings.SplitAfter(reply, " ") {
		send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": word}})
	}
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 6}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

func embeddings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
		Input any    `json:"input"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	var inputs []string
	switch v := req.Input.(type) {
	case string:
		inputs = []string{v}
	case []any:
		for _, x := range v {
			inputs = append(inputs, fmt.Sprint(x))
		}
	}
	data := make([]map[string]any, 0, len(inputs))
	for i, s := range inputs {
		// deterministic pseudo-embedding from character histogram (8 dims)
		vec := make([]float64, 8)
		for _, ch := range s {
			vec[int(ch)%8] += 1
		}
		data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": vec})
	}
	writeJSON(w, map[string]any{"object": "list", "data": data, "model": req.Model, "usage": map[string]int{"prompt_tokens": 5, "total_tokens": 5}})
}

func responses(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model  string `json:"model"`
		Input  any    `json:"input"`
		Stream bool   `json:"stream"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &req)
	prompt := ""
	switch v := req.Input.(type) {
	case string:
		prompt = v
	case []any:
		for _, it := range v {
			if m, ok := it.(map[string]any); ok && m["role"] == "user" {
				if s, ok := m["content"].(string); ok {
					prompt = s
				}
			}
		}
	}
	reply := fmt.Sprintf("[responses:%s] echo: %s", req.Model, prompt)
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	usage := map[string]any{"input_tokens": 9, "output_tokens": 4, "total_tokens": 13, "input_tokens_details": map[string]int{"cached_tokens": 0}}
	final := map[string]any{"id": id, "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": req.Model,
		"output": []map[string]any{{"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": reply, "annotations": []any{}}}}}, "usage": usage}
	if !req.Stream {
		writeJSON(w, final)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	seq := 0
	send := func(ev string, v map[string]any) {
		v["type"] = ev
		v["sequence_number"] = seq
		seq++
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, b)
		fl.Flush()
		time.Sleep(*delay)
	}
	send("response.created", map[string]any{"response": map[string]any{"id": id, "status": "in_progress"}})
	send("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{}}})
	for _, word := range strings.SplitAfter(reply, " ") {
		send("response.output_text.delta", map[string]any{"item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": word})
	}
	send("response.output_text.done", map[string]any{"item_id": "msg_1", "output_index": 0, "content_index": 0, "text": reply})
	send("response.completed", map[string]any{"response": final})
}

// gemini mimics generateContent / streamGenerateContent (SSE with alt=sse).
func gemini(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("x-goog-api-key") == "" && r.URL.Query().Get("key") == "" {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"code":401,"message":"API key not valid","status":"UNAUTHENTICATED"}}`)
		return
	}
	name, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":")
	var req struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
		Tools []any `json:"tools"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`)
		return
	}
	prompt := ""
	for i := len(req.Contents) - 1; i >= 0; i-- {
		if req.Contents[i].Role != "model" && len(req.Contents[i].Parts) > 0 {
			prompt = req.Contents[i].Parts[len(req.Contents[i].Parts)-1].Text
			break
		}
	}
	reply := fmt.Sprintf("[gemini:%s] echo: %s", name, prompt)
	wantTool := len(req.Tools) > 0 && strings.Contains(strings.ToLower(prompt), "weather")
	usage := map[string]int{"promptTokenCount": 11, "candidatesTokenCount": 5, "totalTokenCount": 16, "cachedContentTokenCount": 2}
	cand := func(parts []map[string]any, finish string) map[string]any {
		c := map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "index": 0}
		if finish != "" {
			c["finishReason"] = finish
		}
		return c
	}
	toolPart := map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "Beijing"}}}
	switch action {
	case "generateContent":
		parts := []map[string]any{{"text": reply}}
		if wantTool {
			parts = []map[string]any{toolPart}
		}
		writeJSON(w, map[string]any{"candidates": []map[string]any{cand(parts, "STOP")}, "usageMetadata": usage, "modelVersion": name})
	case "streamGenerateContent":
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		send := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
			time.Sleep(*delay)
		}
		if wantTool {
			send(map[string]any{"candidates": []map[string]any{cand([]map[string]any{toolPart}, "STOP")}, "usageMetadata": usage, "modelVersion": name})
			return
		}
		words := strings.SplitAfter(reply, " ")
		for i, word := range words {
			finish := ""
			ev := map[string]any{"candidates": []map[string]any{cand([]map[string]any{{"text": word}}, finish)}, "modelVersion": name}
			if i == len(words)-1 {
				ev["candidates"] = []map[string]any{cand([]map[string]any{{"text": word}}, "STOP")}
				ev["usageMetadata"] = usage
			}
			send(ev)
		}
	case "countTokens":
		writeJSON(w, map[string]any{"totalTokens": len(body) / 4})
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":{"code":404,"message":"unknown method","status":"NOT_FOUND"}}`)
	}
}
