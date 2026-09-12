package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"yzapi/internal/compliance"
	"yzapi/internal/gateway"
	"yzapi/internal/model"
	"yzapi/internal/routing"
)

type r130Compliance struct {
	ComplianceEngine
	err error
}

func (r130Compliance) Reload() error { return nil }
func (e r130Compliance) BuildVectors(context.Context, []uint) (int, int, error) {
	if e.err != nil {
		return 0, 1, e.err
	}
	return 1, 0, nil
}
func (r130Compliance) Test(context.Context, string) gateway.ComplianceVerdict {
	return gateway.ComplianceVerdict{}
}

type r130Route struct {
	RouteEngine
	err error
}

func (r130Route) Reload() error { return nil }
func (e r130Route) BuildVectors(context.Context, []uint) (int, int, error) {
	if e.err != nil {
		return 1, 0, e.err
	}
	return 1, 0, nil
}

// R130-03: an audit sample edit reports a failed vector build as build_error like
// create does; an index reload failure (vectors stored, runtime stale) is a 503 on
// every build path.
func TestR130AuditUpdateSurfacesBuildFailure(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r130-audit")
	group := model.PolicyGroup{Name: "r130-group", Action: "block", RiskLevel: "high", Enabled: true}
	s.db.Create(&group)
	sample := model.AuditSample{PolicyGroupID: group.ID, Text: "before", Enabled: true}
	s.db.Create(&sample)
	s.SetEngines(Engines{Compliance: r130Compliance{err: errors.New("injected audit vector build failure")}})
	w := auditCall(s.updateAuditSample, admin, sample.ID, map[string]any{"policy_group_id": group.ID, "text": "after", "enabled": true, "build_vector": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"build_error"`) {
		t.Fatalf("audit update must report the build failure: %d %s", w.Code, w.Body.String())
	}
	// Index reload failures are 503 on create, update and build-all, for both engines.
	reloadErr := fmt.Errorf("%w: injected", compliance.ErrIndexReload)
	s.SetEngines(Engines{Compliance: r130Compliance{err: reloadErr}, Route: r130Route{err: fmt.Errorf("%w: injected", routing.ErrIndexReload)}})
	if w := auditCall(s.updateAuditSample, admin, sample.ID, map[string]any{"policy_group_id": group.ID, "text": "after2", "enabled": true, "build_vector": true}); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "compliance_reload_failed") {
		t.Fatalf("audit update reload failure: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.createAuditSample, admin, "/x", map[string]any{"policy_group_id": group.ID, "text": "r130-new", "build_vector": true}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("audit create reload failure: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.buildAuditVectors, admin, "/x", map[string]any{"all": true}); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"built"`) {
		t.Fatalf("audit build-all reload failure must be 503 and keep the counts: %d %s", w.Code, w.Body.String())
	}
	rs := model.RouteSample{Label: "simple", Text: "r130 route"}
	s.db.Create(&rs)
	if w := auditCall(s.updateRouteSample, admin, rs.ID, map[string]any{"label": "simple", "text": "r130 route changed", "build_vector": true}); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "route_reload_failed") {
		t.Fatalf("route update reload failure: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.buildRouteVectors, admin, "/x", map[string]any{"all": true}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("route build-all reload failure: %d %s", w.Code, w.Body.String())
	}
}
