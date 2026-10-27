// Package clientguard orders short agent operations and credential retirement
// per node. Idle entries are removed after their last contender leaves.
package clientguard

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

type entry struct {
	gate *semaphore.Weighted
	refs int
}

var registry = struct {
	sync.Mutex
	entries map[string]*entry
}{entries: make(map[string]*entry)}

// With must not be reentered for the same node. Network calls, streams and
// long polls belong outside fn; only bounded local/DB operations belong here.
func With(ctx context.Context, uuid string, fn func() error) error {
	registry.Lock()
	e := registry.entries[uuid]
	if e == nil {
		e = &entry{gate: semaphore.NewWeighted(1)}
		registry.entries[uuid] = e
	}
	e.refs++
	registry.Unlock()
	defer func() {
		registry.Lock()
		e.refs--
		if e.refs == 0 {
			delete(registry.entries, uuid)
		}
		registry.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.gate.Acquire(ctx, 1); err != nil {
		return err
	}
	defer e.gate.Release(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
