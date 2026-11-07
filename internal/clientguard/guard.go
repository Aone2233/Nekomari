// Package clientguard orders short agent operations and credential retirement
// per node. Idle entries are removed after their last contender leaves.
package clientguard

import (
	"context"
	"errors"
	"slices"
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

// WithMany locks nodes in a stable order. Callers must not reenter any of the
// guards and must keep network calls and long polls outside the callback.
func WithMany(ctx context.Context, uuids []string, fn func() error) error {
	if ctx == nil || fn == nil || len(uuids) == 0 {
		return errors.New("client guard requires a context, node IDs and callback")
	}
	uuids = slices.Clone(uuids)
	slices.Sort(uuids)
	uuids = slices.Compact(uuids)
	if uuids[0] == "" {
		return errors.New("client guard requires nonempty node IDs")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	registry.Lock()
	entries := make([]*entry, len(uuids))
	for i, uuid := range uuids {
		e := registry.entries[uuid]
		if e == nil {
			e = &entry{gate: semaphore.NewWeighted(1)}
			registry.entries[uuid] = e
		}
		e.refs++
		entries[i] = e
	}
	registry.Unlock()
	defer func() {
		registry.Lock()
		for i, uuid := range uuids {
			entries[i].refs--
			if entries[i].refs == 0 {
				delete(registry.entries, uuid)
			}
		}
		registry.Unlock()
	}()
	acquired := 0
	defer func() {
		for i := acquired - 1; i >= 0; i-- {
			entries[i].gate.Release(1)
		}
	}()
	for _, e := range entries {
		if err := e.gate.Acquire(ctx, 1); err != nil {
			return err
		}
		acquired++
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

// With serializes a short operation against credential retirement for one node.
func With(ctx context.Context, uuid string, fn func() error) error {
	return WithMany(ctx, []string{uuid}, fn)
}
