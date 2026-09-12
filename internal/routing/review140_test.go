package routing

import (
	"context"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R140-01: a row re-claimed by a later build between this build's claim and its token
// read is counted as failed, never as a silent 0/0 success; built + failed equals the
// rows claimed, with the whole table or an explicit, partially overlapping id set.
func TestR140LostClaimIsCountedAsFailed(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{})
	a := model.RouteSample{Label: LabelSimple, Text: "row a"}
	b := model.RouteSample{Label: LabelSimple, Text: "row b"}
	db.Create(&a)
	db.Create(&b)
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
	// Fence: right after a claim update on route_samples and before the token read, a
	// competing build re-claims row b.
	var armed atomic.Bool
	var steal atomic.Bool
	if err := db.Callback().Update().After("gorm:update").Register("t140_after_claim", func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" && steal.Load() {
			armed.Store(true)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Query().Before("gorm:query").Register("t140_before_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" && armed.Swap(false) {
			db.Session(&gorm.Session{NewDB: true}).Model(&model.RouteSample{}).Where("id = ?", b.ID).UpdateColumn("build_token", "someone-else")
			armed.Store(false) // the steal itself is an update: do not re-arm
		}
	}); err != nil {
		t.Fatal(err)
	}
	steal.Store(true)
	built, failed, err := e.BuildVectors(context.Background(), []uint{a.ID, b.ID})
	if err != nil || built != 1 || failed != 1 || embeds.Load() != 1 {
		t.Fatalf("partial overlap: built=%d failed=%d embeds=%d err=%v", built, failed, embeds.Load(), err)
	}
	var got model.RouteSample
	db.First(&got, b.ID)
	if len(got.Vector) != 0 || got.BuildToken != "someone-else" {
		t.Fatalf("the stolen row belongs to the later build: %+v", got)
	}
	// Whole table, every row stolen: 0 built, all counted failed, embed never runs.
	if err := db.Callback().Query().Replace("t140_before_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" && armed.Swap(false) {
			db.Session(&gorm.Session{NewDB: true}).Model(&model.RouteSample{}).Where("1 = 1").UpdateColumn("build_token", "someone-else")
			armed.Store(false) // the steal itself is an update: do not re-arm
		}
	}); err != nil {
		t.Fatal(err)
	}
	embeds.Store(0)
	built, failed, err = e.BuildVectors(context.Background(), nil)
	if err != nil || built != 0 || failed != 2 || embeds.Load() != 0 {
		t.Fatalf("all stolen: built=%d failed=%d embeds=%d err=%v", built, failed, embeds.Load(), err)
	}
	// No competitor: conservation with built only.
	steal.Store(false)
	built, failed, err = e.BuildVectors(context.Background(), nil)
	if err != nil || built != 2 || failed != 0 {
		t.Fatalf("clean build: built=%d failed=%d err=%v", built, failed, err)
	}
}
