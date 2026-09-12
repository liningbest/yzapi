package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

type t136RouteBuilder struct {
	RouteEngine
	ids []uint
}

func (r *t136RouteBuilder) Reload() error { return nil }
func (r *t136RouteBuilder) BuildVectors(_ context.Context, ids []uint) (int, int, error) {
	r.ids = append(r.ids, ids...)
	return len(ids), 0, nil
}

// R136-03: rows inserted and then a failed re-read: a committed 503 with the count,
// no build, and the same batch resubmitted completes (idempotent).
func TestR136BatchRouteRequeryFailureReportsCommittedWrite(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r136-post-write-read")
	builder := &t136RouteBuilder{}
	s.SetEngines(Engines{Route: builder})
	created := false
	if err := s.db.Callback().Create().After("gorm:create").Register("t136_mark_route_created", func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" {
			created = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Callback().Query().Before("gorm:query").Register("t136_fail_post_create_query", func(tx *gorm.DB) {
		if created && tx.Statement.Table == "route_samples" {
			tx.AddError(errors.New("forced post-insert reclassification failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"items": []map[string]any{{"label": "simple", "text": "r136 committed"}}, "build_vector": true}
	w := review126Call(s, s.batchRouteSamples, admin, "/x", body)
	created = false
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"committed":true`) || !strings.Contains(w.Body.String(), `"created":1`) || len(builder.ids) != 0 {
		t.Fatalf("committed batch with a failed re-read: %d %s ids=%v", w.Code, w.Body.String(), builder.ids)
	}
	var n int64
	s.db.Model(&model.RouteSample{}).Where("text = ?", "r136 committed").Count(&n)
	if n != 1 {
		t.Fatalf("rows: %d", n)
	}
	// Resubmitting the same batch finishes the job without a duplicate.
	w = review126Call(s, s.batchRouteSamples, admin, "/x", body)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created":0`) || !strings.Contains(w.Body.String(), `"existing":1`) || len(builder.ids) != 1 {
		t.Fatalf("resubmit: %d %s ids=%v", w.Code, w.Body.String(), builder.ids)
	}
}

// R136-04: planning and re-classification use chunked set queries: a 300-item batch
// issues a handful of route SELECTs, and the item limit is enforced.
func TestR136BatchRoutePlanningIsBounded(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r136-batch-query-count")
	queries := 0
	if err := s.db.Callback().Query().Before("gorm:query").Register("t136_count_route_queries", func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	items := make([]map[string]any, 300)
	for i := range items {
		items[i] = map[string]any{"label": "simple", "text": fmt.Sprintf("r136 bulk %03d", i)}
	}
	if w := review126Call(s, s.batchRouteSamples, admin, "/x", map[string]any{"items": items}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created":300`) {
		t.Fatalf("batch: %d %s", w.Code, w.Body.String())
	}
	if queries > 10 {
		t.Fatalf("300-item batch executed %d route SELECTs", queries)
	}
	queries = 0
	if w := review126Call(s, s.batchRouteSamples, admin, "/x", map[string]any{"items": items}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"existing":300`) || !strings.Contains(w.Body.String(), `"created":0`) {
		t.Fatalf("re-import: %d %s", w.Code, w.Body.String())
	}
	if queries > 10 {
		t.Fatalf("re-import executed %d route SELECTs", queries)
	}
	too := make([]map[string]any, batchRouteMax+1)
	for i := range too {
		too[i] = map[string]any{"label": "simple", "text": fmt.Sprintf("r136 too many %d", i)}
	}
	if w := review126Call(s, s.batchRouteSamples, admin, "/x", map[string]any{"items": too}); w.Code != http.StatusBadRequest {
		t.Fatalf("over the limit must be 400: %d", w.Code)
	}
}

// R136-01: the runtime's vector identity and the shared resolver agree on a mapped
// embedding account, so the migration judges vectors by the same rule.
func TestR136RuntimeIdentityMatchesSharedResolver(t *testing.T) {
	s := auditServer(t)
	enc, _ := s.cipher.Encrypt("k")
	a := model.Account{Name: "r136-vec", Provider: "custom", Type: model.TypeEmbedding, BaseURL: "http://current", APIKeyEnc: enc, Enabled: true,
		Mappings: []model.ModelMapping{{RequestModel: "embed", UpstreamModel: "provider-embed-v2"}}}
	s.db.Create(&a)
	if err := s.st.SetVector(settingsVector(a.ID, "embed")); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d|http://current|provider-embed-v2", a.ID)
	if got := s.VectorIdentity(); got != want {
		t.Fatalf("runtime identity: %q want %q", got, want)
	}
	r, err := vector.Identity(s.db, a.ID, "embed")
	if err != nil || r.Identity != want || !r.Live {
		t.Fatalf("shared resolver: %+v %v", r, err)
	}
}

func settingsVector(id uint, model string) settings.Vector {
	return settings.Vector{AccountID: id, Model: model}
}
