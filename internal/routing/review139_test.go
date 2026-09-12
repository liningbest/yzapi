package routing

import (
	"context"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R139-01: the write-back is ordered by a per-build claim, not by the repeatable
// identity: A (started first, reads B) must not overwrite the B build that started and
// finished later, even though the row shows B again at A's write time.
func TestR139RouteCASRejectsABAGeneration(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{})
	r := model.RouteSample{Label: LabelSimple, Text: "same text", Vector: []byte{1, 2}, VectorDim: 1, VectorModel: "model-B"}
	db.Create(&r)
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
		b, f, err := e.BuildVectors(context.Background(), []uint{r.ID})
		done <- res{b, f, err}
	}()
	<-started
	current.Store("model-B")
	if b, f, err := e.BuildVectors(context.Background(), []uint{r.ID}); err != nil || b != 1 || f != 0 {
		t.Fatalf("new B build: built=%d failed=%d err=%v", b, f, err)
	}
	close(release)
	old := <-done
	var got model.RouteSample
	db.First(&got, r.ID)
	if old.err != nil || old.built != 0 || old.failed != 1 || got.VectorModel != "model-B" {
		t.Fatalf("older A build passed the ABA CAS: old=%+v final=%q", old, got.VectorModel)
	}
	// A build with nil ids (all rows) claims every row and still writes.
	if b, f, err := e.BuildVectors(context.Background(), nil); err != nil || b != 1 || f != 0 {
		t.Fatalf("all-rows build: built=%d failed=%d err=%v", b, f, err)
	}
}
