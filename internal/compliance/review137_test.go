package compliance

import (
	"context"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

// R137-02: audit-sample vectors are stored under the identity that produced them.
func TestR137ComplianceBuildKeepsEmbeddingGeneration(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "g", Action: ActionAudit, RiskLevel: RiskLow, Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 1, PolicyGroupID: 1, Text: "generation race", Enabled: true})
	st := newStore(t, db, settings.Compliance{Enabled: true})
	var identity atomic.Value
	identity.Store("7|http://old|old-model")
	embed := func(_ context.Context, inputs []string) ([][]float32, error) {
		identity.Store("7|http://new|new-model")
		out := make([][]float32, len(inputs))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		return out, nil
	}
	e := New(db, st, embed)
	if err := e.SetVectorIdentity(func() string { return identity.Load().(string) }); err != nil {
		t.Fatal(err)
	}
	if built, failed, err := e.BuildVectors(context.Background(), []uint{1}); err != nil || built != 1 || failed != 0 {
		t.Fatalf("build: built=%d failed=%d err=%v", built, failed, err)
	}
	var got model.AuditSample
	db.First(&got, 1)
	if got.VectorModel != "7|http://old|old-model" || vector.Compatible(got.VectorModel, "7|http://new|new-model") {
		t.Fatalf("old-model embedding was stamped as %q", got.VectorModel)
	}
	e.SetIdentifiedEmbed(func(_ context.Context, inputs []string) ([][]float32, string, error) {
		out := make([][]float32, len(inputs))
		for i := range out {
			out[i] = []float32{0, 1}
		}
		return out, "snapshot-identity", nil
	})
	if _, _, err := e.BuildVectors(context.Background(), []uint{1}); err != nil {
		t.Fatal(err)
	}
	db.First(&got, 1)
	if got.VectorModel != "snapshot-identity" {
		t.Fatalf("identified embed must stamp its own identity: %q", got.VectorModel)
	}
}
