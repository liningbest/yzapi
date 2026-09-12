package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

func t133AccountBody(name string) map[string]any {
	return map[string]any{
		"name": name, "provider": "openai", "type": "text", "base_url": "http://127.0.0.1:1/v1", "api_key": "sk-r133",
		"protocols": []string{model.ProtoOpenAIChat}, "mappings": []map[string]string{{"request_model": "r133-model", "upstream_model": "r133-model"}},
		"weight": 1, "max_concurrency": 4, "skip_test": true,
	}
}

// R133-01a: an account retry compares the complete effective configuration; any
// difference is a 409 carrying the existing resource, never a silent 200.
func TestR133AccountRetryComparisonIsComplete(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r133-comparison")
	if w := review126Call(s, s.createAccount, admin, "/x", t133AccountBody("r133-same-name")); w.Code != http.StatusOK {
		t.Fatalf("first create: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.createAccount, admin, "/x", t133AccountBody("r133-same-name")); w.Code != http.StatusOK {
		t.Fatalf("identical retry: %d %s", w.Code, w.Body.String())
	}
	for field, value := range map[string]any{"weight": 99, "max_concurrency": 77, "enabled": false, "note": "changed", "priority": 3,
		"passthrough_models": true, "test_model": "x", "account_type": "official", "protocols": []string{model.ProtoOpenAIChat, "anthropic-messages"},
		"mappings": []map[string]string{{"request_model": "r133-new", "upstream_model": "r133-new"}}} {
		changed := t133AccountBody("r133-same-name")
		changed[field] = value
		w := review126Call(s, s.createAccount, admin, "/x", changed)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"resource"`) {
			t.Fatalf("%s differs: must be 409 with the existing resource, got %d %s", field, w.Code, w.Body.String())
		}
	}
	var n int64
	s.db.Model(&model.Account{}).Where("name = ?", "r133-same-name").Count(&n)
	if n != 1 {
		t.Fatalf("accounts: %d", n)
	}
}

// R133-01b: every create idempotency key is enforced by the database, including for
// direct inserts and for concurrent creates.
func TestR133CreateIdempotencyKeysHaveDatabaseUniqueness(t *testing.T) {
	s := auditServer(t)
	a := model.Account{Name: "r133-db-unique", Provider: "openai", Type: model.TypeText, BaseURL: "http://127.0.0.1:1", Enabled: true}
	if err := s.db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	b := a
	b.ID = 0
	if err := s.db.Create(&b).Error; !uniqueViolation(err) {
		t.Fatalf("database accepted a duplicate account name: %v", err)
	}
	pg := model.PolicyGroup{Name: "r133-unique-policy", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	w1 := model.SensitiveWord{PolicyGroupID: pg.ID, Word: "r133 duplicate word", Enabled: true}
	s.db.Create(&w1)
	w2 := w1
	w2.ID = 0
	if err := s.db.Create(&w2).Error; !uniqueViolation(err) {
		t.Fatalf("database accepted duplicate (policy_group_id, word): %v", err)
	}
	s1 := model.AuditSample{PolicyGroupID: pg.ID, Text: "r133 duplicate audit", Enabled: true}
	s.db.Create(&s1)
	if s1.TextHash != model.TextKey("r133 duplicate audit") {
		t.Fatal("hook must fill text_hash")
	}
	s2 := s1
	s2.ID = 0
	if err := s.db.Create(&s2).Error; !uniqueViolation(err) {
		t.Fatalf("database accepted duplicate (policy_group_id, text): %v", err)
	}
	r1 := model.RouteSample{Label: "simple", Text: "r133 duplicate route"}
	s.db.Create(&r1)
	r2 := r1
	r2.ID = 0
	if err := s.db.Create(&r2).Error; !uniqueViolation(err) {
		t.Fatalf("database accepted duplicate (label, text): %v", err)
	}
	// Concurrent identical creates: exactly one row, every caller gets it (200).
	admin := auditUser(s, "r133-concurrent")
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = review126Call(s, s.createRouteSample, admin, "/x", map[string]any{"label": "simple", "text": "r133 concurrent"}).Code
		}(i)
	}
	wg.Wait()
	for _, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("concurrent creates: %v", codes)
		}
	}
	var n int64
	s.db.Model(&model.RouteSample{}).Where("text = ?", "r133 concurrent").Count(&n)
	if n != 1 {
		t.Fatalf("concurrent identical creates produced %d rows", n)
	}
}

// R133-01c: the insert goes first and the database key decides; when the existing row
// cannot be read after a conflict the answer is a 500 and the table keeps exactly the
// one row it had.
func TestR133CreateLookupErrorDoesNotCreate(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r133-lookup")
	pg := model.PolicyGroup{Name: "r133-lookup-policy", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	// First creates succeed without injection.
	if w := review126Call(s, s.createAccount, admin, "/x", t133AccountBody("r133-lookup-error")); w.Code != http.StatusOK {
		t.Fatalf("account: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.createWord, admin, "/x", map[string]any{"policy_group_id": pg.ID, "word": "r133-lookup"}); w.Code != http.StatusOK {
		t.Fatalf("word: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.createAuditSample, admin, "/x", map[string]any{"policy_group_id": pg.ID, "text": "r133-lookup"}); w.Code != http.StatusOK {
		t.Fatalf("sample: %d %s", w.Code, w.Body.String())
	}
	if w := review126Call(s, s.createRouteSample, admin, "/x", map[string]any{"label": "simple", "text": "r133-lookup"}); w.Code != http.StatusOK {
		t.Fatalf("route: %d %s", w.Code, w.Body.String())
	}
	var failTable string
	if err := s.db.Callback().Query().Before("gorm:query").Register("r133:lookup_error", func(tx *gorm.DB) {
		if failTable != "" && tx.Statement.Table == failTable {
			tx.AddError(errors.New("injected idempotency lookup failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		table string
		call  func() int
		count func() int64
	}{
		{"accounts", func() int {
			return review126Call(s, s.createAccount, admin, "/x", t133AccountBody("r133-lookup-error")).Code
		},
			func() (n int64) {
				s.db.Model(&model.Account{}).Where("name = ?", "r133-lookup-error").Count(&n)
				return
			}},
		{"sensitive_words", func() int {
			return review126Call(s, s.createWord, admin, "/x", map[string]any{"policy_group_id": pg.ID, "word": "r133-lookup"}).Code
		}, func() (n int64) {
			s.db.Model(&model.SensitiveWord{}).Where("word = ?", "r133-lookup").Count(&n)
			return
		}},
		{"audit_samples", func() int {
			return review126Call(s, s.createAuditSample, admin, "/x", map[string]any{"policy_group_id": pg.ID, "text": "r133-lookup"}).Code
		}, func() (n int64) { s.db.Model(&model.AuditSample{}).Where("text = ?", "r133-lookup").Count(&n); return }},
		{"route_samples", func() int {
			return review126Call(s, s.createRouteSample, admin, "/x", map[string]any{"label": "simple", "text": "r133-lookup"}).Code
		}, func() (n int64) { s.db.Model(&model.RouteSample{}).Where("text = ?", "r133-lookup").Count(&n); return }},
	}
	for _, tc := range cases {
		failTable = tc.table
		code := tc.call()
		failTable = "" // the count below must not be intercepted
		if code != http.StatusInternalServerError {
			t.Fatalf("%s: a failed conflict read must be a 500, got %d", tc.table, code)
		}
		if n := tc.count(); n != 1 {
			t.Fatalf("%s: a failed conflict read must never insert, got %d rows", tc.table, n)
		}
	}
	failTable = ""
}

type t133RouteBuilder struct {
	RouteEngine
	builds int
}

func (r *t133RouteBuilder) Reload() error { return nil }
func (r *t133RouteBuilder) BuildVectors(context.Context, []uint) (int, int, error) {
	r.builds++
	return 0, 1, nil
}

type t133ComplianceBuilder struct {
	ComplianceEngine
	builds int
}

func (r *t133ComplianceBuilder) Reload() error { return nil }
func (r *t133ComplianceBuilder) BuildVectors(context.Context, []uint) (int, int, error) {
	r.builds++
	return 0, 1, nil
}

// R133-02: a create that hits an existing sample still runs the requested vector build
// on that row and reports it like a fresh create; differing non-key fields are a 409.
func TestR133DuplicateSampleStillRunsRequestedBuild(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r133-dup-build")
	rb, cb := &t133RouteBuilder{}, &t133ComplianceBuilder{}
	s.SetEngines(Engines{Route: rb, Compliance: cb})
	s.db.Create(&model.RouteSample{Label: "simple", Text: "r133 existing route"})
	w := review126Call(s, s.createRouteSample, admin, "/x", map[string]any{"label": "simple", "text": "r133 existing route", "build_vector": true})
	if rb.builds != 1 || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "build_error") {
		t.Fatalf("route: builds=%d status=%d body=%s", rb.builds, w.Code, w.Body.String())
	}
	if w := review126Call(s, s.createRouteSample, admin, "/x", map[string]any{"label": "simple", "text": "r133 existing route", "note": "other"}); w.Code != http.StatusConflict {
		t.Fatalf("route: differing note must be 409, got %d %s", w.Code, w.Body.String())
	}
	pg := model.PolicyGroup{Name: "r133-policy", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	s.db.Create(&model.AuditSample{PolicyGroupID: pg.ID, Text: "r133 existing audit", Enabled: true})
	w = review126Call(s, s.createAuditSample, admin, "/x", map[string]any{"policy_group_id": pg.ID, "text": "r133 existing audit", "build_vector": true})
	if cb.builds != 1 || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "build_error") {
		t.Fatalf("compliance: builds=%d status=%d body=%s", cb.builds, w.Code, w.Body.String())
	}
	if w := review126Call(s, s.createAuditSample, admin, "/x", map[string]any{"policy_group_id": pg.ID, "text": "r133 existing audit", "enabled": false}); w.Code != http.StatusConflict {
		t.Fatalf("compliance: differing enabled must be 409, got %d %s", w.Code, w.Body.String())
	}
}

type t133FailReloadRoute struct{ RouteEngine }

func (t133FailReloadRoute) Reload() error { return errors.New("injected route reload failure") }

// R133-03: the manual reload writes nothing; its failure must not claim a committed
// write and must invite a retry.
func TestR133ManualReloadFailureIsNotCommittedWrite(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r133-reload")
	s.SetEngines(Engines{Route: t133FailReloadRoute{}})
	w := review126Call(s, s.reloadRuntime, admin, "/api/admin/runtime/reload", map[string]any{})
	var body struct {
		Committed bool   `json:"committed"`
		Error     string `json:"error"`
		Failed    []runtimeFailure
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusServiceUnavailable || body.Committed || len(body.Failed) != 1 || strings.Contains(body.Error, "数据已写入数据库") || strings.Contains(body.Error, "请勿重复提交") || !strings.Contains(body.Error, "重试") {
		t.Fatalf("manual reload failure must be retryable and uncommitted: %d %s", w.Code, w.Body.String())
	}
}
