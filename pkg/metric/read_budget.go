package metric

import (
	"context"
	"errors"
	"sync/atomic"
)

type readBudgetKey struct{}

var ErrReadBudget = errors.New("metric read budget exceeded; narrow the query")

// WithReadBudget bounds materialized samples and rollup rows across all reads
// in one public request, including tag-expanded series. Internal callers can
// omit it for maintenance operations.
func WithReadBudget(ctx context.Context, limit int64) context.Context {
	remaining := &atomic.Int64{}
	remaining.Store(limit)
	return context.WithValue(ctx, readBudgetKey{}, remaining)
}

func spendReadBudget(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if remaining, ok := ctx.Value(readBudgetKey{}).(*atomic.Int64); ok && remaining.Add(-int64(n)) < 0 {
		return ErrReadBudget
	}
	return nil
}
