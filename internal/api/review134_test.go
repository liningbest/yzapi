package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"yzapi/internal/model"
)

type t134RouteBuilder struct {
	RouteEngine
	ids []uint
}

func (r *t134RouteBuilder) Reload() error { return nil }
func (r *t134RouteBuilder) BuildVectors(_ context.Context, ids []uint) (int, int, error) {
	r.ids = append(r.ids, ids...)
	return len(ids), 0, nil
}

// R134-03: a batch import that meets existing samples still builds them when asked;
// a differing note is a reported conflict; the response carries every count.
func TestR134BatchRouteBuildsExistingSamples(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r134-route-batch")
	builder := &t134RouteBuilder{}
	s.SetEngines(Engines{Route: builder})
	existing := model.RouteSample{Label: "simple", Text: "r134 existing unvectorized"}
	s.db.Create(&existing)
	noted := model.RouteSample{Label: "simple", Text: "r134 noted", Note: "keep"}
	s.db.Create(&noted)
	w := review126Call(s, s.batchRouteSamples, admin, "/x", map[string]any{
		"items":        []map[string]any{{"label": "simple", "text": existing.Text}, {"label": "simple", "text": "r134 noted", "note": "other"}, {"label": "simple", "text": "r134 brand new"}, {"label": "simple", "text": "r134 brand new"}},
		"build_vector": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"created":1`, `"existing":1`, `"skipped":1`, `"conflicts":1`, `"built":2`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("batch response must carry %s: %s", want, w.Body.String())
		}
	}
	if len(builder.ids) != 2 || builder.ids[0] != existing.ID && builder.ids[1] != existing.ID {
		t.Fatalf("existing sample must be built with the new one: ids=%v", builder.ids)
	}
	var n int64
	s.db.Model(&model.RouteSample{}).Count(&n)
	if n != 3 {
		t.Fatalf("rows: %d", n)
	}
}

// R134-04: a word batch is idempotent against existing rows and concurrent batches:
// no 500, the new words land, the counts are real.
func TestR134BatchWordsSkipsExistingRows(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r134-word-batch")
	pg := model.PolicyGroup{Name: "r134-policy", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	s.db.Create(&model.SensitiveWord{PolicyGroupID: pg.ID, Word: "existing", Enabled: true})
	w := review126Call(s, s.batchWords, admin, "/x", map[string]any{"policy_group_id": pg.ID, "words": []string{"existing", "new-word"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"created":1`) || !strings.Contains(w.Body.String(), `"skipped":1`) {
		t.Fatalf("batch with an existing word: %d %s", w.Code, w.Body.String())
	}
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = review126Call(s, s.batchWords, admin, "/x", map[string]any{"policy_group_id": pg.ID, "words": []string{"race-a", "race-b"}}).Code
		}(i)
	}
	wg.Wait()
	for _, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("concurrent batches: %v", codes)
		}
	}
	var n int64
	s.db.Model(&model.SensitiveWord{}).Where("policy_group_id = ?", pg.ID).Count(&n)
	if n != 4 {
		t.Fatalf("expected existing, new-word, race-a, race-b: %d rows", n)
	}
}

// R134-05: unique conflicts on edit are 409 with the conflicting resource.
func TestR134UpdateUniqueConflictsAre409(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r134-update")
	for _, name := range []string{"r134-account-one", "r134-account-two"} {
		if w := review126Call(s, s.createAccount, admin, "/x", t133AccountBody(name)); w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
	}
	var two model.Account
	s.db.Where("name = ?", "r134-account-two").First(&two)
	changed := t133AccountBody("r134-account-one")
	changed["api_key"] = "******"
	if w := auditCall(s.updateAccount, admin, two.ID, changed); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "account_exists") || !strings.Contains(w.Body.String(), `"resource"`) {
		t.Fatalf("duplicate account name on update: %d %s", w.Code, w.Body.String())
	}
	pg := model.PolicyGroup{Name: "r134-update-policy", Action: "audit", RiskLevel: "low", Enabled: true}
	s.db.Create(&pg)
	one := model.SensitiveWord{PolicyGroupID: pg.ID, Word: "one", Enabled: true}
	tw := model.SensitiveWord{PolicyGroupID: pg.ID, Word: "two", Enabled: true}
	s.db.Create(&one)
	s.db.Create(&tw)
	if w := auditCall(s.updateWord, admin, tw.ID, map[string]any{"policy_group_id": pg.ID, "word": "one", "enabled": true}); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "word_exists") {
		t.Fatalf("duplicate word key on update: %d %s", w.Code, w.Body.String())
	}
	// The translated driver error is what uniqueViolation relies on in production.
	if err := s.db.Create(&model.SensitiveWord{PolicyGroupID: pg.ID, Word: "one"}).Error; !uniqueViolation(err) {
		t.Fatalf("driver error must be recognised as a duplicate key: %v", err)
	}
}
