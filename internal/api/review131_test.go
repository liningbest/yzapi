package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R131-01: saving smart-route settings refreshes the gateway snapshot (the virtual
// model lives there); a failed refresh is a 503 and the snapshot keeps the old value,
// a successful one exposes the new virtual model.
func TestR131SmartRouteSettingsSurfaceGatewayReloadFailure(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r131-smart-route")
	g1 := model.ModelGroup{Name: "r131-simple", Type: model.TypeText}
	g2 := model.ModelGroup{Name: "r131-complex", Type: model.TypeText}
	s.db.Create(&g1)
	s.db.Create(&g2)
	if err := s.st.SetVector(settings.Vector{AccountID: 999, Model: "r131-embed"}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"enabled": true, "virtual_model": "r131-auto", "simple_group_id": g1.ID, "complex_group_id": g2.ID, "threshold": 0.7, "confidence_gap": 0.1, "top_k": 5}
	before := s.gw.Snapshot().VirtualModel
	const cb = "r131:gateway_reload"
	if err := s.db.Callback().Query().Before("gorm:query").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == "accounts" {
			tx.AddError(errors.New("injected gateway snapshot query failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	w := review126Call(s, s.putSmartRoute, admin, "/api/admin/settings/smart-route", body)
	s.db.Callback().Query().Remove(cb)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "gateway_reload_failed") {
		t.Fatalf("smart-route settings must report the failed snapshot rebuild: %d %s", w.Code, w.Body.String())
	}
	if got := s.gw.Snapshot().VirtualModel; got != before {
		t.Fatalf("snapshot must keep the old virtual model on failure: before=%q after=%q", before, got)
	}
	if s.st.Get().SmartRoute.VirtualModel != "r131-auto" {
		t.Fatal("the settings write itself stays committed")
	}
	if w := review126Call(s, s.putSmartRoute, admin, "/api/admin/settings/smart-route", body); w.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	if got := s.gw.Snapshot().VirtualModel; got != "r131-auto" {
		t.Fatalf("successful save must expose the new virtual model in the live snapshot: %q", got)
	}
}

type r131PartialRoute struct{ RouteEngine }

func (r131PartialRoute) Reload() error                                          { return nil }
func (r131PartialRoute) BuildVectors(context.Context, []uint) (int, int, error) { return 0, 1, nil }

// R131-02: failed>0 with a nil error is a build failure on create, as on update.
func TestR131RouteCreateReportsFailedVector(t *testing.T) {
	s := auditServer(t)
	s.SetEngines(Engines{Route: r131PartialRoute{}})
	admin := auditUser(s, "r131-route-create")
	w := review126Call(s, s.createRouteSample, admin, "/api/admin/route/samples", map[string]any{"label": "simple", "text": "r131", "build_vector": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"build_error"`) {
		t.Fatalf("route create must report failed=1 as build_error: %d %s", w.Code, w.Body.String())
	}
}
