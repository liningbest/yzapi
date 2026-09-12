package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"yzapi/internal/model"
)

// R127-01: a plan id is claimed atomically; two concurrent applies of one id yield
// exactly one 200 and one 409, and a failed apply releases the claim for a retry.
func TestR127PlanIDIsAtomicallySingleUse(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r127-plan-race")
	var fetches atomic.Int32 // each preview serves a new model so every apply has something to write
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schemaVersion":1,"models":{"r127-plan-race-` + strconv.Itoa(int(fetches.Add(1))) + `":{"inputPer1M":1,"outputPer1M":2}}}`))
	}))
	defer upstream.Close()
	preview := func() string {
		pv := review126Call(s, s.importPrices, admin, "/api/admin/prices/import", map[string]any{"source": "url", "url": upstream.URL})
		if pv.Code != http.StatusOK {
			t.Fatalf("preview: %d %s", pv.Code, pv.Body.String())
		}
		var plan struct {
			PlanID string `json:"plan_id"`
		}
		_ = json.Unmarshal(pv.Body.Bytes(), &plan)
		return plan.PlanID
	}
	id := preview()
	// Hold the serialiser so both requests pass the lookup before either finishes.
	s.imports.applying.Lock()
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			codes <- review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": id}).Code
		}()
	}
	time.Sleep(250 * time.Millisecond)
	s.imports.applying.Unlock()
	a, b := <-codes, <-codes
	if !((a == http.StatusOK && b == http.StatusConflict) || (b == http.StatusOK && a == http.StatusConflict)) {
		t.Fatalf("single-use plan must produce one 200 and one 409, got %d and %d", a, b)
	}
	// A sha mismatch does not consume the plan; a failed write releases it.
	id = preview()
	if w := review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": id, "sha256": "bad"}); w.Code != http.StatusConflict {
		t.Fatalf("sha mismatch: %d", w.Code)
	}
	var writes atomic.Int32
	var failWrite atomic.Bool
	failWrite.Store(true)
	if err := s.db.Callback().Create().Before("gorm:create").Register("r127:fail_price_write", func(tx *gorm.DB) {
		if tx.Statement.Table == "model_prices" && failWrite.Load() {
			writes.Add(1)
			tx.AddError(errors.New("injected write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if w := review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": id}); w.Code != http.StatusInternalServerError || writes.Load() == 0 {
		t.Fatalf("failed write must surface: %d %s", w.Code, w.Body.String())
	}
	failWrite.Store(false)
	if w := review126Call(s, s.importApply, admin, "/api/admin/prices/import/apply", map[string]any{"plan_id": id}); w.Code != http.StatusOK {
		t.Fatalf("retry after a failed write must work with the same plan: %d %s", w.Code, w.Body.String())
	}
	if plans, _ := s.imports.pending(); plans != 0 {
		t.Fatalf("applied plans must be dropped: %d pending", plans)
	}
}

func r127Restore(t *testing.T, s *Server, admin *model.User, prices []model.ModelPrice) *httptest.ResponseRecorder {
	t.Helper()
	p := &configPayload{Version: 2, Settings: map[string]string{}, Prices: &prices, Meta: map[string]int{"prices": len(prices)}}
	raw, err := sealPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	snap := model.ConfigSnapshot{Actor: admin.Username, Reason: "pre-1.0.27", Data: model.JSON(raw), CreatedAt: time.Now()}
	if err := s.db.Create(&snap).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	id := strconv.FormatUint(uint64(snap.ID), 10)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/config/snapshots/"+id+"/restore", strings.NewReader(`{}`))
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Set(ctxUser, admin)
	s.restoreConfigSnapshot(c)
	return w
}

// R127-02: a snapshot from before the unique key is normalised on restore: keys are
// lower-cased, case-duplicates and exact duplicates collapse to the preferred row,
// disabled rows stay disabled, and rows failing today's validation are dropped and
// reported rather than restored.
func TestR127OldSnapshotPricesAreNormalizedAndDeduped(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r127-old-snapshot")
	w := r127Restore(t, s, admin, []model.ModelPrice{
		{ID: 9001, Pattern: "R127-Dup", Provider: "OpenAI", InputPerM: 1, OutputPerM: 2, Currency: "USD", Enabled: true},
		{ID: 9002, Pattern: "r127-dup", Provider: "openai", InputPerM: 3, OutputPerM: 4, Currency: "USD", Enabled: true, Edited: true},
		{ID: 9003, Pattern: "r127-same", Provider: "openai", InputPerM: 1, OutputPerM: 1, Currency: "USD", Enabled: true, Builtin: true},
		{ID: 9004, Pattern: "r127-same", Provider: "openai", InputPerM: 2, OutputPerM: 2, Currency: "USD", Enabled: true},
		{ID: 9005, Pattern: "r127-off", Provider: "gemini", InputPerM: 1, OutputPerM: 1, Currency: "USD", Enabled: false},
		{ID: 9006, Pattern: "r127-bad", Provider: "gemini", InputPerM: -5, OutputPerM: 1, Currency: "USD", Enabled: true},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("restore old snapshot: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Skipped []string `json:"price_rows_skipped"`
		Merged  int      `json:"price_rows_merged"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Skipped) != 1 || out.Merged != 2 {
		t.Fatalf("restore must report dropped rows: %s", w.Body.String())
	}
	var rows []model.ModelPrice
	s.db.Order("id").Find(&rows)
	byID := map[uint]model.ModelPrice{}
	for _, r := range rows {
		byID[r.ID] = r
		if r.Provider != strings.ToLower(r.Provider) || r.Pattern != strings.ToLower(r.Pattern) {
			t.Fatalf("restored key not lower-cased: %+v", r)
		}
	}
	if len(rows) != 3 || byID[9002].InputPerM != 3 || byID[9004].InputPerM != 2 || byID[9005].Enabled || byID[9006].ID != 0 {
		t.Fatalf("restored rows: %+v", rows)
	}
	if _, ok := s.pricer.Lookup("openai", "r127-dup"); !ok {
		t.Fatal("runtime not reloaded with the restored table")
	}
}

// R127-03: every create handler with a default:true Enabled column honours
// enabled:false (gorm writes the default back into the struct after Create).
func TestR127CreateDisabledStaysDisabled(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r127-disabled")
	if w := review126Call(s, s.createPrice, admin, "/api/admin/prices", map[string]any{"pattern": "r127-disabled-price", "provider": "openai", "input_per_m": 1, "output_per_m": 2, "currency": "USD", "enabled": false}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("create price: %d %s", w.Code, w.Body.String())
	}
	var price model.ModelPrice
	if err := s.db.Where("pattern = ?", "r127-disabled-price").First(&price).Error; err != nil || price.Enabled {
		t.Fatalf("price requested as disabled was stored enabled: %v %+v", err, price)
	}
	if _, ok := s.pricer.Lookup("openai", "r127-disabled-price"); ok {
		t.Fatal("disabled price must not price requests")
	}
	if w := review126Call(s, s.createUserGroup, admin, "/api/admin/groups", map[string]any{"name": "r127-off-group", "enabled": false}); w.Code != http.StatusOK {
		t.Fatalf("create group: %d %s", w.Code, w.Body.String())
	}
	var g model.UserGroup
	if err := s.db.Where("name = ?", "r127-off-group").First(&g).Error; err != nil || g.Enabled {
		t.Fatalf("group requested as disabled was stored enabled: %v %+v", err, g)
	}
	if w := review126Call(s, s.createPolicyGroup, admin, "/api/admin/compliance/policy-groups", map[string]any{"name": "r127-off-policy", "action": "audit", "risk_level": "low", "enabled": false}); w.Code != http.StatusOK {
		t.Fatalf("create policy group: %d %s", w.Code, w.Body.String())
	}
	var pg model.PolicyGroup
	if err := s.db.Where("name = ?", "r127-off-policy").First(&pg).Error; err != nil || pg.Enabled {
		t.Fatalf("policy group requested as disabled was stored enabled: %v %+v", err, pg)
	}
	if w := review126Call(s, s.createWord, admin, "/api/admin/compliance/words", map[string]any{"policy_group_id": pg.ID, "word": "r127-off-word", "enabled": false}); w.Code != http.StatusOK {
		t.Fatalf("create word: %d %s", w.Code, w.Body.String())
	}
	var word model.SensitiveWord
	if err := s.db.Where("word = ?", "r127-off-word").First(&word).Error; err != nil || word.Enabled {
		t.Fatalf("word requested as disabled was stored enabled: %v %+v", err, word)
	}
	if w := review126Call(s, s.createAuditSample, admin, "/api/admin/compliance/samples", map[string]any{"policy_group_id": pg.ID, "text": "r127-off-sample", "enabled": false}); w.Code != http.StatusOK {
		t.Fatalf("create sample: %d %s", w.Code, w.Body.String())
	}
	var sample model.AuditSample
	if err := s.db.Where("text = ?", "r127-off-sample").First(&sample).Error; err != nil || sample.Enabled {
		t.Fatalf("sample requested as disabled was stored enabled: %v %+v", err, sample)
	}
}

// R127-04: a snapshot restore whose price reload fails answers 503, never 200.
func TestR127SnapshotRestoreSurfacesPriceReloadFailure(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r127-restore-reload")
	var priceReads atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	if err := s.db.Callback().Query().Before("gorm:query").Register("r127:fail_price_reload", func(tx *gorm.DB) {
		if tx.Statement.Table == "model_prices" && priceReads.Add(1) >= 2 && fail.Load() {
			tx.AddError(errors.New("injected price reload failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	w := r127Restore(t, s, admin, []model.ModelPrice{{ID: 9101, Pattern: "r127-restored-price", Provider: "openai", InputPerM: 7, OutputPerM: 8, Currency: "USD", Enabled: true}})
	fail.Store(false)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "price_reload_failed") {
		t.Fatalf("restore with a failed price reload must answer 503 price_reload_failed: %d %s", w.Code, w.Body.String())
	}
}
