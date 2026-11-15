package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/internal/config"
)

func TestReloadShutdownWaitsForAdmittedCallback(t *testing.T) {
	m := NewReloadManager()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m.Register("blocked", func(config.ConfigEvent) {
		calls.Add(1)
		close(started)
		<-release
	})
	go m.handleEvent(config.ConfigEvent{})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("reload callback did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err := m.StopAndWait(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop before callback returns = %v", err)
	}
	close(release)
	if err := m.StopAndWait(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.handleEvent(config.ConfigEvent{})
	if calls.Load() != 1 {
		t.Fatalf("callback ran after shutdown: %d", calls.Load())
	}
}
