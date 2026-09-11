package gateway

import (
	"net/http"
	"testing"
)

func TestDetectClient(t *testing.T) {
	cases := []struct {
		ua, hdrK, hdrV, want string
	}{
		{"claude-cli/2.1.4 (external, cli)", "", "", "claude-code"},
		{"anthropic-sdk-typescript/0.55 node", "x-app", "cli", "claude-code"},
		{"codex_cli_rs/0.48.0 (Mac OS 15; arm64)", "", "", "codex"},
		{"OpenAI/JS 5.2", "originator", "codex_cli_rs", "codex"},
		{"GeminiCLI/0.12.0 (darwin; arm64)", "", "", "gemini-cli"},
		{"opencode/1.2.3", "", "", "opencode"},
		{"OpenAI/JS 4.98", "X-Title", "Cline", "cline"},
		{"OpenAI/JS 4.98", "X-Title", "Roo Code", "roo-code"},
		{"OpenAI/JS 4.98", "HTTP-Referer", "https://cursor.com", "cursor"},
		{"kimi-cli/1.0", "", "", "kimi-code"},
		{"ZCode/0.3", "", "", "zcode"},
		{"deepseek-harness/0.9", "", "", "deepseek-harness"},
		{"anthropic-python/0.40", "", "", "anthropic-sdk"},
		{"OpenAI/Python 1.55", "", "", "openai-sdk"},
		{"openai-node/5.0", "", "", "openai-sdk"},
		{"google-genai-sdk/1.2 gl-node", "", "", "genai-sdk"},
		{"curl/8.7.1", "", "", "curl"},
		{"python-requests/2.32", "", "", "python-http"},
		{"Mozilla/5.0 (Macintosh)", "", "", "browser"},
		{"myagent/1.0 (linux)", "", "", "myagent"},
		{"", "", "", "unknown"},
	}
	for _, c := range cases {
		h := http.Header{}
		if c.ua != "" {
			h.Set("User-Agent", c.ua)
		}
		if c.hdrK != "" {
			h.Set(c.hdrK, c.hdrV)
		}
		if got := DetectClient(h); got != c.want {
			t.Errorf("ua=%q %s=%q: got %q want %q", c.ua, c.hdrK, c.hdrV, got, c.want)
		}
	}
}
