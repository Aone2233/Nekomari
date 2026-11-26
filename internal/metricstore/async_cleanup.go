package metricstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// asyncCleanup tracks destructive work accepted by an API before the reply is
// sent. A failure is sticky: the caller has already received success, and a
// shutdown must not misrepresent an incomplete cleanup as a clean handoff.
type asyncCleanup struct {
	mu       sync.Mutex
	stopping bool
	active   int
	drained  chan struct{}
	failed   error
}

var pendingCleanup asyncCleanup

func (g *asyncCleanup) start(fn func() error) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping {
		return false
	}
	g.active++
	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("metric cleanup panic: %v", recovered)
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			if err != nil && g.failed == nil {
				g.failed = err
			}
			g.active--
			if g.stopping && g.active == 0 && g.drained != nil {
				close(g.drained)
				g.drained = nil
			}
		}()
		err = fn()
	}()
	return true
}

func (g *asyncCleanup) stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("metric cleanup shutdown requires a context")
	}
	g.mu.Lock()
	if !g.stopping {
		g.stopping = true
		if g.active != 0 {
			g.drained = make(chan struct{})
		}
	}
	drained := g.drained
	g.mu.Unlock()
	if drained != nil {
		select {
		case <-drained:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failed != nil {
		return fmt.Errorf("asynchronous metric cleanup did not complete: %w", g.failed)
	}
	return nil
}

// QuiesceAsyncCleanup follows plugin and API shutdown, but precedes closing
// the report queue or store. It is terminal for this process.
func QuiesceAsyncCleanup(ctx context.Context) error { return pendingCleanup.stop(ctx) }
