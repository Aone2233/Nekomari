package metricstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAsyncCleanupStopWaitsAndPreservesFailure(t *testing.T) {
	var g asyncCleanup
	started, release := make(chan struct{}), make(chan struct{})
	cause := errors.New("synthetic cleanup failed")
	if !g.start(func() error { close(started); <-release; return cause }) {
		t.Fatal("cleanup not admitted")
	}
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err := g.stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop while cleanup is running = %v", err)
	}
	if g.start(func() error { return nil }) {
		t.Fatal("cleanup admitted after stopping")
	}
	close(release)
	if err := g.stop(t.Context()); !errors.Is(err, cause) {
		t.Fatalf("cleanup error lost: %v", err)
	}
}

func TestAsyncCleanupConcurrentAdmissionAndStop(t *testing.T) {
	var g asyncCleanup
	start := make(chan struct{})
	var started sync.WaitGroup
	for range 64 {
		started.Add(1)
		go func() {
			defer started.Done()
			<-start
			g.start(func() error { return nil })
		}()
	}
	close(start)
	if err := g.stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	started.Wait()
	if g.start(func() error { return nil }) {
		t.Fatal("cleanup admitted after stop")
	}
}

func TestAsyncCleanupPanicFailsShutdown(t *testing.T) {
	var g asyncCleanup
	g.start(func() error { panic("synthetic failure") })
	if err := g.stop(t.Context()); err == nil {
		t.Fatal("cleanup panic was reported as a clean shutdown")
	}
}
