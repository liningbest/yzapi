package gateway

import (
	"net/http"
	"net/url"
	"strings"
)

// DetectClient names the coding client behind a request from its User-Agent and the
// identifying headers some tools send. The label is short and stable ("claude-code",
// "codex", ...), so logs and reports can be filtered by it; anything unknown is reduced
// to its first User-Agent product token so a new tool still shows up as itself.
//
// Matching is by whole product token, never by substring: "decline/1.0" is not Cline,
// and a Referer only counts by its host name.
func DetectClient(h http.Header) string {
	// Headers that name the client explicitly take precedence over the SDK's User-Agent.
	if v := strings.ToLower(strings.TrimSpace(h.Get("x-app"))); v == "cli" || v == "claude-code" {
		return "claude-code" // Claude Code sends x-app: cli next to its anthropic-sdk User-Agent
	}
	for _, name := range []string{"originator", "X-Title", "x-client-name"} {
		if l := clientFromTokens(productTokens(h.Get(name))); l != "" {
			return l
		}
	}
	// Referer (standard) and HTTP-Referer (the OpenRouter convention several editors
	// adopted): only the host decides.
	for _, name := range []string{"Referer", "HTTP-Referer"} {
		if l := clientFromHost(h.Get(name)); l != "" {
			return l
		}
	}
	ua := strings.ToLower(strings.TrimSpace(h.Get("User-Agent")))
	if l := clientFromTokens(productTokens(ua)); l != "" {
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

// productTokens splits a header value into lower-cased product names: for a User-Agent
// the part before each "/" (comments in parentheses dropped), for a plain name the
// words. "codex_cli_rs/0.48 (Mac OS)" -> [codex_cli_rs]; "Roo Code" -> [roo code].
func productTokens(v string) []string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return nil
	}
	// Drop parenthesised comments.
	var b strings.Builder
	depth := 0
	for _, r := range v {
		switch {
		case r == '(':
			depth++
		case r == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	var out []string
	for _, f := range strings.Fields(b.String()) {
		if i := strings.IndexByte(f, '/'); i >= 0 {
			f = f[:i]
		}
		f = strings.Trim(f, ",;")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// knownProducts maps a whole product token to a client label.
var knownProducts = map[string]string{
	"claude-cli": "claude-code", "claude-code": "claude-code", "claudecode": "claude-code",
	"claude-desktop": "claude-desktop",
	"codex":          "codex", "codex_cli_rs": "codex", "codex-cli": "codex", "codex_cli": "codex",
	"geminicli": "gemini-cli", "gemini-cli": "gemini-cli",
	"opencode": "opencode",
	"roo-code": "roo-code", "roocode": "roo-code", "roo-cline": "roo-code",
	"cline":    "cline",
	"cursor":   "cursor",
	"kimi-cli": "kimi-code", "kimi-code": "kimi-code", "kimi": "kimi-code",
	"zcode":  "zcode",
	"hermes": "hermes", "hermes-agent": "hermes",
	"deepseek-harness": "deepseek-harness", "deepseek_harness": "deepseek-harness",
	"openclaw":      "openclaw",
	"continue":      "continue",
	"aider":         "aider",
	"cherry-studio": "cherry-studio", "cherrystudio": "cherry-studio",
	"chatbox":   "chatbox",
	"lobe-chat": "lobe-chat", "lobechat": "lobe-chat", "lobehub": "lobe-chat",
}

// clientFromTokens maps product tokens to a known client label. Two-word names ("roo
// code") are matched on their first word.
func clientFromTokens(toks []string) string {
	for i, t := range toks {
		if l, ok := knownProducts[t]; ok {
			return l
		}
		if t == "roo" && i+1 < len(toks) && toks[i+1] == "code" {
			return "roo-code"
		}
		if t == "cherry" && i+1 < len(toks) && toks[i+1] == "studio" {
			return "cherry-studio"
		}
	}
	return ""
}

// knownHosts maps a Referer host (or a parent domain of it) to a client label.
var knownHosts = map[string]string{
	"cursor.com": "cursor", "cursor.sh": "cursor",
	"cline.bot":     "cline",
	"roocode.com":   "roo-code",
	"opencode.ai":   "opencode",
	"continue.dev":  "continue",
	"aider.chat":    "aider",
	"cherry-ai.com": "cherry-studio",
	"chatboxai.app": "chatbox",
	"lobehub.com":   "lobe-chat",
	"openclaw.ai":   "openclaw",
}

// clientFromHost resolves a Referer-style value by its host name only.
func clientFromHost(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	host := strings.ToLower(v)
	if strings.Contains(v, "://") {
		u, err := url.Parse(v)
		if err != nil {
			return ""
		}
		host = strings.ToLower(u.Hostname())
	} else if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
	}
	for host != "" {
		if l, ok := knownHosts[host]; ok {
			return l
		}
		i := strings.IndexByte(host, '.')
		if i < 0 {
			return ""
		}
		host = host[i+1:]
	}
	return ""
}
