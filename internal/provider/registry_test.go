package provider

import (
	"strings"
	"testing"

	"yzapi/internal/model"
)

// Every preset must be internally consistent: valid URLs, account-type protocols within
// the provider's, and Anthropic-compatible endpoints never sharing an OpenAI base path.
func TestRegistryConsistency(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range registry {
		if seen[p.Key] {
			t.Fatalf("duplicate provider key %s", p.Key)
		}
		seen[p.Key] = true
		if !p.Custom && !strings.HasPrefix(p.BaseURL, "https://") && !strings.HasPrefix(p.BaseURL, "http://") {
			t.Fatalf("%s: base url %q", p.Key, p.BaseURL)
		}
		if len(p.Protocols) == 0 || len(p.Types) == 0 {
			t.Fatalf("%s: protocols/types empty", p.Key)
		}
		prov := map[string]bool{}
		for _, pr := range p.Protocols {
			prov[pr] = true
		}
		keys := map[string]bool{}
		for _, at := range p.AccountTypes {
			if keys[at.Key] {
				t.Fatalf("%s: duplicate account type %s", p.Key, at.Key)
			}
			keys[at.Key] = true
			for _, pr := range at.Protocols {
				if !prov[pr] {
					t.Fatalf("%s/%s: protocol %s not in provider protocols", p.Key, at.Key, pr)
				}
			}
			onlyAnthropic := len(at.Protocols) == 1 && at.Protocols[0] == model.ProtoAnthropicMessages
			if onlyAnthropic && strings.HasSuffix(at.BaseURL, "/v1") && !strings.Contains(at.BaseURL, "anthropic") {
				t.Fatalf("%s/%s: Anthropic-compatible endpoint must not reuse the OpenAI /v1 base: %s", p.Key, at.Key, at.BaseURL)
			}
		}
	}
	// The Gemini preset must offer the native endpoint (Gemini CLI) and the OpenAI-compatible one.
	g, ok := Get("gemini")
	if !ok || len(g.AccountTypes) != 2 {
		t.Fatalf("gemini preset: %+v", g)
	}
	nat, ok := g.AccountTypeOf("native")
	if !ok || len(nat.Protocols) != 1 || nat.Protocols[0] != model.ProtoGemini || strings.HasSuffix(nat.BaseURL, "/openai") {
		t.Fatalf("gemini native account type: %+v", nat)
	}
	if ProtocolType(model.ProtoGemini) != model.TypeText {
		t.Fatal("gemini protocol must be a text protocol")
	}
	if n := len(registry); n != 28 {
		t.Fatalf("registry has %d providers; update the login page and README counts", n)
	}
}
