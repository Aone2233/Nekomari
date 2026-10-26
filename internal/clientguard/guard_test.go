package clientguard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithSerializesOneNodeWithoutBlockingOthers(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- With(t.Context(), "a", func() error { close(entered); <-release; return nil })
	}()
	<-entered
	defer func() { close(release); <-done }()
	if err := With(t.Context(), "b", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := With(ctx, "a", func() error { t.Error("contender entered a held gate"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held gate error = %v", err)
	}
}

func TestWithReleasesContendersAndIdleEntries(t *testing.T) {
	var active atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if err := With(t.Context(), "cleanup", func() error {
				if active.Add(1) != 1 {
					t.Error("same-node operations overlapped")
				}
				active.Add(-1)
				return errors.New("expected callback failure")
			}); err == nil {
				t.Error("callback error was lost")
			}
		})
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := With(ctx, "cleanup", func() error { t.Error("canceled callback ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled gate = %v", err)
	}
	registry.Lock()
	defer registry.Unlock()
	if len(registry.entries) != 0 {
		t.Fatalf("idle entries retained = %d", len(registry.entries))
	}
}
