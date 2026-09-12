package compliance

import (
	"context"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R139-01: the audit-sample write-back is ordered by the per-build claim (A→B→A).
func TestR139ComplianceCASRejectsABAGeneration(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "g", Action: ActionAudit, RiskLevel: RiskLow, Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 1, PolicyGroupID: 1, Text: "same text", Vector: []byte{1, 2}, VectorDim: 1, VectorModel: "model-B", Enabled: true})
	st := newStore(t, db, settings.Compliance{Enabled: true})
	var current atomic.Value
	current.Store("model-A")
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	e := New(db, st, nil)
	_ = e.SetVectorIdentity(func() string { return current.Load().(string) })
	e.SetIdentifiedEmbed(func(_ context.Context, _ []string) ([][]float32, string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return [][]float32{{1, 0}}, "model-A", nil
		}
		return [][]float32{{0, 1}}, "model-B", nil
	})
	type res struct {
		built, failed int
		err           error
	}
	done := make(chan res, 1)
	go func() {
		b, f, err := e.BuildVectors(context.Background(), []uint{1})
		done <- res{b, f, err}
	}()
	<-started
	current.Store("model-B")
	if b, f, err := e.BuildVectors(context.Background(), []uint{1}); err != nil || b != 1 || f != 0 {
		t.Fatalf("new B build: built=%d failed=%d err=%v", b, f, err)
	}
	close(release)
	old := <-done
	var got model.AuditSample
	db.First(&got, 1)
	if old.err != nil || old.built != 0 || old.failed != 1 || got.VectorModel != "model-B" {
		t.Fatalf("older A build passed the ABA CAS: old=%+v final=%q", old, got.VectorModel)
	}
}
