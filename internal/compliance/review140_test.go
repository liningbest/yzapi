package compliance

import (
	"context"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R140-01: an audit sample re-claimed between claim and token read counts as failed.
func TestR140ComplianceLostClaimIsCountedAsFailed(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "g", Action: ActionAudit, RiskLevel: RiskLow, Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 1, PolicyGroupID: 1, Text: "row a", Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 2, PolicyGroupID: 1, Text: "row b", Enabled: true})
	st := newStore(t, db, settings.Compliance{Enabled: true})
	var embeds atomic.Int32
	e := New(db, st, func(_ context.Context, inputs []string) ([][]float32, error) {
		embeds.Add(1)
		out := make([][]float32, len(inputs))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		return out, nil
	})
	_ = e.SetVectorIdentity(func() string { return "id" })
	var armed atomic.Bool
	if err := db.Callback().Update().After("gorm:update").Register("t140c_after_claim", func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_samples" {
			armed.Store(true)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Query().Before("gorm:query").Register("t140c_before_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "audit_samples" && armed.Swap(false) {
			db.Session(&gorm.Session{NewDB: true}).Model(&model.AuditSample{}).Where("id = ?", 2).UpdateColumn("build_token", "someone-else")
			armed.Store(false) // the steal itself is an update: do not re-arm
		}
	}); err != nil {
		t.Fatal(err)
	}
	built, failed, err := e.BuildVectors(context.Background(), []uint{1, 2})
	if err != nil || built != 1 || failed != 1 || embeds.Load() != 1 {
		t.Fatalf("built=%d failed=%d embeds=%d err=%v", built, failed, embeds.Load(), err)
	}
	var got model.AuditSample
	db.First(&got, 2)
	if len(got.Vector) != 0 {
		t.Fatalf("the stolen row must not receive this build's vector: %+v", got)
	}
}
