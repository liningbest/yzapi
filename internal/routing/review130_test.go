package routing

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R130-01: the index reload after storing vectors is part of BuildVectors' result,
// marked ErrIndexReload; SetVectorIdentity returns its reload error.
func TestR130RouteBuildSurfacesReloadFailure(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.RouteSample{ID: 1, Label: "simple", Text: "route-130"}).Error; err != nil {
		t.Fatal(err)
	}
	st := newStore(t, db, settings.SmartRoute{Enabled: true})
	e := New(db, st, func(_ context.Context, in []string) ([][]float32, error) { return [][]float32{{1, 0}}, nil })
	const cb = "r130:route_reload"
	if err := db.Callback().Query().Before("gorm:query").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == "route_samples" && tx.Statement.SQL.Len() == 0 && len(tx.Statement.Selects) == 0 {
			tx.AddError(errors.New("injected route index query failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	built, failed, err := e.BuildVectors(context.Background(), []uint{1})
	if err == nil || !errors.Is(err, ErrIndexReload) || built != 1 || failed != 0 {
		t.Fatalf("BuildVectors must report the reload failure with ErrIndexReload: built=%d failed=%d err=%v", built, failed, err)
	}
	if err := e.SetVectorIdentity(func() string { return "" }); err == nil {
		t.Fatal("SetVectorIdentity must return the reload error")
	}
	db.Callback().Query().Remove(cb)
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
}
