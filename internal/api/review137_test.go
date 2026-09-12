package api

import (
	"net/http"
	"sync"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
)

// R137-01: the vector client is one account generation. An update landing between
// reads can only yield the complete old or the complete new client, never the old
// secret against the new endpoint.
func TestR137VectorClientDoesNotMixAccountGenerations(t *testing.T) {
	s := auditServer(t)
	oldEnc, _ := s.cipher.Encrypt("old-secret")
	newEnc, _ := s.cipher.Encrypt("new-secret")
	a := model.Account{Name: "r137-vector", Provider: "custom", Type: model.TypeEmbedding, BaseURL: "http://old-provider", APIKeyEnc: oldEnc, Enabled: true,
		Mappings: []model.ModelMapping{{RequestModel: "embed", UpstreamModel: "old-model"}}}
	s.db.Create(&a)
	var once sync.Once
	var switchErr error
	// Fire on the first read that touches the account (raw or model query).
	if err := s.db.Callback().Query().After("gorm:query").Register("t137_switch_generation", func(tx *gorm.DB) {
		once.Do(func() {
			db := s.db.Session(&gorm.Session{NewDB: true, SkipHooks: true})
			if err := db.Exec("UPDATE accounts SET base_url = ?, api_key_enc = ? WHERE id = ?", "http://new-provider", newEnc, a.ID).Error; err != nil {
				switchErr = err
				return
			}
			switchErr = db.Exec("UPDATE model_mappings SET upstream_model = ? WHERE account_id = ? AND request_model = ?", "new-model", a.ID, "embed").Error
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Callback().Raw().After("gorm:raw").Register("t137_switch_generation_raw", func(tx *gorm.DB) {
		once.Do(func() {
			db := s.db.Session(&gorm.Session{NewDB: true, SkipHooks: true})
			if err := db.Exec("UPDATE accounts SET base_url = ?, api_key_enc = ? WHERE id = ?", "http://new-provider", newEnc, a.ID).Error; err != nil {
				switchErr = err
				return
			}
			switchErr = db.Exec("UPDATE model_mappings SET upstream_model = ? WHERE account_id = ? AND request_model = ?", "new-model", a.ID, "embed").Error
		})
	}); err != nil {
		t.Fatal(err)
	}
	cl, msg := s.vectorClient(a.ID, "embed")
	if switchErr != nil {
		t.Fatal(switchErr)
	}
	if cl == nil {
		t.Fatalf("client: %s", msg)
	}
	old := cl.APIKey == "old-secret" && cl.BaseURL == "http://old-provider" && cl.Model == "old-model"
	newer := cl.APIKey == "new-secret" && cl.BaseURL == "http://new-provider" && cl.Model == "new-model"
	if !old && !newer {
		t.Fatalf("client is not one complete account generation: key=%q url=%q model=%q", cl.APIKey, cl.BaseURL, cl.Model)
	}
	// Missing, wrong-type and disabled accounts keep their messages.
	if cl, msg := s.vectorClient(9999, "embed"); cl != nil || msg == "" {
		t.Fatal("missing account must be reported")
	}
	s.db.Model(&model.Account{}).Where("id = ?", a.ID).Update("enabled", false)
	if cl, msg := s.vectorClient(a.ID, "embed"); cl != nil || msg == "" {
		t.Fatal("disabled account must be reported")
	}
	if w := review126Call(s, s.testVector, auditUser(s, "r137-admin"), "/x", map[string]any{"account_id": 9999, "model": "x"}); w.Code == http.StatusInternalServerError {
		t.Fatalf("vector test with a missing account must not be a 500: %d %s", w.Code, w.Body.String())
	}
}
