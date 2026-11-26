package notifier

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBackgroundWorkCancelsAndWaitsBeforeStopping(t *testing.T) {
	var work backgroundWork
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	if !work.start(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
	}) {
		t.Fatal("worker not accepted")
	}
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err := work.stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop before worker returns = %v", err)
	}
	<-canceled
	if work.start(func(context.Context) {}) {
		t.Fatal("worker accepted after stop")
	}
	close(release)
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := work.stop(ctx); err != nil {
		t.Fatalf("stop retry: %v", err)
	}
}

func TestBackgroundWorkOrdersConcurrentStartsAndStop(t *testing.T) {
	var work backgroundWork
	start := make(chan struct{})
	var launched sync.WaitGroup
	for range 64 {
		launched.Add(1)
		go func() {
			defer launched.Done()
			<-start
			work.start(func(ctx context.Context) { <-ctx.Done() })
		}()
	}
	close(start)
	if err := work.stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	launched.Wait()
	if work.start(func(context.Context) {}) {
		t.Fatal("worker accepted after concurrent stop")
	}
}
