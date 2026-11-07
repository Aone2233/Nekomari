package filemanager

import (
	"context"
	"errors"
	"sync"
)

// uploadCleanup owns the periodic session sweeper and its best-effort remote
// cancel calls. Both can outlive the HTTP handler which created a session.
type uploadCleanup struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	drained  chan struct{}
}

var remoteUploadCleanup uploadCleanup

func (w *uploadCleanup) start(fn func(context.Context)) bool {
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
		fn(ctx)
	}()
	return true
}

func (w *uploadCleanup) stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("file upload cleanup requires a context")
	}
	w.mu.Lock()
	if !w.stopping {
		w.stopping = true
		if w.active > 0 {
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

func QuiesceRemoteUploadCleanup(ctx context.Context) error { return remoteUploadCleanup.stop(ctx) }
