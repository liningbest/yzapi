package backup

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// Policy is the automatic-backup setting the scheduler reads on every tick.
type Policy struct {
	Enabled   bool
	HourLocal int // 0-23, local time of the daily run
	KeepCount int // newest archives to keep; 0 keeps all
}

// RunScheduler takes a daily backup at the configured local hour while the policy is
// enabled, then prunes to KeepCount. It returns when ctx is cancelled. A failed run is
// logged and retried the next day; the tick is one minute so a change of hour applies
// without a restart.
func RunScheduler(ctx context.Context, db *gorm.DB, dataDir, appVersion string, policy func() Policy) {
	var lastDay string
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			p := policy()
			if !p.Enabled || now.Hour() != p.HourLocal {
				continue
			}
			day := now.Format("2006-01-02")
			if day == lastDay {
				continue
			}
			lastDay = day
			info, err := CreateLocal(ctx, db, dataDir, appVersion)
			if err != nil {
				slog.Error("scheduled backup failed", "err", err)
				continue
			}
			pruned, _ := Prune(dataDir, p.KeepCount)
			slog.Info("scheduled backup written", "name", info.Name, "bytes", info.Size, "pruned", pruned)
		}
	}
}
