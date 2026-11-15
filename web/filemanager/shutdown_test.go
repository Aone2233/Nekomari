package filemanager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestUploadCleanupCancelsAndWaits(t *testing.T) {
	var w uploadCleanup
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	w.start(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
	})
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err := w.stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop while work is active = %v", err)
	}
	<-canceled
	if w.start(func(context.Context) {}) {
		t.Fatal("new work accepted after stop")
	}
	close(release)
	if err := w.stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestUploadCleanupConcurrentStartsAndStop(t *testing.T) {
	var w uploadCleanup
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			w.start(func(ctx context.Context) { <-ctx.Done() })
		}()
	}
	close(start)
	if err := w.stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	if w.start(func(context.Context) {}) {
		t.Fatal("new work accepted after concurrent stop")
	}
}
