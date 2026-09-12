package compliance

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R130-01: the index reload after storing vectors is part of BuildVectors' result and
// is marked ErrIndexReload so the API can tell "vectors stored, runtime stale" apart
// from an embedding failure.
func TestR130ComplianceBuildSurfacesReloadFailure(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "r130", Action: ActionBlock, RiskLevel: RiskHigh, Enabled: true})
	mustCreate(t, db, &model.AuditSample{ID: 1, PolicyGroupID: 1, Text: "audit-130", Enabled: true})
	st := newStore(t, db, settings.Compliance{Enabled: true})
	e := New(db, st, func(_ context.Context, in []string) ([][]float32, error) { return [][]float32{{1, 0}}, nil })
	const cb = "r130:compliance_reload"
	if err := db.Callback().Query().Before("gorm:query").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == "policy_groups" {
			tx.AddError(errors.New("injected compliance index query failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	built, failed, err := e.BuildVectors(context.Background(), []uint{1})
	db.Callback().Query().Remove(cb)
	if err == nil || !errors.Is(err, ErrIndexReload) || built != 1 || failed != 0 {
		t.Fatalf("BuildVectors must report the reload failure with ErrIndexReload: built=%d failed=%d err=%v", built, failed, err)
	}
	var row model.AuditSample
	db.First(&row, 1)
	if len(row.Vector) == 0 {
		t.Fatal("vectors must still be stored")
	}
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
}

// R130-02: until a rule set has loaded, an enabled compliance engine blocks rather than
// passing; the first successful reload restores normal checking.
func TestR130ComplianceStartupCannotFailOpen(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "r130-start", Action: ActionBlock, RiskLevel: RiskHigh, Enabled: true})
	mustCreate(t, db, &model.SensitiveWord{ID: 1, PolicyGroupID: 1, Word: "blocked130", Enabled: true})
	st := newStore(t, db, settings.Compliance{Enabled: true})
	const cb = "r130:compliance_startup"
	if err := db.Callback().Query().Before("gorm:query").Register(cb, func(tx *gorm.DB) {
		if tx.Statement.Table == "policy_groups" {
			tx.AddError(errors.New("injected compliance startup query failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	e := New(db, st, nil)
	if ok, why := e.Loaded(); ok || why == "" {
		t.Fatalf("engine must report the failed load: %v %q", ok, why)
	}
	if err := e.SetVectorIdentity(func() string { return "" }); err == nil {
		t.Fatal("SetVectorIdentity must return the reload error")
	}
	for _, text := range []string{"contains blocked130", "harmless text"} {
		if v := e.Check(context.Background(), text); !v.Block || !v.Degraded {
			t.Fatalf("unloaded engine must fail closed for %q: %+v", text, v)
		}
	}
	db.Callback().Query().Remove(cb)
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
	if v := e.Check(context.Background(), "harmless text"); v.Block {
		t.Fatalf("after a successful load normal checking resumes: %+v", v)
	}
	if v := e.Check(context.Background(), "contains blocked130"); !v.Block || v.Degraded {
		t.Fatalf("configured rule must block normally: %+v", v)
	}
}
