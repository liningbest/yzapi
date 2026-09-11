package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"

	"yzapi/internal/gateway/convert"
	"yzapi/internal/model"
)

// protoPath returns the path (and query) appended to an account's base URL for one call.
// Gemini addresses the model in the URL, so it needs the upstream model and stream flag.
func protoPath(proto, upstreamModel string, stream bool) string {
	switch proto {
	case model.ProtoGemini:
		if stream {
			return "/models/" + upstreamModel + ":streamGenerateContent?alt=sse"
		}
		return "/models/" + upstreamModel + ":generateContent"
	case model.ProtoOpenAIChat:
		return "/chat/completions"
	case model.ProtoOpenAIResponses:
		return "/responses"
	case model.ProtoAnthropicMessages:
		return "/messages"
	case model.ProtoOpenAIEmbeddings:
		return "/embeddings"
	case model.ProtoOpenAIImages:
		return "/images/generations"
	}
	return "/"
}

// upstreamCall is one attempt against one account.
type upstreamCall struct {
	up      *Upstream
	proto   string
	model   string // upstream model name (needed for URL-addressed protocols)
	body    []byte
	stream  bool
	headers http.Header // selected client headers to forward
	sent    *bool       // set once the request has been written to the upstream connection
}

type attemptRecord struct {
	AccountID   uint   `json:"account_id"`
	AccountName string `json:"account_name"`
	Provider    string `json:"provider"`
	Protocol    string `json:"protocol"`
	Model       string `json:"model"`
	StatusCode  int    `json:"status_code"`
	LatencyMs   int64  `json:"latency_ms"`
	Error       string `json:"error,omitempty"`
	// Per-attempt usage: only the attempt that produced the response carries counts;
	// failed attempts are "none" (rejected before generation) or "unknown".
	UsageStatus      string `json:"usage_status"`
	PromptTokens     int64  `json:"prompt_tokens,omitempty"`
	CompletionTokens int64  `json:"completion_tokens,omitempty"`
	CachedTokens     int64  `json:"cached_tokens,omitempty"`
	CacheWriteTokens int64  `json:"cache_write_tokens,omitempty"`
	CostMicros       int64  `json:"cost_micros,omitempty"` // ledger currency (USD), 1e-6 units
	CostKnown        bool   `json:"cost_known,omitempty"`
}

// doUpstream sends the request and returns the response without reading the body.
func (g *Gateway) doUpstream(ctx context.Context, c *upstreamCall) (*http.Response, error) {
	if c.sent != nil {
		// Once the request is on the wire the upstream may bill it even if we never see
		// a response, so record that moment for usage classification.
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { *c.sent = true }})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.up.BaseURL+protoPath(c.proto, c.model, c.stream), bytes.NewReader(c.body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "yzapi-gateway/1.0")
	req.ContentLength = int64(len(c.body))
	if c.proto == model.ProtoGemini {
		// Google validates an Authorization header when present, so send only the
		// API-key header it expects (OpenAI-compatible relays accept it too).
		req.Header.Set("x-goog-api-key", c.up.APIKey)
	} else if c.proto == model.ProtoAnthropicMessages {
		req.Header.Set("x-api-key", c.up.APIKey)
		req.Header.Set("Authorization", "Bearer "+c.up.APIKey)
		ver := c.headers.Get("anthropic-version")
		if ver == "" {
			ver = "2023-06-01"
		}
		req.Header.Set("anthropic-version", ver)
		if b := c.headers.Get("anthropic-beta"); b != "" {
			req.Header.Set("anthropic-beta", b)
		}
	} else {
		if c.up.AuthHeader == "x-api-key" {
			req.Header.Set("x-api-key", c.up.APIKey)
		}
		req.Header.Set("Authorization", "Bearer "+c.up.APIKey)
		if b := c.headers.Get("OpenAI-Beta"); b != "" {
			req.Header.Set("OpenAI-Beta", b)
		}
	}
	if c.stream {
		req.Header.Set("Accept", "text/event-stream")
		// Never let an upstream (or a proxy in front of it) gzip an event stream: the
		// compressor batches tokens into blocks, so they arrive late and in bursts. An
		// explicit Accept-Encoding also stops Go's transparent gzip negotiation.
		req.Header.Set("Accept-Encoding", "identity")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	return g.client.Load().Do(req)
}

// readErrorBody extracts a human-readable message from an upstream error response.
func readErrorBody(resp *http.Response) (string, []byte) {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if e.Error.Message != "" {
			return e.Error.Message, raw
		}
		if e.Message != "" {
			return e.Message, raw
		}
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		s = http.StatusText(resp.StatusCode)
	}
	return s, raw
}

// retryable decides whether a failed attempt should move on to the next account.
func retryable(status int) bool {
	switch {
	case status == 401, status == 403, status == 402:
		return true // account-level problem
	case status == 408, status == 429:
		return true
	case status >= 500:
		return true
	case status == 404:
		return true // model not found at this upstream; try another
	}
	return false
}

// idleReader cancels the request when no bytes arrive within the idle timeout.
type idleReader struct {
	r      io.ReadCloser
	timer  *time.Timer
	idle   time.Duration
	cancel context.CancelFunc
	fired  atomic.Bool
}

func newIdleReader(r io.ReadCloser, idle time.Duration, cancel context.CancelFunc) *idleReader {
	ir := &idleReader{r: r, idle: idle, cancel: cancel}
	if idle > 0 {
		ir.timer = time.AfterFunc(idle, func() { ir.fired.Store(true); cancel() })
	}
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if ir.timer != nil && n > 0 {
		ir.timer.Reset(ir.idle)
	}
	if err != nil && ir.fired.Load() {
		return n, errors.New("stream idle timeout")
	}
	return n, err
}

func (ir *idleReader) Close() error {
	if ir.timer != nil {
		ir.timer.Stop()
	}
	return ir.r.Close()
}

// usageFromJSON extracts token usage from a non-streaming response body.
func usageFromJSON(proto string, raw []byte) (u convert.Usage, ok bool) {
	switch proto {
	case model.ProtoGemini:
		var r struct {
			UsageMetadata *convert.GeminiUsage `json:"usageMetadata"`
		}
		if json.Unmarshal(raw, &r) != nil || r.UsageMetadata == nil {
			return u, false
		}
		return geminiUsage(r.UsageMetadata), true
	case model.ProtoAnthropicMessages:
		var r struct {
			Usage convert.AnthropicUsage `json:"usage"`
		}
		if json.Unmarshal(raw, &r) != nil {
			return u, false
		}
		u.PromptTokens = r.Usage.InputTokens + r.Usage.CacheReadInputTokens + r.Usage.CacheCreationInputTokens
		u.CacheWriteTokens = r.Usage.CacheCreationInputTokens
		u.CompletionTokens = r.Usage.OutputTokens
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
		if r.Usage.CacheReadInputTokens > 0 {
			u.PromptTokensDetails = &struct {
				CachedTokens int `json:"cached_tokens"`
			}{CachedTokens: r.Usage.CacheReadInputTokens}
		}
		return u, u.TotalTokens > 0
	case model.ProtoOpenAIResponses:
		var r struct {
			Usage *struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				TotalTokens        int `json:"total_tokens"`
				InputTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &r) != nil || r.Usage == nil {
			return u, false
		}
		u.PromptTokens, u.CompletionTokens, u.TotalTokens = r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.TotalTokens
		if r.Usage.InputTokensDetails.CachedTokens > 0 {
			u.PromptTokensDetails = &struct {
				CachedTokens int `json:"cached_tokens"`
			}{CachedTokens: r.Usage.InputTokensDetails.CachedTokens}
		}
		return u, true
	default:
		var r struct {
			Usage *convert.Usage `json:"usage"`
		}
		if json.Unmarshal(raw, &r) != nil || r.Usage == nil {
			return u, false
		}
		u = *r.Usage
		if u.TotalTokens == 0 {
			u.TotalTokens = u.PromptTokens + u.CompletionTokens
		}
		return u, true
	}
}

// passthroughStream copies SSE from upstream to client while sniffing usage.
// dropUsageOnly removes OpenAI usage-only chunks that the client did not ask for.
// It returns convert.ErrIncomplete when the stream ends without its terminal event and
// an upstreamError when the upstream signalled a failure inside the stream.
func passthroughStream(r io.Reader, w io.Writer, flush func(), proto string, dropUsageOnly bool) (convert.Usage, bool, error) {
	var usage convert.Usage
	known := false
	done := false
	var upstreamErr error
	rd := convert.NewSSEReader(r)
	for {
		ev, err := rd.Next()
		if err == io.EOF {
			switch {
			case upstreamErr != nil:
				return usage, known, upstreamErr
			case !done:
				return usage, known, convert.ErrIncomplete
			}
			return usage, known, nil
		}
		if err != nil {
			return usage, known, err
		}
		data := ev.Data
		switch proto {
		case model.ProtoGemini:
			var e struct {
				Candidates []struct {
					FinishReason string `json:"finishReason"`
				} `json:"candidates"`
				UsageMetadata *convert.GeminiUsage `json:"usageMetadata"`
				Error         *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(data), &e) == nil {
				if e.Error != nil {
					upstreamErr = &upstreamError{msg: "upstream error event: " + e.Error.Message}
				}
				if e.UsageMetadata != nil {
					usage = geminiUsage(e.UsageMetadata)
					known = true
				}
				for _, c := range e.Candidates {
					if c.FinishReason != "" {
						done = true
					}
				}
			}
		case model.ProtoAnthropicMessages:
			switch ev.Event {
			case "message_stop":
				done = true
			case "error":
				var e struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				_ = json.Unmarshal([]byte(data), &e)
				upstreamErr = &upstreamError{msg: "upstream error event: " + e.Error.Message}
			}
			if ev.Event == "message_start" || ev.Event == "message_delta" || ev.Event == "error" {
				var e struct {
					Type    string `json:"type"`
					Message *struct {
						Usage convert.AnthropicUsage `json:"usage"`
					} `json:"message"`
					Usage *convert.AnthropicUsage `json:"usage"`
				}
				if json.Unmarshal([]byte(data), &e) == nil {
					if e.Message != nil {
						usage.PromptTokens = e.Message.Usage.InputTokens + e.Message.Usage.CacheReadInputTokens + e.Message.Usage.CacheCreationInputTokens
						usage.CacheWriteTokens = e.Message.Usage.CacheCreationInputTokens
						if e.Message.Usage.CacheReadInputTokens > 0 {
							usage.PromptTokensDetails = &struct {
								CachedTokens int `json:"cached_tokens"`
							}{CachedTokens: e.Message.Usage.CacheReadInputTokens}
						}
						known = true
					}
					if e.Usage != nil {
						usage.CompletionTokens = e.Usage.OutputTokens
						if e.Usage.InputTokens > 0 {
							usage.PromptTokens = e.Usage.InputTokens + e.Usage.CacheReadInputTokens + e.Usage.CacheCreationInputTokens
							usage.CacheWriteTokens = e.Usage.CacheCreationInputTokens
						}
						known = true
					}
				}
			}
		case model.ProtoOpenAIResponses:
			switch ev.Event {
			case "response.completed", "response.incomplete":
				done = true
			case "response.failed", "error":
				var e struct {
					Message  string `json:"message"`
					Response *struct {
						Error *struct {
							Message string `json:"message"`
						} `json:"error"`
					} `json:"response"`
				}
				_ = json.Unmarshal([]byte(data), &e)
				msg := e.Message
				if msg == "" && e.Response != nil && e.Response.Error != nil {
					msg = e.Response.Error.Message
				}
				upstreamErr = &upstreamError{msg: "upstream error event: " + msg}
			}
			if strings.HasSuffix(ev.Event, "completed") || strings.HasSuffix(ev.Event, "incomplete") || strings.Contains(data, `"usage"`) {
				var e struct {
					Response *struct {
						Usage *struct {
							InputTokens        int `json:"input_tokens"`
							OutputTokens       int `json:"output_tokens"`
							TotalTokens        int `json:"total_tokens"`
							InputTokensDetails struct {
								CachedTokens int `json:"cached_tokens"`
							} `json:"input_tokens_details"`
						} `json:"usage"`
					} `json:"response"`
				}
				if json.Unmarshal([]byte(data), &e) == nil && e.Response != nil && e.Response.Usage != nil {
					usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens = e.Response.Usage.InputTokens, e.Response.Usage.OutputTokens, e.Response.Usage.TotalTokens
					if e.Response.Usage.InputTokensDetails.CachedTokens > 0 {
						usage.PromptTokensDetails = &struct {
							CachedTokens int `json:"cached_tokens"`
						}{CachedTokens: e.Response.Usage.InputTokensDetails.CachedTokens}
					}
					known = true
				}
			}
		default: // openai chat
			if data == "[DONE]" {
				done = true
			} else if strings.HasPrefix(data, "{") && strings.Contains(data, `"error"`) {
				var e struct {
					Error *struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal([]byte(data), &e) == nil && e.Error != nil {
					upstreamErr = &upstreamError{msg: "upstream error event: " + e.Error.Message}
				}
			}
			if data != "[DONE]" && strings.Contains(data, `"usage"`) {
				var e struct {
					Choices []json.RawMessage `json:"choices"`
					Usage   *convert.Usage    `json:"usage"`
				}
				if json.Unmarshal([]byte(data), &e) == nil && e.Usage != nil {
					usage = *e.Usage
					if usage.TotalTokens == 0 {
						usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
					}
					known = true
					if dropUsageOnly && len(e.Choices) == 0 {
						continue
					}
				}
			}
		}
		if err := convert.WriteSSE(w, ev.Event, data); err != nil {
			return usage, known, err
		}
		flush()
	}
}

// geminiUsage folds Gemini usageMetadata into the OpenAI usage shape (thoughts count as output).
func geminiUsage(g *convert.GeminiUsage) convert.Usage {
	u := convert.Usage{PromptTokens: g.PromptTokenCount, CompletionTokens: g.CandidatesTokenCount + g.ThoughtsTokenCount, TotalTokens: g.TotalTokenCount}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	if g.CachedContentTokenCount > 0 {
		u.PromptTokensDetails = &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: g.CachedContentTokenCount}
	}
	return u
}

// upstreamError marks a failure the upstream reported inside an otherwise-200 stream.
type upstreamError struct{ msg string }

func (e *upstreamError) Error() string { return e.msg }

func (u *Upstream) mapModel(reqModel string) string {
	if m, ok := u.Mappings[reqModel]; ok && m != "" {
		return m
	}
	return reqModel
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
