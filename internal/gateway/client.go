package gateway

import (
	"net/http"
	"strings"
)

// DetectClient names the coding client behind a request from its User-Agent and the
// identifying headers some tools send. The label is short and stable ("claude-code",
// "codex", ...), so logs and reports can be filtered by it; anything unknown is reduced
// to its first User-Agent product token so a new tool still shows up as itself.
func DetectClient(h http.Header) string {
	ua := strings.ToLower(strings.TrimSpace(h.Get("User-Agent")))
	// Headers that name the client explicitly take precedence over the SDK's User-Agent.
	for _, v := range []string{h.Get("x-app"), h.Get("originator"), h.Get("X-Title"), h.Get("x-client-name"), h.Get("HTTP-Referer")} {
		if l := clientFromToken(strings.ToLower(strings.TrimSpace(v))); l != "" {
			return l
		}
	}
	if l := clientFromToken(ua); l != "" {
		return l
	}
	switch {
	case ua == "":
		return "unknown"
	case strings.HasPrefix(ua, "anthropic-"):
		return "anthropic-sdk"
	case strings.HasPrefix(ua, "openai/") || strings.HasPrefix(ua, "openai-"):
		return "openai-sdk"
	case strings.Contains(ua, "google-genai") || strings.HasPrefix(ua, "genai-"):
		return "genai-sdk"
	case strings.HasPrefix(ua, "curl/"):
		return "curl"
	case strings.HasPrefix(ua, "python-requests") || strings.HasPrefix(ua, "python-httpx") || strings.HasPrefix(ua, "httpx/"):
		return "python-http"
	case strings.HasPrefix(ua, "node") || strings.HasPrefix(ua, "undici") || strings.HasPrefix(ua, "axios"):
		return "node-http"
	case strings.HasPrefix(ua, "mozilla/"):
		return "browser"
	}
	// First product token, e.g. "myagent/1.2 (linux)" -> "myagent".
	tok := ua
	if i := strings.IndexAny(tok, "/ ("); i > 0 {
		tok = tok[:i]
	}
	if len(tok) > 24 {
		tok = tok[:24]
	}
	if tok == "" {
		return "unknown"
	}
	return tok
}

// clientFromToken maps a header value or User-Agent to a known client label.
func clientFromToken(v string) string {
	if v == "" {
		return ""
	}
	switch {
	case strings.Contains(v, "claude-cli") || strings.Contains(v, "claude-code") || v == "cli":
		return "claude-code"
	case strings.Contains(v, "claude-desktop"):
		return "claude-desktop"
	case strings.Contains(v, "codex"):
		return "codex"
	case strings.Contains(v, "geminicli") || strings.Contains(v, "gemini-cli"):
		return "gemini-cli"
	case strings.Contains(v, "opencode"):
		return "opencode"
	case strings.Contains(v, "roo code") || strings.Contains(v, "roo-code") || strings.Contains(v, "roocode"):
		return "roo-code"
	case strings.Contains(v, "cline"):
		return "cline"
	case strings.Contains(v, "cursor"):
		return "cursor"
	case strings.Contains(v, "kimi"):
		return "kimi-code"
	case strings.Contains(v, "zcode"):
		return "zcode"
	case strings.Contains(v, "hermes"):
		return "hermes"
	case strings.Contains(v, "deepseek-harness") || strings.Contains(v, "deepseek_harness"):
		return "deepseek-harness"
	case strings.Contains(v, "openclaw"):
		return "openclaw"
	case strings.Contains(v, "continue"):
		return "continue"
	case strings.Contains(v, "aider"):
		return "aider"
	case strings.Contains(v, "cherry"):
		return "cherry-studio"
	case strings.Contains(v, "chatbox"):
		return "chatbox"
	case strings.Contains(v, "lobe"):
		return "lobe-chat"
	}
	return ""
}
