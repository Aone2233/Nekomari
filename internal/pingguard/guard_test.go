package pingguard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithSerializesSameTaskWithoutBlockingOtherTasks(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- With(t.Context(), 1, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	defer func() { close(release); <-done }()
	if err := With(t.Context(), 2, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := With(ctx, 1, func() error { t.Error("contender entered held gate"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held gate error = %v", err)
	}
}

func TestWithManyOrdersAndReleasesEntries(t *testing.T) {
	var active atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		ids := []uint{2, 1, 2}
		if i%2 == 0 {
			ids = []uint{1, 2}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WithMany(t.Context(), ids, func() error {
				if active.Add(1) != 1 {
					t.Error("overlapping task sets entered together")
				}
				active.Add(-1)
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	registry.Lock()
	defer registry.Unlock()
	if len(registry.entries) != 0 {
		t.Fatalf("idle entries retained: %d", len(registry.entries))
	}
}

func TestWithManyRejectsInvalidAndCanceled(t *testing.T) {
	for _, ids := range [][]uint{nil, {0}, {2, 0, 1}} {
		if err := WithMany(t.Context(), ids, func() error { t.Error("invalid guard ran"); return nil }); err == nil {
			t.Fatalf("accepted IDs %v", ids)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WithMany(ctx, []uint{1, 2}, func() error { t.Error("canceled guard ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled guard = %v", err)
	}
}
