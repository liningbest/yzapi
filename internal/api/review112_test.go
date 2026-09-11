package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// Independent acceptance findings R112-01/02/05 (docs/acceptance-review-1.0.12-2026-09-11.md),
// adopted with the fixes.

// R112-01: a restore must bring the encrypted key back, and the gateway must be able to
// use it (the decrypted key is what reaches the upstream).
func TestR112RestoreKeepsCredential(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review112")
	w := auditCall(s.createAccount, admin, 0, map[string]any{"name": "keyed", "provider": "custom", "type": "text", "base_url": "http://127.0.0.1:1", "api_key": "sk-local-test", "skip_test": true,
		"mappings": []map[string]string{{"request_model": "m", "upstream_model": "m"}}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var a model.Account
	s.db.First(&a)
	if a.APIKeyEnc == "" {
		t.Fatal("setup missing key")
	}
	want := a.APIKeyEnc
	if err := s.snapshotConfig(admin.Username, "credential"); err != nil {
		t.Fatal(err)
	}
	var snap model.ConfigSnapshot
	s.db.Order("id desc").First(&snap)
	// The stored payload carries the key; the browser summary never does.
	if !strings.Contains(string(snap.Data), `"api_key_enc":"`+want+`"`) {
		t.Fatal("snapshot payload must contain the encrypted key")
	}
	if w := auditCall(s.getConfigSnapshot, admin, snap.ID, nil); strings.Contains(w.Body.String(), want) || strings.Contains(w.Body.String(), "api_key_enc") {
		t.Fatal("snapshot summary leaks the encrypted key")
	}
	// Corrupt the live key, then restore.
	s.db.Model(&model.Account{}).Where("id = ?", a.ID).Update("api_key_enc", "")
	w = auditCall(s.restoreConfigSnapshot, admin, snap.ID, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got model.Account
	s.db.First(&got, a.ID)
	if got.APIKeyEnc != want {
		t.Fatalf("restore lost encrypted credential: before present=%v after present=%v", want != "", got.APIKeyEnc != "")
	}
	if up := s.gw.Snapshot().Accounts[a.ID]; up == nil || up.APIKey != "sk-local-test" {
		t.Fatalf("gateway must see the decrypted key after restore: %+v", up)
	}
	var res struct {
		MissingKeys []string `json:"missing_keys"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.MissingKeys) != 0 {
		t.Fatalf("no key should be missing: %v", res.MissingKeys)
	}
}

// R112-01 follow-up: v1 snapshots (no key, no version) keep the key the same account has
// now; an account whose key is nowhere to be found comes back disabled and is reported.
func TestR112RestoreV1SnapshotWithoutKeys(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review112v1")
	w := auditCall(s.createAccount, admin, 0, map[string]any{"name": "keep", "provider": "custom", "type": "text", "base_url": "http://127.0.0.1:1", "api_key": "sk-keep", "skip_test": true,
		"mappings": []map[string]string{{"request_model": "m", "upstream_model": "m"}}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var keep model.Account
	s.db.First(&keep)
	v1 := map[string]any{
		"accounts": []map[string]any{
			{"id": keep.ID, "name": "keep", "provider": "custom", "type": "text", "base_url": "http://127.0.0.1:1", "protocols": []string{"openai-completions"}, "enabled": true, "mappings": []map[string]any{{"request_model": "m", "upstream_model": "m"}}},
			{"id": keep.ID + 7, "name": "gone", "provider": "custom", "type": "text", "base_url": "http://127.0.0.1:1", "protocols": []string{"openai-completions"}, "enabled": true},
		},
		"model_groups": []any{}, "group_links": []any{}, "settings": map[string]string{}, "meta": map[string]int{},
	}
	raw, _ := json.Marshal(v1)
	snap := model.ConfigSnapshot{Actor: "old", Reason: "v1", Data: model.JSON(raw), CreatedAt: time.Now()}
	s.db.Create(&snap)
	w = auditCall(s.restoreConfigSnapshot, admin, snap.ID, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var res struct {
		MissingKeys []string `json:"missing_keys"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.MissingKeys) != 1 || res.MissingKeys[0] != "gone" {
		t.Fatalf("missing keys: %v", res.MissingKeys)
	}
	var back model.Account
	s.db.First(&back, keep.ID)
	if back.APIKeyEnc != keep.APIKeyEnc || !back.Enabled {
		t.Fatalf("existing key must be kept: %+v", back)
	}
	var gone model.Account
	s.db.Where("name = ?", "gone").First(&gone)
	if gone.Enabled || gone.APIKeyEnc != "" || !strings.Contains(gone.Note, "密钥缺失") {
		t.Fatalf("keyless account must come back disabled and flagged: %+v", gone)
	}
	// Prices absent from a v1 payload: the current table is left alone.
	var n int64
	s.db.Model(&model.ModelPrice{}).Count(&n)
	if n == 0 {
		t.Fatal("v1 snapshot without a prices field must not wipe the price table")
	}
}

// A tampered payload is refused before anything is deleted.
func TestR112RestoreRejectsCorruptSnapshot(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review112sum")
	if err := s.snapshotConfig(admin.Username, "ok"); err != nil {
		t.Fatal(err)
	}
	var snap model.ConfigSnapshot
	s.db.Order("id desc").First(&snap)
	tampered := strings.Replace(string(snap.Data), `"version":2`, `"version":3`, 1)
	if tampered == string(snap.Data) {
		t.Fatal("setup: version marker not found")
	}
	s.db.Model(&model.ConfigSnapshot{}).Where("id = ?", snap.ID).Update("data", tampered)
	var before int64
	s.db.Model(&model.ModelPrice{}).Count(&before)
	w := auditCall(s.restoreConfigSnapshot, admin, snap.ID, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "snapshot_corrupt") {
		t.Fatalf("want 409 snapshot_corrupt, got %d %s", w.Code, w.Body.String())
	}
	var after int64
	s.db.Model(&model.ModelPrice{}).Count(&after)
	if after != before {
		t.Fatal("a refused restore must not touch the price table")
	}
}

// R112-05: an empty price set is a state to restore, not "leave prices alone".
func TestR112RestoreEmptyPrices(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review112empty")
	s.db.Exec("DELETE FROM model_prices")
	if err := s.snapshotConfig(admin.Username, "empty"); err != nil {
		t.Fatal(err)
	}
	var snap model.ConfigSnapshot
	s.db.Order("id desc").First(&snap)
	p := model.ModelPrice{Pattern: "later", Provider: "custom", Currency: "USD"}
	if err := s.db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	w := auditCall(s.restoreConfigSnapshot, admin, snap.ID, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int64
	s.db.Model(&model.ModelPrice{}).Count(&n)
	if n != 0 {
		t.Fatalf("restoring empty prices kept %d later rows", n)
	}
}

// R112-02: stored amounts are a USD ledger; switching the display currency converts
// history instead of relabeling it, so 1 USD reads as 7.2 CNY, never as 1 CNY.
func TestR112CurrencyDoesNotRelabelHistory(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "review112currency")
	if err := s.st.SetPricing(settings.Pricing{Currency: "USD", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	row := model.UsageHourly{Hour: time.Now().Truncate(time.Hour), CostMicros: 1000000, Requests: 1}
	if err := s.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	cost := func() (float64, string) {
		w := auditCall(s.adminUsage, admin, 0, nil)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var rep map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &rep)
		return rep["summary"].(map[string]any)["cost"].(float64), rep["currency"].(string)
	}
	if c, cur := cost(); c != 1 || cur != "USD" {
		t.Fatalf("setup: %v %s", c, cur)
	}
	if err := s.st.SetPricing(settings.Pricing{Currency: "CNY", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	c, cur := cost()
	if cur != "CNY" || c < 7.2-1e-6 || c > 7.2+1e-6 {
		t.Fatalf("historical 1 USD must display as 7.2 CNY, got %v %s", c, cur)
	}
	// A new call priced now and the old row aggregate in one currency.
	s.db.Create(&model.UsageHourly{Hour: time.Now().Truncate(time.Hour), CostMicros: 500000, Requests: 1, UserID: 9})
	if c, _ := cost(); c < 1.5*7.2-1e-6 || c > 1.5*7.2+1e-6 {
		t.Fatalf("mixed rows must sum in the ledger then convert: %v", c)
	}
}
