package api

import (
	"reflect"
	"testing"
)

// R144-01: normalisation is idempotent for every accepted input. Leading "/v1" segments
// are all removed on the first save, so a stored path never begins with one and saving
// the stored list back changes nothing; a repeated prefix that resolves to a reserved
// route is refused on the first save rather than accepted and re-cut later.
func TestR144NormalizeIdempotent(t *testing.T) {
	inputs := []string{"/v1/v1/systemone", "/v1/v1/v1/rerank", "v1/v1/x", "/v1/v1beta/y", "/v1/v10/z", "/systemone", "/v1beta", "/v10/rank", " /a/b/ ", "/v1/systemone/"}
	want := map[string]string{"/v1/v1/systemone": "/systemone", "/v1/v1/v1/rerank": "/rerank", "v1/v1/x": "/x", "/v1/v1beta/y": "/v1beta/y", "/v1/v10/z": "/v10/z",
		"/systemone": "/systemone", "/v1beta": "/v1beta", "/v10/rank": "/v10/rank", " /a/b/ ": "/a/b", "/v1/systemone/": "/systemone"}
	for _, in := range inputs {
		once, msg := normalizeEndpoints([]string{in})
		if msg != "" || len(once) != 1 || once[0] != want[in] {
			t.Fatalf("%q -> %v (%q), want %q", in, once, msg, want[in])
		}
		twice, msg := normalizeEndpoints(once)
		if msg != "" || !reflect.DeepEqual(once, twice) {
			t.Fatalf("%q: not idempotent: %v -> %v (%q)", in, once, twice, msg)
		}
	}
	all, msg := normalizeEndpoints(inputs)
	if msg != "" {
		t.Fatal(msg)
	}
	if again, msg := normalizeEndpoints(all); msg != "" || !reflect.DeepEqual(all, again) {
		t.Fatalf("list not idempotent: %v -> %v (%q)", all, again, msg)
	}
	for _, in := range []string{"/v1/v1/chat/other", "/v1/v1/models/x", "/v1/v1", "/v1/v1/"} {
		if got, msg := normalizeEndpoints([]string{in}); msg == "" {
			t.Fatalf("accepted %q -> %v", in, got)
		}
	}
}
