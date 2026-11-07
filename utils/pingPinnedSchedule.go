package utils

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	logger "github.com/Aone2233/nekomari/utils/log"
)

const (
	pinnedScheduleParallelism = 64
	pinnedScheduleTimeout     = 8 * time.Second
)

type pinnedScheduleKey struct {
	task uint
	uuid string
}

// A busy or slow node must not hold up the rest of a task or spawn an
// unbounded delivery backlog. This bounds concurrent deliveries, not the
// Agent probes that may continue after a successful socket write. The delivery
// helper independently rechecks task, assignment, credentials and socket.
type pinnedPingExecutor struct {
	dispatch func(context.Context, string, models.PingTask) error
	slots    chan struct{}
	mu       sync.Mutex
	inflight map[pinnedScheduleKey]struct{}
}

func newPinnedPingExecutor(dispatch func(context.Context, string, models.PingTask) error, parallelism int) *pinnedPingExecutor {
	return &pinnedPingExecutor{dispatch: dispatch, slots: make(chan struct{}, parallelism), inflight: make(map[pinnedScheduleKey]struct{})}
}

func (e *pinnedPingExecutor) dispatchTask(ctx context.Context, task models.PingTask, rotation *atomic.Uint64) {
	e.dispatchTaskTracked(ctx, task, rotation, nil)
}

func (e *pinnedPingExecutor) dispatchTaskTracked(ctx context.Context, task models.PingTask, rotation *atomic.Uint64, work *sync.WaitGroup) {
	if e == nil || e.dispatch == nil || ctx == nil || ctx.Err() != nil || task.Id == 0 || len(task.Clients) == 0 || rotation == nil {
		return
	}
	// Each scheduled task owns its own counter. Advancing by one also rotates
	// tasks with exactly as many nodes as there are shared worker slots.
	start := int((rotation.Add(1) - 1) % uint64(len(task.Clients)))
	seen := make(map[string]struct{}, len(task.Clients))
	for i := 0; i < len(task.Clients); i++ {
		if ctx.Err() != nil {
			return
		}
		uuid := task.Clients[(start+i)%len(task.Clients)]
		if uuid == "" {
			continue
		}
		if _, duplicate := seen[uuid]; duplicate {
			continue
		}
		seen[uuid] = struct{}{}
		key := pinnedScheduleKey{task: task.Id, uuid: uuid}
		e.mu.Lock()
		if _, busy := e.inflight[key]; busy {
			e.mu.Unlock()
			continue
		}
		select {
		case e.slots <- struct{}{}:
			e.inflight[key] = struct{}{}
		default:
			e.mu.Unlock()
			continue
		}
		e.mu.Unlock()
		if work != nil {
			work.Add(1)
		}
		go func() {
			if work != nil {
				defer work.Done()
			}
			defer func() {
				e.mu.Lock()
				delete(e.inflight, key)
				<-e.slots
				e.mu.Unlock()
			}()
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Errorf("pingschedule", "pinned dispatch panic for task %d: %v", task.Id, recovered)
				}
			}()
			workerCtx, cancel := context.WithTimeout(ctx, pinnedScheduleTimeout)
			defer cancel()
			if workerCtx.Err() == nil {
				_ = e.dispatch(workerCtx, uuid, task)
			}
		}()
	}
}
