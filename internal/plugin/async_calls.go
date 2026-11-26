package plugin

import (
	"context"
	"errors"
	"sync"
)

// asyncCalls tracks host RPC and configuration goroutines even when their
// plugin has unloaded. A handler can ignore cancellation, so shutdown waits.
type asyncCalls struct {
	mu       sync.Mutex
	stopping bool
	cancels  map[*asyncCall]struct{}
	drained  chan struct{}
}

type asyncCall struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (c *asyncCalls) start() (context.Context, func(), bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopping {
		return nil, nil, false
	}
	if c.cancels == nil {
		c.cancels = make(map[*asyncCall]struct{})
	}
	ctx, cancel := context.WithCancel(context.Background())
	call := &asyncCall{cancel: cancel}
	c.cancels[call] = struct{}{}
	return ctx, func() {
		call.once.Do(func() {
			cancel()
			c.mu.Lock()
			delete(c.cancels, call)
			if c.stopping && len(c.cancels) == 0 && c.drained != nil {
				close(c.drained)
				c.drained = nil
			}
			c.mu.Unlock()
		})
	}, true
}

func (c *asyncCalls) stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("plugin async shutdown requires a context")
	}
	c.mu.Lock()
	if !c.stopping {
		c.stopping = true
		if len(c.cancels) != 0 {
			c.drained = make(chan struct{})
			for call := range c.cancels {
				call.cancel()
			}
		}
	}
	drained := c.drained
	c.mu.Unlock()
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

// QuiesceAsyncCalls is terminal for the process-wide manager and must follow
// plugin unload. It does not fence arbitrary plugin or host work.
func QuiesceAsyncCalls(ctx context.Context) error {
	return global.async.stop(ctx)
}
