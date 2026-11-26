// Package pingguard orders task configuration mutations and result admission.
package pingguard

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
	entries map[uint]*entry
}{entries: make(map[uint]*entry)}

// WithMany acquires distinct task IDs in sorted order to avoid deadlocks.
// It must not be reentered for the same task; callbacks should be bounded.
// Idle entries are removed after the last holder or waiter exits.
func WithMany(ctx context.Context, ids []uint, fn func() error) error {
	if ctx == nil || fn == nil || len(ids) == 0 {
		return errors.New("ping task guard requires a context, task IDs and callback")
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if ids[0] == 0 {
		return errors.New("ping task ID must be nonzero")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	registry.Lock()
	entries := make([]*entry, len(ids))
	for i, id := range ids {
		e := registry.entries[id]
		if e == nil {
			e = &entry{gate: semaphore.NewWeighted(1)}
			registry.entries[id] = e
		}
		e.refs++
		entries[i] = e
	}
	registry.Unlock()
	defer func() {
		registry.Lock()
		for i, id := range ids {
			entries[i].refs--
			if entries[i].refs == 0 {
				delete(registry.entries, id)
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

func With(ctx context.Context, id uint, fn func() error) error {
	return WithMany(ctx, []uint{id}, fn)
}
