package pingguard

import (
	"context"
	"errors"

	"golang.org/x/sync/semaphore"
)

// Configuration import must hold this fence while it enumerates and guards
// existing tasks. A new legacy or pinned task must not appear between that
// enumeration and the import transaction.
const importFenceWeight int64 = 1024

var importFence = semaphore.NewWeighted(importFenceWeight)

func withImportFence(ctx context.Context, weight int64, fn func() error) error {
	if ctx == nil || fn == nil {
		return errors.New("configuration fence requires context and callback")
	}
	if err := importFence.Acquire(ctx, weight); err != nil {
		return err
	}
	defer importFence.Release(weight)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

// WithConfigImport excludes task creation while checking and applying a
// configuration document. Call it before acquiring roster, node or task guards.
func WithConfigImport(ctx context.Context, fn func() error) error {
	return withImportFence(ctx, importFenceWeight, fn)
}

// WithTaskCreation excludes an import until the new task is committed.
func WithTaskCreation(ctx context.Context, fn func() error) error {
	return withImportFence(ctx, 1, fn)
}
