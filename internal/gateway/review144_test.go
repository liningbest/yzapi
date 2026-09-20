package gateway

import (
	"strconv"
	"testing"

	"yzapi/internal/model"
)

// R144-02: the explicit-zero contract also holds on error replies and across a retry.
// A 4xx / 5xx that reports zero counts is a confirmed zero (not "none" / "unknown" by
// status), and a zero-usage 5xx followed by a successful attempt on another account
// leaves the request confirmed with the successful attempt's counts and a known cost.
func TestR144ExplicitZeroUsageOnErrorAndRetry(t *testing.T) {
	for _, status := range []int{422, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			up := newRecorder(respondJSON(status, `{"error":{"message":"rejected"},"usage":{"input_tokens":0,"output_tokens":0}}`))
			defer up.srv.Close()
			e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"))
			e.custom("/v1/systemone", e.key, `{"model":"jev","state":"s","questions":{}}`)
			log, atts := e.callLog(t)
			if len(atts) != 1 || atts[0].UsageStatus != model.UsageConfirmed || atts[0].PromptTokens != 0 || log.UsageStatus != model.UsageConfirmed {
				t.Fatalf("status %d: log usage=%s attempts=%+v", status, log.UsageStatus, atts)
			}
		})
	}
	t.Run("no usage on error keeps the status-based rule", func(t *testing.T) {
		up := newRecorder(respondJSON(500, `{"error":{"message":"rejected"}}`))
		defer up.srv.Close()
		e := newE2EAccounts(t, customSpec(up.srv.URL, "/systemone"))
		e.custom("/v1/systemone", e.key, `{"model":"jev","state":"s","questions":{}}`)
		_, atts := e.callLog(t)
		if len(atts) != 1 || atts[0].UsageStatus != model.UsageUnknown {
			t.Fatalf("attempts %+v", atts)
		}
	})
	t.Run("zero-usage 5xx then success is confirmed and priced", func(t *testing.T) {
		bad := newRecorder(respondJSON(500, `{"error":{"message":"rejected"},"usage":{"input_tokens":0,"output_tokens":0}}`))
		defer bad.srv.Close()
		good := newRecorder(respondJSON(200, jevOK))
		defer good.srv.Close()
		e := newE2EAccounts(t, customSpec(bad.srv.URL, "/systemone"), customSpec(good.srv.URL, "/systemone"))
		e.g.SetPricer(testPricer{table: map[string]float64{"jev-1.13": 100}})
		if w := e.custom("/v1/systemone", e.key, `{"model":"jev","state":"s","questions":{}}`); w.Code != 200 {
			t.Fatalf("status %d %s", w.Code, w.Body.String())
		}
		log, atts := e.callLog(t)
		if len(atts) != 2 || atts[0].UsageStatus != model.UsageConfirmed || atts[1].UsageStatus != model.UsageConfirmed {
			t.Fatalf("attempts %+v", atts)
		}
		if log.UsageStatus != model.UsageConfirmed || log.PromptTokens != 21 || log.CompletionTokens != 4 || log.CostMicros != 2500 || !log.CostKnown {
			t.Fatalf("log usage=%s prompt=%d completion=%d cost=%d known=%v", log.UsageStatus, log.PromptTokens, log.CompletionTokens, log.CostMicros, log.CostKnown)
		}
	})
}
