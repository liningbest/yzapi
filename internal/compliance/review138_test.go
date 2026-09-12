package compliance

import (
	"context"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R138-02: audit-sample vectors are bound to the text they were computed from; an
// edit during the build is counted as failed and the row is left without the stale
// vector. A row that merely carries another identity is stale and is rebuilt.
func TestR138ComplianceBuildRejectsEditedSample(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "g", Action: ActionAudit, RiskLevel: RiskLow, Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 1, PolicyGroupID: 1, Text: "old text", Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 2, PolicyGroupID: 1, Text: "stale", Enabled: true, VectorModel: "7|http://other|other-model", Vector: []byte{1, 2}, VectorDim: 1})
	st := newStore(t, db, settings.Compliance{Enabled: true})
	embed := func(_ context.Context, inputs []string) ([][]float32, error) {
		db.Model(&model.AuditSample{}).Where("id = ?", 1).Updates(map[string]any{"text": "new text", "text_hash": model.TextKey("new text")})
		out := make([][]float32, len(inputs))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		return out, nil
	}
	e := New(db, st, embed)
	_ = e.SetVectorIdentity(func() string { return "7|http://cur|cur-model" })
	built, failed, err := e.BuildVectors(context.Background(), []uint{1, 2})
	if err != nil || built != 1 || failed != 1 {
		t.Fatalf("edited sample refused, stale sample rebuilt: built=%d failed=%d err=%v", built, failed, err)
	}
	var got model.AuditSample
	db.First(&got, 1)
	if len(got.Vector) != 0 {
		t.Fatalf("old-text vector was written onto the edited audit sample: %+v", got)
	}
	var stale model.AuditSample // fresh receiver: First() also filters by an id already set on the struct
	db.First(&stale, 2)
	if stale.VectorModel != "7|http://cur|cur-model" {
		t.Fatalf("stale sample must be rebuilt under the current identity: %q", stale.VectorModel)
	}
}

// R138-02: a slower old-generation build cannot overwrite a newer generation written
// after it started; a repeat of the same generation still succeeds.
func TestR138ComplianceOlderBuildCannotOverwriteNewerGeneration(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "g", Action: ActionAudit, RiskLevel: RiskLow, Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 1, PolicyGroupID: 1, Text: "same text", Enabled: true})
	st := newStore(t, db, settings.Compliance{Enabled: true})
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
		b, f, err := e.BuildVectors(context.Background(), []uint{1})
		done <- res{b, f, err}
	}()
	<-started
	current.Store("7|http://new|new-model")
	if built, failed, err := e.BuildVectors(context.Background(), []uint{1}); err != nil || built != 1 || failed != 0 {
		t.Fatalf("new build: built=%d failed=%d err=%v", built, failed, err)
	}
	close(release)
	if old := <-done; old.err != nil || old.built != 0 || old.failed != 1 {
		t.Fatalf("the slower old build must be counted as failed: %+v", old)
	}
	var got model.AuditSample
	db.First(&got, 1)
	if got.VectorModel != "7|http://new|new-model" {
		t.Fatalf("slower old build overwrote the current vector: %q", got.VectorModel)
	}
	if built, failed, err := e.BuildVectors(context.Background(), []uint{1}); err != nil || built != 1 || failed != 0 {
		t.Fatalf("same-generation rebuild: built=%d failed=%d err=%v", built, failed, err)
	}
}
