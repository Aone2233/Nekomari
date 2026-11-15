package agent

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

type pinnedSessionGuardEntry struct {
	gate *semaphore.Weighted
	refs int
}

var pinnedSessionGuards = struct {
	sync.Mutex
	entries map[string]*pinnedSessionGuardEntry
}{entries: make(map[string]*pinnedSessionGuardEntry)}

// lockPinnedSession orders a node's result writes against replacement,
// retirement and re-handshakes without holding the fleet-wide state mutex.
// It must not be reentered for the same node. Idle entries are removed.
func lockPinnedSession(ctx context.Context, uuid string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pinnedSessionGuards.Lock()
	e := pinnedSessionGuards.entries[uuid]
	if e == nil {
		e = &pinnedSessionGuardEntry{gate: semaphore.NewWeighted(1)}
		pinnedSessionGuards.entries[uuid] = e
	}
	e.refs++
	pinnedSessionGuards.Unlock()
	drop := func() {
		pinnedSessionGuards.Lock()
		e.refs--
		if e.refs == 0 {
			delete(pinnedSessionGuards.entries, uuid)
		}
		pinnedSessionGuards.Unlock()
	}
	if err := e.gate.Acquire(ctx, 1); err != nil {
		drop()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		e.gate.Release(1)
		drop()
		return nil, err
	}
	return func() {
		e.gate.Release(1)
		drop()
	}, nil
}

// Lifecycle methods already admitted by their caller have no context/error
// return. A blocked callback can delay this node's retirement, but not other
// nodes. The real-store path must remain nonblocking and bounded before use.
func lockPinnedSessionLifecycle(uuid string) func() {
	unlock, _ := lockPinnedSession(context.Background(), uuid)
	return unlock
}
