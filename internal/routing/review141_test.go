package routing

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R141-01: every exit path conserves the claim: an upstream error or a wrong vector
// count in an early batch fails the current batch and every later, never-sent batch;
// rows built before the failing batch stay built; a lost claim is not double counted.
func TestR141EarlyExitConservesClaimedRows(t *testing.T) {
	db := newTestDB(t)
	st := newStore(t, db, settings.SmartRoute{})
	var ids []uint
	for i := 0; i < 20; i++ {
		r := model.RouteSample{Label: LabelSimple, Text: fmt.Sprintf("row %02d", i)}
		db.Create(&r)
		ids = append(ids, r.ID)
	}
	var calls atomic.Int32
	mode := "error-first"
	e := New(db, st, func(_ context.Context, inputs []string) ([][]float32, error) {
		n := calls.Add(1)
		switch {
		case mode == "error-first" && n == 1:
			return nil, errors.New("upstream down")
		case mode == "short-first" && n == 1:
			return [][]float32{{1, 0}}, nil
		case mode == "error-second" && n == 2:
			return nil, errors.New("upstream down")
		}
		out := make([][]float32, len(inputs))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		return out, nil
	})
	_ = e.SetVectorIdentity(func() string { return "id" })
	built, failed, err := e.BuildVectors(context.Background(), ids)
	if err == nil || built != 0 || failed != 20 {
		t.Fatalf("error in the first batch: built=%d failed=%d err=%v", built, failed, err)
	}
	mode = "short-first"
	calls.Store(0)
	built, failed, err = e.BuildVectors(context.Background(), ids)
	if err == nil || built != 0 || failed != 20 {
		t.Fatalf("wrong vector count in the first batch: built=%d failed=%d err=%v", built, failed, err)
	}
	mode = "error-second"
	calls.Store(0)
	built, failed, err = e.BuildVectors(context.Background(), ids)
	if err == nil || built != BuildBatchSize || failed != 20-BuildBatchSize {
		t.Fatalf("error in the second batch: built=%d failed=%d err=%v", built, failed, err)
	}
	// Lost claim plus an early error: the lost row is counted once.
	var armed atomic.Bool
	if err := db.Callback().Update().After("gorm:update").Register("t141_after_claim", func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" {
			armed.Store(true)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Query().Before("gorm:query").Register("t141_before_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" && armed.Swap(false) {
			db.Session(&gorm.Session{NewDB: true}).Model(&model.RouteSample{}).Where("id = ?", ids[19]).UpdateColumn("build_token", "someone-else")
			armed.Store(false)
		}
	}); err != nil {
		t.Fatal(err)
	}
	mode = "error-first"
	calls.Store(0)
	built, failed, err = e.BuildVectors(context.Background(), ids)
	if err == nil || built != 0 || failed != 20 {
		t.Fatalf("lost claim plus early error must not double count: built=%d failed=%d err=%v", built, failed, err)
	}
	// Clean run: everything built.
	db.Callback().Update().Remove("t141_after_claim")
	db.Callback().Query().Remove("t141_before_read")
	mode = "ok"
	calls.Store(0)
	if built, failed, err = e.BuildVectors(context.Background(), ids); err != nil || built != 20 || failed != 0 {
		t.Fatalf("clean: built=%d failed=%d err=%v", built, failed, err)
	}
}
