package metricstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/Aone2233/nekomari/pkg/metric"
	"golang.org/x/sync/semaphore"
)

// storeOperationGate keeps an active Store stable while allowing regular
// reads, report writes, and compaction to proceed together. Store replacement
// and maintenance take the whole gate exclusively.
type storeOperationGate struct {
	semaphore *semaphore.Weighted
}

const storeOperationGateWeight int64 = 1024

// pingIdentityOperations serializes task deletion/cleanup against store
// transitions. It is separate from storeOperations so the batch writer can
// drain accepted records before a transition takes the store exclusively.
var pingIdentityOperations = newStoreOperationGate()

// Accepted writes must not enter a batch queue between a transition's drain
// and its store lease. Workers do not take this gate, so a drain can progress.
var reportAdmissions = newStoreOperationGate()

// WithPingIdentityOperation protects a deletion from an overlapping store
// transition. The caller acquires task guards afterwards, never in reverse.
func WithPingIdentityOperation(ctx context.Context, fn func() error) error {
	if ctx == nil || fn == nil {
		return errors.New("ping identity operation requires a context and callback")
	}
	if err := pingIdentityOperations.AcquireShared(ctx); err != nil {
		return err
	}
	defer pingIdentityOperations.ReleaseShared()
	return fn()
}

// acquirePingHistoryTransition must not be called while holding the store
// gate: flushing the report queue itself needs a shared store lease.
func acquirePingHistoryTransition(ctx context.Context, operation string) error {
	if err := pingIdentityOperations.Acquire(ctx); err != nil {
		return fmt.Errorf("wait for ping deletions before %s: %w", operation, err)
	}
	if err := reportAdmissions.Acquire(ctx); err != nil {
		pingIdentityOperations.Release()
		return fmt.Errorf("wait for report admission before %s: %w", operation, err)
	}
	if err := FlushReportBatch(ctx); err != nil {
		reportAdmissions.Release()
		pingIdentityOperations.Release()
		return fmt.Errorf("flush reports before %s: %w", operation, err)
	}
	if err := storeOperations.Acquire(ctx); err != nil {
		reportAdmissions.Release()
		pingIdentityOperations.Release()
		return fmt.Errorf("wait for metric store operations before %s: %w", operation, err)
	}
	return nil
}

func releasePingHistoryTransition() {
	storeOperations.Release()
	reportAdmissions.Release()
	pingIdentityOperations.Release()
}

// WithSnapshotStore drains reports accepted before this operation and keeps
// the active store stable until fn returns. New reports are refused while the
// fence is held. This only coordinates this panel process: it does not create
// an atomic snapshot with the main database or fence external writers.
func WithSnapshotStore(ctx context.Context, fn func(*metric.Store) error) error {
	if ctx == nil || fn == nil {
		return errors.New("metric snapshot requires a context and callback")
	}
	if err := acquirePingHistoryTransition(ctx, "snapshot"); err != nil {
		return err
	}
	defer releasePingHistoryTransition()
	return fn(GetStore())
}

func newStoreOperationGate() *storeOperationGate {
	return &storeOperationGate{semaphore: semaphore.NewWeighted(storeOperationGateWeight)}
}

func (g *storeOperationGate) Acquire(ctx context.Context) error {
	return g.semaphore.Acquire(ctx, storeOperationGateWeight)
}

func (g *storeOperationGate) TryAcquire() bool {
	return g.semaphore.TryAcquire(storeOperationGateWeight)
}

func (g *storeOperationGate) Release() {
	g.semaphore.Release(storeOperationGateWeight)
}

func (g *storeOperationGate) AcquireShared(ctx context.Context) error {
	return g.semaphore.Acquire(ctx, 1)
}

func (g *storeOperationGate) TryAcquireShared() bool {
	return g.semaphore.TryAcquire(1)
}

func (g *storeOperationGate) ReleaseShared() {
	g.semaphore.Release(1)
}
