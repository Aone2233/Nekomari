package notifier

import (
	"context"
	"errors"
	"sync"

	logger "github.com/Aone2233/nekomari/utils/log"
)

// backgroundWork owns notifier work that can outlive an HTTP handler or a
// scheduler callback. Cancellation ends grace timers; already-running calls
// must return before the provider or databases are closed.
type backgroundWork struct {
	mu       sync.Mutex
	stopping bool
	ctx      context.Context
	cancel   context.CancelFunc
	active   int
	drained  chan struct{}
}

var notifierWork backgroundWork

func (w *backgroundWork) start(fn func(context.Context)) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopping {
		return false
	}
	if w.ctx == nil {
		w.ctx, w.cancel = context.WithCancel(context.Background())
	}
	ctx := w.ctx
	w.active++
	go func() {
		defer func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.active--
			if w.stopping && w.active == 0 && w.drained != nil {
				close(w.drained)
				w.drained = nil
			}
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Errorf("notifier", "background notification work panicked: %v", recovered)
			}
		}()
		fn(ctx)
	}()
	return true
}

func (w *backgroundWork) stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("notifier shutdown requires a context")
	}
	w.mu.Lock()
	if !w.stopping {
		w.stopping = true
		if w.active != 0 {
			w.drained = make(chan struct{})
		}
		if w.cancel != nil {
			w.cancel()
		}
	}
	drained := w.drained
	w.mu.Unlock()
	if drained == nil {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StartBackground admits work before the caller returns, so a delayed
// goroutine cannot enter after quiescence has observed an empty worker set.
func StartBackground(fn func(context.Context)) bool { return notifierWork.start(fn) }

func QuiesceBackground(ctx context.Context) error { return notifierWork.stop(ctx) }
