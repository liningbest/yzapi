package compliance

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/settings"
)

// R141-01: audit-sample builds conserve the claim on every exit path.
func TestR141ComplianceEarlyExitConservesClaimedRows(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, &model.PolicyGroup{ID: 1, Name: "g", Action: ActionAudit, RiskLevel: RiskLow, Enabled: true})
	var ids []uint
	for i := 0; i < 20; i++ {
		s := model.AuditSample{PolicyGroupID: 1, Text: fmt.Sprintf("row %02d", i), Enabled: true}
		mustCreate(t, db, &s)
		ids = append(ids, s.ID)
	}
	st := newStore(t, db, settings.Compliance{Enabled: true})
	var calls atomic.Int32
	mode := "short-first"
	e := New(db, st, func(_ context.Context, inputs []string) ([][]float32, error) {
		n := calls.Add(1)
		switch {
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
		t.Fatalf("wrong vector count in the first batch: built=%d failed=%d err=%v", built, failed, err)
	}
	mode = "error-second"
	calls.Store(0)
	built, failed, err = e.BuildVectors(context.Background(), ids)
	if err == nil || built != BuildBatchSize || failed != 20-BuildBatchSize {
		t.Fatalf("error in the second batch: built=%d failed=%d err=%v", built, failed, err)
	}
}
