package metricstore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func TestPinnedAppendRetriesTransientContention(t *testing.T) {
	attempts := 0
	err := retryPinnedAppend(t.Context(), func() error {
		attempts++
		if attempts < 3 {
			return metric.ErrWriteUnavailable
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("retry returned %v after %d attempts", err, attempts)
	}
}

func TestPinnedAppendRetryStopsOnPermanentError(t *testing.T) {
	want := errors.New("permanent error")
	attempts := 0
	err := retryPinnedAppend(t.Context(), func() error { attempts++; return want })
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("retry returned %v after %d attempts", err, attempts)
	}
}

func TestPinnedAppendRetryIsBoundedAndNeverContinuesAfterReturn(t *testing.T) {
	var attempts atomic.Int64
	done := make(chan error, 1)
	go func() {
		done <- retryPinnedAppend(t.Context(), func() error {
			attempts.Add(1)
			return metric.ErrWriteUnavailable
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, metric.ErrWriteUnavailable) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry exceeded its bounded contention window")
	}
	count := attempts.Load()
	if count < 2 {
		t.Fatalf("expected more than one attempt, got %d", count)
	}
	time.Sleep(2 * pinnedAppendRetryDelay)
	if attempts.Load() != count {
		t.Fatal("an append continued after the identity fence was released")
	}
}

func TestPinnedAppendRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempted := make(chan struct{})
	var attempts atomic.Int64
	done := make(chan error, 1)
	go func() {
		done <- retryPinnedAppend(ctx, func() error {
			if attempts.Add(1) == 1 {
				close(attempted)
			}
			return metric.ErrWriteUnavailable
		})
	}()
	<-attempted
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled retry returned %v", err)
	}
	count := attempts.Load()
	time.Sleep(2 * pinnedAppendRetryDelay)
	if attempts.Load() != count {
		t.Fatal("canceled retry continued after returning")
	}
}
