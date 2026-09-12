package routing

import (
	"context"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R138-02: the vector write-back is bound to the text and generation it was computed
// from: an edit or delete during the build counts as failed and writes nothing.
func TestR138RouteBuildRejectsEditedOrDeletedSample(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{})
	edited := model.RouteSample{Label: LabelSimple, Text: "old text"}
	deleted := model.RouteSample{Label: LabelSimple, Text: "doomed"}
	db.Create(&edited)
	db.Create(&deleted)
	embed := func(_ context.Context, inputs []string) ([][]float32, error) {
		db.Model(&model.RouteSample{}).Where("id = ?", edited.ID).Updates(map[string]any{"text": "new text", "text_hash": model.TextKey("new text")})
		db.Delete(&model.RouteSample{}, deleted.ID)
		out := make([][]float32, len(inputs))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		return out, nil
	}
	e := New(db, st, embed)
	_ = e.SetVectorIdentity(func() string { return "7|http://vec|embed" })
	built, failed, err := e.BuildVectors(context.Background(), []uint{edited.ID, deleted.ID})
	if err != nil || built != 0 || failed != 2 {
		t.Fatalf("built=%d failed=%d err=%v", built, failed, err)
	}
	var got model.RouteSample
	db.First(&got, edited.ID)
	if len(got.Vector) != 0 || got.Text != "new text" {
		t.Fatalf("old-text vector must not land on the edited sample: %+v", got)
	}
}

// R138-02: a slower old-generation build cannot overwrite a newer generation, while a
// repeat of the same generation still succeeds.
func TestR138OlderBuildCannotOverwriteNewerGeneration(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{})
	r := model.RouteSample{Label: LabelSimple, Text: "same text"}
	db.Create(&r)
	var current atomic.Value
	current.Store("7|http://old|old-model")
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	e := New(db, st, nil)
	_ = e.SetVectorIdentity(func() string { return current.Load().(string) })
	e.SetIdentifiedEmbed(func(_ context.Context, _ []string) ([][]float32, string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return [][]float32{{1, 0}}, "7|http://old|old-model", nil
		}
		return [][]float32{{0, 1}}, "7|http://new|new-model", nil
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
	current.Store("7|http://new|new-model")
	if built, failed, err := e.BuildVectors(context.Background(), []uint{r.ID}); err != nil || built != 1 || failed != 0 {
		t.Fatalf("new build: built=%d failed=%d err=%v", built, failed, err)
	}
	close(release)
	old := <-done
	if old.err != nil || old.built != 0 || old.failed != 1 {
		t.Fatalf("the slower old build must be counted as failed, not written: %+v", old)
	}
	var got model.RouteSample
	db.First(&got, r.ID)
	if got.VectorModel != "7|http://new|new-model" {
		t.Fatalf("slower old build overwrote the current vector: %q", got.VectorModel)
	}
	// Same generation again: allowed (idempotent rebuild).
	if built, failed, err := e.BuildVectors(context.Background(), []uint{r.ID}); err != nil || built != 1 || failed != 0 {
		t.Fatalf("same-generation rebuild: built=%d failed=%d err=%v", built, failed, err)
	}
}
