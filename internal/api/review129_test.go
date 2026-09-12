package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

type r129FailRoute struct{ RouteEngine }

func (r129FailRoute) Reload() error { return errors.New("injected route reload failure") }

type r129FailCompliance struct{ ComplianceEngine }

func (r129FailCompliance) Reload() error { return errors.New("injected compliance reload failure") }

type r129OKRoute struct {
	RouteEngine
	reloads int
}

func (r *r129OKRoute) Reload() error { r.reloads++; return nil }

// R129-01: a snapshot restore refreshes every runtime even when one fails, and reports
// all failures in one 503 that still carries the restore report.
func TestR129RestoreSurfacesEveryRuntimeReloadFailure(t *testing.T) {
	s := auditServer(t)
	okRoute := &r129OKRoute{}
	s.SetEngines(Engines{Route: okRoute, Compliance: r129FailCompliance{}})
	admin := auditUser(s, "r129-restore")
	w := r127Restore(t, s, admin, []model.ModelPrice{
		{ID: 9301, Pattern: "r129-runtime", Provider: "openai", Currency: "USD", Enabled: true},
		{ID: 9302, Pattern: "r129-bad", Provider: "openai", InputPerM: -1, Currency: "USD", Enabled: true},
	})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("restore with a failed compliance reload must answer 503: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Code    string `json:"code"`
		Failed  []runtimeFailure
		Skipped []string `json:"price_rows_skipped"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Code != "compliance_reload_failed" || len(out.Failed) != 1 || out.Failed[0].Subsystem != "compliance" || len(out.Skipped) != 1 {
		t.Fatalf("503 must name the failed runtime and keep the restore report: %s", w.Body.String())
	}
	if okRoute.reloads == 0 {
		t.Fatal("the route runtime must still be refreshed when compliance fails")
	}
	if _, ok := s.pricer.Lookup("openai", "r129-runtime"); !ok {
		t.Fatal("the price runtime must still be refreshed when compliance fails")
	}
	// Both engines failing: both listed under the generic code.
	s.SetEngines(Engines{Route: r129FailRoute{}, Compliance: r129FailCompliance{}})
	w = r127Restore(t, s, admin, []model.ModelPrice{{ID: 9303, Pattern: "r129-both", Provider: "openai", Currency: "USD", Enabled: true}})
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusServiceUnavailable || out.Code != "runtime_reload_failed" || len(out.Failed) != 2 {
		t.Fatalf("both failures must be listed: %d %s", w.Code, w.Body.String())
	}
}

// R129-01: ordinary mutation handlers answer 503 when the runtime they feed fails to
// reload, for the compliance, route, vector and gateway paths.
func TestR129MutationSurfacesRuntimeReloadFailure(t *testing.T) {
	s := auditServer(t)
	s.SetEngines(Engines{Compliance: r129FailCompliance{}, Route: r129FailRoute{}})
	admin := auditUser(s, "r129-mutation")
	if w := review126Call(s, s.putCompliance, admin, "/api/admin/settings/compliance", map[string]any{"enabled": true, "semantic_threshold": 0.8, "on_failure": "allow"}); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "compliance_reload_failed") {
		t.Fatalf("compliance settings: %d %s", w.Code, w.Body.String())
	}
	pg := model.PolicyGroup{Name: "r129-pg", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	if w := review126Call(s, s.createWord, admin, "/x", map[string]any{"policy_group_id": pg.ID, "word": "r129-word"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("word create: %d %s", w.Code, w.Body.String())
	}
	var n int64
	s.db.Model(&model.SensitiveWord{}).Where("word = ?", "r129-word").Count(&n)
	if n != 1 {
		t.Fatalf("the database write stays committed (%d rows); only the runtime refresh failed", n)
	}
	sample := model.RouteSample{Label: "simple", Text: "r129 route sample"}
	s.db.Create(&sample)
	if w := auditCall(s.deleteRouteSample, admin, sample.ID, nil); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "route_reload_failed") {
		t.Fatalf("route sample delete: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.putVector, admin, "/api/admin/settings/vector", map[string]any{"enabled": false}); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "runtime_reload_failed") {
		t.Fatalf("vector settings must list both engines: %d %s", w.Code, w.Body.String())
	}
}

// R128-01 follow-up: the transactional create also covers Account, whose Create writes
// mapping rows; a failed disable update rolls back both tables.
func TestR129DisabledAccountCreateRollsBackAssociations(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r129-account")
	const cb = "r129:fail_account_disable"
	if err := s.db.Callback().Update().Before("gorm:update").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == "accounts" {
			tx.AddError(errors.New("injected account disable failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer s.db.Callback().Update().Remove(cb)
	w := review126Call(s, s.createAccount, admin, "/api/admin/accounts", map[string]any{
		"name": "r129-account", "provider": "custom", "type": "text", "base_url": "http://127.0.0.1:1/v1", "api_key": "sk-review-only",
		"protocols": []string{"openai-completions"}, "enabled": false, "skip_test": true,
		"mappings": []map[string]any{{"request_model": "r129-model", "upstream_model": "r129-upstream"}},
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected the injected failure to surface: %d %s", w.Code, w.Body.String())
	}
	var accounts, mappings int64
	s.db.Model(&model.Account{}).Where("name = ?", "r129-account").Count(&accounts)
	s.db.Model(&model.ModelMapping{}).Where("request_model = ?", "r129-model").Count(&mappings)
	if accounts != 0 || mappings != 0 {
		t.Fatalf("account transaction left partial rows: accounts=%d mappings=%d", accounts, mappings)
	}
}
