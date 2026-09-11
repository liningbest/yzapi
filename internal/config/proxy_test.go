package config

import (
	"net/http"
	"net/url"
	"testing"
)

// Only YZAPI_HTTP_PROXY is an unconditional proxy; the generic variables keep their
// standard semantics (NO_PROXY honoured, loopback never proxied).
func TestProxyConfig(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:7897")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7897")
	t.Setenv("YZAPI_HTTP_PROXY", "")
	if c := Load(); c.HTTPProxy != "" {
		t.Fatalf("generic proxy variables must not become an explicit proxy: %q", c.HTTPProxy)
	}
	u, _ := url.Parse("http://127.0.0.1:19911/v1/chat/completions")
	if p, err := http.ProxyFromEnvironment(&http.Request{URL: u}); err != nil || p != nil {
		t.Fatalf("loopback upstream must bypass the environment proxy, got %v %v", p, err)
	}
	t.Setenv("YZAPI_HTTP_PROXY", "http://10.0.0.1:3128")
	if c := Load(); c.HTTPProxy != "http://10.0.0.1:3128" {
		t.Fatalf("explicit proxy not read: %q", c.HTTPProxy)
	}
}
