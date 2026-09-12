package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"yzapi/internal/model"
)

func review126Call(s *Server, fn gin.HandlerFunc, actor *model.User, path string, body any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	b, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(ctxUser, actor)
	fn(c)
	return w
}

// R126-02: apply writes exactly the catalog that was previewed. A remote file that
// changes between the two calls is not downloaded again; a bare apply is refused.
func TestR126ApplyCannotUseCatalogDifferentFromPreview(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review126-source-drift")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		price := "1"
		if calls.Add(1) > 1 {
			price = "9"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schemaVersion":1,"models":[{"id":"review-source-drift","inputPer1M":` + price + `,"outputPer1M":2}]}`))
	}))
	defer upstream.Close()
	body := map[string]any{"source": "url", "url": upstream.URL, "overwrite_edited": false}
	preview := review126Call(s, s.importPrices, admin, "/api/admin/prices/import", body)
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"new":[1,2,0,0]`) {
		t.Fatalf("preview failed: %d %s", preview.Code, preview.Body.String())
	}
	var pv struct {
		PlanID string `json:"plan_id"`
		SHA    string `json:"sha256"`
	}
	_ = json.Unmarshal(preview.Body.Bytes(), &pv)
	if pv.PlanID == "" || pv.SHA == "" {
		t.Fatalf("preview must return plan_id and sha256: %s", preview.Body.String())
	}
	// The old direct-apply form is refused.
	body["apply"] = true
	if w := review126Call(s, s.importPrices, admin, "/api/admin/prices/import", body); w.Code != http.StatusBadRequest {
		t.Fatalf("apply without plan_id must be refused: %d %s", w.Code, w.Body.String())
	}
	// A wrong sha is refused; the right plan id applies the previewed bytes.
	if w := review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": pv.PlanID, "sha256": "deadbeef"}); w.Code != http.StatusConflict {
		t.Fatalf("sha mismatch must be refused: %d %s", w.Code, w.Body.String())
	}
	applied := review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": pv.PlanID, "sha256": pv.SHA})
	if applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), `"applied":true`) {
		t.Fatalf("apply: %d %s", applied.Code, applied.Body.String())
	}
	var got model.ModelPrice
	if err := s.db.Where("pattern = ?", "review-source-drift").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.InputPerM != 1 {
		t.Fatalf("apply wrote a catalog that was never previewed: input=%v", got.InputPerM)
	}
	if calls.Load() != 1 {
		t.Fatalf("apply must not download again: %d fetches", calls.Load())
	}
	// A plan id is single-use.
	if w := review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": pv.PlanID}); w.Code != http.StatusConflict {
		t.Fatalf("second apply of the same plan must be refused: %d %s", w.Code, w.Body.String())
	}
	// Live pricing sees the imported row (runtime reloaded).
	if p, ok := s.pricer.Lookup("", "review-source-drift"); !ok || p.InputPerM != 1 {
		t.Fatalf("runtime price table not reloaded: %+v %v", p, ok)
	}
}

// R126-07: a preview is read-only and does not consume a configuration snapshot; one
// preview → apply cycle creates exactly one snapshot, taken by apply.
func TestR126PreviewDoesNotConsumeConfigSnapshot(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review126-preview")
	count := func() int64 {
		var n int64
		if err := s.db.Model(&model.ConfigSnapshot{}).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	for _, path := range []string{"/api/admin/prices/import", "/api/admin/prices/import-file", "/api/admin/prices/import/apply"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"apply":false}`))
		c.Set(ctxUser, admin)
		s.autoSnapshot()(c)
		if got := count(); got != before {
			t.Fatalf("%s: middleware created a rollback snapshot: before=%d after=%d", path, before, got)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schemaVersion":1,"models":{"review-snap":{"inputPer1M":1,"outputPer1M":2}}}`))
	}))
	defer upstream.Close()
	preview := review126Call(s, s.importPrices, admin, "/api/admin/prices/import", map[string]any{"source": "url", "url": upstream.URL})
	if preview.Code != http.StatusOK || count() != before {
		t.Fatalf("preview handler must not snapshot: %d %s snapshots=%d", preview.Code, preview.Body.String(), count())
	}
	var pv struct {
		PlanID string `json:"plan_id"`
	}
	_ = json.Unmarshal(preview.Body.Bytes(), &pv)
	if w := review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": pv.PlanID}); w.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	if got := count(); got != before+1 {
		t.Fatalf("one preview → apply must create exactly one snapshot: before=%d after=%d", before, got)
	}
}

// R126-05: by_client uses the same hour window as the rollup summary, so its rows add
// up to the summary even for a range that starts and ends mid-hour.
func TestR126ClientDistributionMatchesUsageWindow(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review126-window")
	hour := time.Now().Add(-2 * time.Hour).Truncate(time.Hour)
	from := hour.Add(30 * time.Minute)
	to := hour.Add(40 * time.Minute)
	if err := s.db.Create(&model.UsageHourly{Hour: hour, Requests: 1, TotalTokens: 17}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&model.CallLog{RequestID: "review126-window", Client: "codex", CreatedAt: hour.Add(10 * time.Minute), TotalTokens: 17}).Error; err != nil {
		t.Fatal(err)
	}
	// Outside the window: the next hour's log must not be counted.
	if err := s.db.Create(&model.CallLog{RequestID: "review126-window-next", Client: "codex", CreatedAt: hour.Add(61 * time.Minute), TotalTokens: 5}).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/usage?range=custom&from="+url.QueryEscape(from.Format(time.RFC3339))+"&to="+url.QueryEscape(to.Format(time.RFC3339)), nil)
	c.Set(ctxUser, admin)
	s.adminUsage(c)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Summary struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"summary"`
		ByClient []struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"by_client"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var byClient int64
	for _, row := range out.ByClient {
		byClient += row.TotalTokens
	}
	if out.Summary.TotalTokens != 17 || byClient != 17 {
		t.Fatalf("by_client must use the summary's hour window: summary=%d by_client=%d body=%s", out.Summary.TotalTokens, byClient, w.Body.String())
	}
}
