package pingguard

import (
	"context"
	"errors"

	"golang.org/x/sync/semaphore"
)

var defaultRoster = semaphore.NewWeighted(1)

// WithDefaultRoster orders node creation against task creation and edits that
// change default-on assignments. Acquire it before node and task guards.
func WithDefaultRoster(ctx context.Context, fn func() error) error {
	if ctx == nil || fn == nil {
		return errors.New("default-on roster requires a context and callback")
	}
	if err := defaultRoster.Acquire(ctx, 1); err != nil {
		return err
	}
	defer defaultRoster.Release(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
