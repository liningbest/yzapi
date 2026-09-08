package logstore

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// BatchWriter persists arbitrary rows asynchronously with a bounded queue, batching
// and bounded retries. It replaces ad-hoc "go db.Create(...)" goroutines so slow
// databases cannot make goroutine counts grow without limit. It is meant for
// diagnostic rows (audit / decision logs), not for metering.
type BatchWriter[T any] struct {
	db      *gorm.DB
	name    string
	ch      chan *T
	stop    chan struct{}
	wg      sync.WaitGroup
	dropped atomic.Int64
}

func NewBatchWriter[T any](db *gorm.DB, name string, capacity int) *BatchWriter[T] {
	w := &BatchWriter[T]{db: db, name: name, ch: make(chan *T, capacity), stop: make(chan struct{})}
	w.wg.Add(1)
	go w.loop()
	return w
}

func (w *BatchWriter[T]) Record(row *T) {
	select {
	case w.ch <- row:
	default:
		if n := w.dropped.Add(1); n == 1 || n%1000 == 0 {
			slog.Warn("async writer queue full, dropping rows", "writer", w.name, "dropped_total", n)
		}
	}
}

func (w *BatchWriter[T]) Dropped() int64 { return w.dropped.Load() }

func (w *BatchWriter[T]) loop() {
	defer w.wg.Done()
	batch := make([]*T, 0, 128)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			if err = w.db.CreateInBatches(batch, 128).Error; err == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
		if err != nil {
			w.dropped.Add(int64(len(batch)))
			slog.Error("async writer batch discarded", "writer", w.name, "err", err, "n", len(batch))
		}
		batch = batch[:0]
	}
	for {
		select {
		case r := <-w.ch:
			batch = append(batch, r)
			if len(batch) >= 128 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-w.stop:
			for {
				select {
				case r := <-w.ch:
					batch = append(batch, r)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (w *BatchWriter[T]) Close(ctx context.Context) {
	close(w.stop)
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
