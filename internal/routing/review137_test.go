package routing

import (
	"context"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/settings"
	"yzapi/internal/vector"
)

// R137-02: vectors are stored under the identity that produced them, both with a
// plain embed function (identity fixed before the call) and with an identified one
// (identity reported by the snapshot that embedded).
func TestR137BuildKeepsEmbeddingGeneration(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{})
	r := model.RouteSample{Label: LabelSimple, Text: "generation race"}
	db.Create(&r)
	var identity atomic.Value
	identity.Store("7|http://old|old-model")
	embed := func(_ context.Context, inputs []string) ([][]float32, error) {
		identity.Store("7|http://new|new-model") // the configuration switches while the request is in flight
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
	if built, failed, err := e.BuildVectors(context.Background(), []uint{r.ID}); err != nil || built != 1 || failed != 0 {
		t.Fatalf("build: built=%d failed=%d err=%v", built, failed, err)
	}
	var got model.RouteSample
	db.First(&got, r.ID)
	if got.VectorModel != "7|http://old|old-model" || vector.Compatible(got.VectorModel, "7|http://new|new-model") {
		t.Fatalf("old-model embedding was stamped as %q", got.VectorModel)
	}
	if len(e.Samples()) != 0 {
		t.Fatalf("the new runtime must not load the old-model vector: %d", len(e.Samples()))
	}
	// Identified embed: the identity comes from the embedding snapshot itself.
	e.SetIdentifiedEmbed(func(_ context.Context, inputs []string) ([][]float32, string, error) {
		out := make([][]float32, len(inputs))
		for i := range out {
			out[i] = []float32{0, 1}
		}
		return out, "snapshot-identity", nil
	})
	if _, _, err := e.BuildVectors(context.Background(), []uint{r.ID}); err != nil {
		t.Fatal(err)
	}
	db.First(&got, r.ID)
	if got.VectorModel != "snapshot-identity" {
		t.Fatalf("identified embed must stamp its own identity: %q", got.VectorModel)
	}
}
