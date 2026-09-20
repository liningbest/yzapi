package api

import (
	"reflect"
	"testing"
)

// R143 (A2, canvas R143-01): the gateway prefix is removed as a path segment, never as a
// byte prefix; built-in routes and malformed paths are refused; results are normalised
// and deduplicated.
func TestR143NormalizeEndpoints(t *testing.T) {
	ok := map[string]string{
		"/v1/systemone": "/systemone", "systemone/": "/systemone", "v1/systemone": "/systemone", "/v1/systemone/": "/systemone",
		" /rerank ": "/rerank", "/v1beta": "/v1beta", "/v1beta/rerank": "/v1beta/rerank", "/v10/rank": "/v10/rank", "/v10/rerank": "/v10/rerank",
		"/v1/v1/x": "/x", "/a/b": "/a/b",
	}
	for in, want := range ok {
		got, msg := normalizeEndpoints([]string{in})
		if msg != "" || len(got) != 1 || got[0] != want {
			t.Fatalf("%q -> %v (%q), want %q", in, got, msg, want)
		}
	}
	bad := []string{"/v1", "/v1/", "/", "/chat/completions", "/chat", "/responses", "/messages/count_tokens", "/embeddings", "/images/generations", "/models/a", "/models",
		"/a/../b", "/a/..", "/a/./b", "/a?b", "/a#b", "/a//b", "/a b", "/a\tb", "/" + string(make([]byte, 129))}
	for _, in := range bad {
		if got, msg := normalizeEndpoints([]string{in}); msg == "" {
			t.Fatalf("accepted %q -> %v", in, got)
		}
	}
	got, msg := normalizeEndpoints([]string{"systemone/", "/v1/systemone", "", " /rerank ", "/rerank"})
	if msg != "" || !reflect.DeepEqual(got, []string{"/systemone", "/rerank"}) {
		t.Fatalf("dedupe: %v %q", got, msg)
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = "/p" + string(rune('a'+i))
	}
	if _, msg := normalizeEndpoints(many); msg == "" {
		t.Fatal("21 paths accepted")
	}
	// Idempotent: normalising an already-normalised list changes nothing.
	again, _ := normalizeEndpoints(got)
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("not idempotent: %v -> %v", got, again)
	}
}
