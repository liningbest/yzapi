package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yzapi/internal/model"
)

// R143 (A1 / B1): the two usage vocabularies are aliases. The OpenAI pair wins when any
// of its fields is present, otherwise the Responses pair; nothing is added together;
// an explicit zero is a known zero, a usage block with neither pair reports nothing.
func TestR143UsageAliasesAreAlternatives(t *testing.T) {
	cases := []struct {
		name   string
		usage  string
		prompt int64
		comp   int64
		status string
	}{
		{"openai pair", `{"prompt_tokens":21,"completion_tokens":4}`, 21, 4, model.UsageConfirmed},
		{"responses pair", `{"input_tokens":21,"output_tokens":4}`, 21, 4, model.UsageConfirmed},
		{"both pairs, same values", `{"prompt_tokens":21,"completion_tokens":4,"input_tokens":21,"output_tokens":4,"total_tokens":25}`, 21, 4, model.UsageConfirmed},
		{"both pairs, conflict: openai wins", `{"prompt_tokens":21,"completion_tokens":4,"input_tokens":99,"output_tokens":99}`, 21, 4, model.UsageConfirmed},
		{"total disagrees with parts: parts win", `{"input_tokens":21,"output_tokens":4,"total_tokens":999}`, 21, 4, model.UsageConfirmed},
		{"one field of a pair", `{"prompt_tokens":21}`, 21, 0, model.UsageConfirmed},
		{"explicit zero", `{"input_tokens":0,"output_tokens":0}`, 0, 0, model.UsageConfirmed},
		{"usage block without counts", `{"cost":0.01}`, 0, 0, model.UsageUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newRecorder(respondJSON(200, `{"answers":{},"usage":`+tc.usage+`}`))
			defer up.srv.Close()
			e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"))
			if w := e.custom("/v1/systemone", e.key, `{"model":"jev","state":"s","questions":{}}`); w.Code != 200 {
				t.Fatalf("status %d %s", w.Code, w.Body.String())
			}
			log, atts := e.callLog(t)
			if log.PromptTokens != tc.prompt || log.CompletionTokens != tc.comp || log.UsageStatus != tc.status {
				t.Fatalf("log prompt=%d completion=%d status=%s; want %d/%d/%s", log.PromptTokens, log.CompletionTokens, log.UsageStatus, tc.prompt, tc.comp, tc.status)
			}
			if len(atts) != 1 || atts[0].UsageStatus != tc.status || atts[0].PromptTokens != tc.prompt || atts[0].CompletionTokens != tc.comp {
				t.Fatalf("attempts %+v", atts)
			}
		})
	}
}

// R143 (A3): "returned as-is" includes the upstream's success status; the call log
// records the same code. Converting protocols still normalise to 200.
func TestR143SuccessStatusPassthrough(t *testing.T) {
	for _, status := range []int{200, 201, 202} {
		up := newRecorder(respondJSON(status, `{"result":"created","usage":{"input_tokens":1,"output_tokens":1}}`))
		e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"))
		w := e.custom("/v1/systemone", e.key, `{"model":"jev","state":"s","questions":{}}`)
		if w.Code != status || !strings.Contains(w.Body.String(), `"created"`) {
			t.Fatalf("upstream %d became %d: %s", status, w.Code, w.Body.String())
		}
		log, _ := e.callLog(t)
		if log.StatusCode != status || log.Result != "success" || log.PromptTokens != 1 {
			t.Fatalf("log %+v", log)
		}
		up.srv.Close()
	}
	// A chat upstream answering 201 is still normalised to 200 for chat clients.
	up := newRecorder(respondJSON(201, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	defer up.srv.Close()
	e := newE2EAccounts(t, chatSpec(up.srv.URL))
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer "+e.key)
	w := httptest.NewRecorder()
	e.g.HandleChat(w, r)
	if w.Code != 200 {
		t.Fatalf("chat status %d", w.Code)
	}
}

// R143 (canvas note): a stored path under a built-in route (only reachable by editing the
// database behind the admin API) is refused on dispatch as well, so it cannot shadow a
// built-in entry point that happens to have no exact gin route.
func TestR143ReservedPathRefusedAtDataPlane(t *testing.T) {
	up := newRecorder(respondJSON(200, jevOK))
	defer up.srv.Close()
	e := newE2EAccounts(t, customSpec(up.srv.URL, "/chat/other", "/models/x", "/systemone"))
	for _, p := range []string{"/v1/chat/other", "/v1/models/x"} {
		if w := e.custom(p, e.key, `{"model":"jev","state":"s","questions":{}}`); w.Code != 404 {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
	if w := e.custom("/v1/systemone", e.key, `{"model":"jev","state":"s","questions":{}}`); w.Code != 200 {
		t.Fatalf("declared path: %d", w.Code)
	}
	if len(up.paths) != 1 {
		t.Fatalf("upstream calls %v", up.paths)
	}
	for p, want := range map[string]bool{"/chat": true, "/chat/x": true, "/models": true, "/models/a": true, "/chatty": false, "/systemone": false, "/v1beta/x": false} {
		if IsReservedEndpoint(p) != want {
			t.Fatalf("IsReservedEndpoint(%q) != %v", p, want)
		}
	}
}
