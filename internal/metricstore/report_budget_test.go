package metricstore

import (
	"context"
	"errors"
	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"testing"
	"time"
)

func TestFailedFlushRetainsBoundedReportsAndRecovers(t *testing.T) {
	useReportTestStore(t, nil)
	storeMu.Lock()
	healthy := store
	store = nil
	storeMu.Unlock()
	defer func() { storeMu.Lock(); store = healthy; storeMu.Unlock() }()
	w := &reportBatchWorker{queue: make(chan v2.Report, reportBatchQueueSize), pingQueue: make(chan models.PingRecord, reportBatchQueueSize), requests: make(chan reportBatchRequest), done: make(chan struct{})}
	go w.run()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flush := func(stop bool) error {
		done := make(chan error, 1)
		w.requests <- reportBatchRequest{ctx: ctx, done: done, stop: stop}
		return <-done
	}
	defer func() { _ = flush(true); <-w.done }()
	report := v2.Report{UUID: "bounded-worker", UpdatedAt: time.Now().UTC()}
	for i := 0; i < reportBatchQueueSize; i++ {
		if err := w.enqueue(ctx, report); err != nil {
			t.Fatal(err)
		}
	}
	for cycle := 0; cycle < 5; cycle++ {
		if err := flush(false); err == nil {
			t.Fatal("expected unavailable store")
		}
		if err := w.enqueue(ctx, report); !errors.Is(err, ErrReportBatchQueueFull) {
			t.Fatalf("cycle %d admitted extra report: %v", cycle, err)
		}
		w.mu.Lock()
		count := w.reportCount
		w.mu.Unlock()
		if count != reportBatchQueueSize {
			t.Fatalf("retained %d", count)
		}
	}
	storeMu.Lock()
	store = healthy
	storeMu.Unlock()
	if err := flush(false); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	count := w.reportCount
	w.mu.Unlock()
	if count != 0 {
		t.Fatalf("recovered queue count %d", count)
	}
	if err := w.enqueue(ctx, report); err != nil {
		t.Fatal(err)
	}
}
