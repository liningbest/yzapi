package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

type t135RouteBuilder struct {
	RouteEngine
	ids []uint
}

func (r *t135RouteBuilder) Reload() error { return nil }
func (r *t135RouteBuilder) BuildVectors(_ context.Context, ids []uint) (int, int, error) {
	r.ids = append(r.ids, ids...)
	return len(ids), 0, nil
}

// R135-03: the batch import compares notes exactly like a single create.
func TestR135BatchRouteReportsEmptyNoteConflict(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r135-route-note")
	s.db.Create(&model.RouteSample{Label: "simple", Text: "r135 note", Note: "keep me"})
	s.db.Create(&model.RouteSample{Label: "simple", Text: "r135 blank"})
	w := review126Call(s, s.batchRouteSamples, admin, "/x", map[string]any{"items": []map[string]any{{"label": "simple", "text": "r135 note", "note": ""}, {"label": "simple", "text": "r135 blank", "note": "added"}, {"label": "simple", "text": "r135 blank", "note": ""}}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"conflicts":2`) || !strings.Contains(w.Body.String(), `"existing":0`) || !strings.Contains(w.Body.String(), `"skipped":1`) {
		t.Fatalf("note differences must be conflicts: %d %s", w.Code, w.Body.String())
	}
}

// R135-04: a row inserted by someone else between the read and the insert is counted
// as existing (not created) and still built; `created` is the database's count.
func TestR135BatchRouteReportsActualCreatedCount(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r135-route-race")
	builder := &t135RouteBuilder{}
	s.SetEngines(Engines{Route: builder})
	var once sync.Once
	if err := s.db.Callback().Create().Before("gorm:create").Register("r135_concurrent_route", func(tx *gorm.DB) {
		if tx.Statement.Table != "route_samples" {
			return
		}
		once.Do(func() {
			tx.Exec(`INSERT INTO route_samples (label,text,text_hash,note,threshold,vector_dim,vector_model,created_at,updated_at) VALUES (?,?,?,?,0,0,'',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
				"simple", "r135 raced", model.TextKey("r135 raced"), "")
		})
	}); err != nil {
		t.Fatal(err)
	}
	w := review126Call(s, s.batchRouteSamples, admin, "/x", map[string]any{"items": []map[string]any{{"label": "simple", "text": "r135 raced"}, {"label": "simple", "text": "r135 created"}}, "build_vector": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created":1`) || !strings.Contains(w.Body.String(), `"existing":1`) || !strings.Contains(w.Body.String(), `"built":2`) || len(builder.ids) != 2 {
		t.Fatalf("race: ids=%v body=%s", builder.ids, w.Body.String())
	}
	var n int64
	s.db.Model(&model.RouteSample{}).Count(&n)
	if n != 2 {
		t.Fatalf("rows: %d", n)
	}
	// A racing row whose note differs is a conflict, not built.
	s.db.Callback().Create().Remove("r135_concurrent_route")
	var once2 sync.Once
	if err := s.db.Callback().Create().Before("gorm:create").Register("r135_concurrent_route2", func(tx *gorm.DB) {
		if tx.Statement.Table != "route_samples" {
			return
		}
		once2.Do(func() {
			tx.Exec(`INSERT INTO route_samples (label,text,text_hash,note,threshold,vector_dim,vector_model,created_at,updated_at) VALUES (?,?,?,?,0,0,'',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
				"simple", "r135 raced2", model.TextKey("r135 raced2"), "other")
		})
	}); err != nil {
		t.Fatal(err)
	}
	builder.ids = nil
	w = review126Call(s, s.batchRouteSamples, admin, "/x", map[string]any{"items": []map[string]any{{"label": "simple", "text": "r135 raced2"}}, "build_vector": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created":0`) || !strings.Contains(w.Body.String(), `"conflicts":1`) || len(builder.ids) != 0 {
		t.Fatalf("race with a different note: ids=%v body=%s", builder.ids, w.Body.String())
	}
}

// R135-05: a failed read of the conflicting resource on an edit is a 500, never a
// fabricated 409, for accounts and words.
func TestR135EditConflictLookupFailureIs500(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r135-lookup")
	for _, name := range []string{"r135 account one", "r135 account two"} {
		if w := review126Call(s, s.createAccount, admin, "/x", t133AccountBody(name)); w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
	}
	var second model.Account
	s.db.Where("name = ?", "r135 account two").First(&second)
	pg := model.PolicyGroup{Name: "r135-policy", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	one := model.SensitiveWord{PolicyGroupID: pg.ID, Word: "one", Enabled: true}
	two := model.SensitiveWord{PolicyGroupID: pg.ID, Word: "two", Enabled: true}
	s.db.Create(&one)
	s.db.Create(&two)
	var failTable string
	var seen int
	if err := s.db.Callback().Query().Before("gorm:query").Register("r135_conflict_lookup_error", func(tx *gorm.DB) {
		if failTable != "" && tx.Statement.Table == failTable {
			seen++
			if seen >= 2 { // the handler's own load succeeds; the conflict read fails
				tx.AddError(errors.New("forced conflict lookup failure"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	failTable, seen = "accounts", 0
	changed := t133AccountBody("r135 account one")
	changed["api_key"] = "******"
	if w := auditCall(s.updateAccount, admin, second.ID, changed); w.Code != http.StatusInternalServerError {
		t.Fatalf("account conflict lookup failure must be a 500: %d %s", w.Code, w.Body.String())
	}
	failTable, seen = "sensitive_words", 0
	if w := auditCall(s.updateWord, admin, two.ID, map[string]any{"policy_group_id": pg.ID, "word": "one", "enabled": true}); w.Code != http.StatusInternalServerError {
		t.Fatalf("word conflict lookup failure must be a 500: %d %s", w.Code, w.Body.String())
	}
	failTable = ""
	var a model.Account
	s.db.First(&a, second.ID)
	var wd model.SensitiveWord
	s.db.First(&wd, two.ID)
	if a.Name != "r135 account two" || wd.Word != "two" {
		t.Fatalf("the failed updates must stay rolled back: %q %q", a.Name, wd.Word)
	}
}
