package api

import (
	"encoding/json"
	"testing"
	"time"

	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/pricing"
	"yzapi/internal/settings"
)

// R115-01: a row whose currency is unverified is neither summed into the report nor
// shown as an amount; it is counted separately on every surface.
func TestR115UnverifiedCostNotReported(t *testing.T) {
	s := auditServer(t)
	admin := auditUser(s, "r115")
	if err := s.st.SetPricing(settings.Pricing{Currency: "USD", USDToCNY: 7.2}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&model.Setting{}).Where("key = ?", "cost_ledger").Update("value", "usd-v2").Error; err != nil {
		t.Fatal(err)
	}
	l := model.CallLog{RequestID: "uncertain-v2", CreatedAt: time.Now(), UserID: admin.ID, CostMicros: 7200000, CostKnown: true, PromptTokens: 1, TotalTokens: 1, UsageStatus: model.UsageConfirmed, Result: "success"}
	if err := s.db.Create(&l).Error; err != nil {
		t.Fatal(err)
	}
	if err := pricing.MigrateLedger(s.db, s.st); err != nil {
		t.Fatal(err)
	}
	s.db.First(&l, l.ID)
	if l.CostLedger != pricing.LedgerUnverified || l.CostKnown {
		t.Fatal("setup", l.CostLedger, l.CostKnown)
	}
	rows := logstore.Aggregate([]*model.CallLog{&l})
	if err := s.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	w := auditCall(s.adminUsage, admin, 0, nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var r map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	sum := r["summary"].(map[string]any)
	if sum["cost"].(float64) != 0 || sum["cost_unverified"].(float64) != 1 {
		t.Fatalf("unverified amount must be excluded and counted: cost=%v unverified=%v", sum["cost"], sum["cost_unverified"])
	}
	// Admin log list and detail, and the user's own log list.
	w = auditCall(s.listLogs, admin, 0, nil)
	var lst struct {
		Items []model.CallLog `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &lst)
	if len(lst.Items) != 1 || lst.Items[0].Cost != 0 || !lst.Items[0].CostUnverified {
		t.Fatalf("admin list: %+v", lst.Items)
	}
	w = auditCall(s.getLog, admin, l.ID, nil)
	var one model.CallLog
	_ = json.Unmarshal(w.Body.Bytes(), &one)
	if one.Cost != 0 || !one.CostUnverified || one.CostMicros != 7200000 {
		t.Fatalf("admin detail: %+v", one)
	}
	w = auditCall(s.userLogs, admin, 0, nil)
	var ul struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &ul)
	if len(ul.Items) != 1 || ul.Items[0]["cost"].(float64) != 0 || ul.Items[0]["cost_unverified"] != true {
		t.Fatalf("user list: %+v", ul.Items)
	}
}
