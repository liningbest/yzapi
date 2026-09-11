package api

import (
	"testing"

	"yzapi/internal/model"
)

// R113-03: a disabled price row stays disabled after a restore (gorm's default:true
// would otherwise flip it on Create).
func TestR113RestoreDisabledPrice(t *testing.T) {
	s := auditServer(t)
	a := auditUser(s, "review113")
	p := model.ModelPrice{Pattern: "disabled-only", Provider: "custom", Currency: "USD", InputPerM: 99}
	s.db.Create(&p)
	if err := s.db.Model(&p).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	on := model.ModelPrice{Pattern: "enabled-one", Provider: "custom", Currency: "USD", InputPerM: 1}
	s.db.Create(&on)
	if err := s.snapshotConfig(a.Username, "disabled-price"); err != nil {
		t.Fatal(err)
	}
	var snap model.ConfigSnapshot
	s.db.Order("id DESC").First(&snap)
	s.db.Model(&model.ModelPrice{}).Where("id = ?", on.ID).Update("enabled", false)
	w := auditCall(s.restoreConfigSnapshot, a, snap.ID, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got model.ModelPrice
	s.db.First(&got, p.ID)
	if got.Enabled {
		t.Fatal("restore silently re-enabled disabled price")
	}
	var got2 model.ModelPrice // a fresh struct: First() also filters by a primary key already set on the receiver
	s.db.First(&got2, on.ID)
	if !got2.Enabled {
		t.Fatal("restore must bring an enabled row back enabled")
	}
	if _, ok := s.pricer.Lookup("custom", "disabled-only"); ok {
		t.Fatal("disabled row must not price after restore")
	}
}
