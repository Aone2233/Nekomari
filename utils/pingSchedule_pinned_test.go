package utils

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/scheduler"
	agent "github.com/Aone2233/nekomari/web/agent"
)

type intervalRecordingScheduler struct{ names []string }

func (s *intervalRecordingScheduler) AddContextFunc(name, _ string, _ bool, _ scheduler.Func) error {
	s.names = append(s.names, name)
	return nil
}

func (*intervalRecordingScheduler) RemovePrefix(string) {}

func TestPingScheduleSkipsOverflowingIntervalsWithoutLosingValidTasks(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("time.Duration seconds cannot overflow an int on this platform")
	}
	s := &intervalRecordingScheduler{}
	m := newPingTaskManager(s, nil)
	maxSeconds := int64(math.MaxInt64 / int64(time.Second))
	if err := m.Reload([]models.PingTask{
		{Id: 1, Interval: 60},
		{Id: 2, Interval: int(maxSeconds + 1)},
		{Id: 3, Interval: int(maxSeconds*2 + 2)},
	}); err != nil {
		t.Fatal(err)
	}
	if len(s.names) != 1 || s.names[0] != "ping:60" {
		t.Fatalf("registered schedules = %v", s.names)
	}
}

func TestIncompletePinnedScheduleNeverUsesLegacyDelivery(t *testing.T) {
	const uuid = "test-pinned-no-legacy-delivery"
	agent.MarkV2Client(uuid)
	t.Cleanup(func() { agent.ResetClient(uuid) })
	manager := newPingTaskManager(defaultPingScheduler{}, nil)
	for _, family := range []string{"ipv4", "ipv6", "invalid"} {
		manager.executePingTask(context.Background(), models.PingTask{Id: 7, Type: "tcp", Target: "host.example:80", TargetFamily: family, Clients: models.StringArray{uuid}}, nil, new(atomic.Uint64))
		if events := agent.TakeV2Events(uuid, nil, 0); len(events) != 0 {
			t.Fatalf("family %q reached legacy event queue: %+v", family, events)
		}
	}
}

func TestPinnedScheduleBoundsAndRotatesWorkers(t *testing.T) {
	started := make(chan string, 8)
	release := make(chan struct{})
	var workers sync.WaitGroup
	executor := newPinnedPingExecutor(func(ctx context.Context, uuid string, _ models.PingTask) error {
		defer workers.Done()
		started <- uuid
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}, 2)
	task := models.PingTask{Id: 7, TargetFamily: "ipv6", Clients: models.StringArray{"one", "two", "three", "four", "five"}}
	var rotation atomic.Uint64
	workers.Add(2)
	executor.dispatchTask(t.Context(), task, &rotation)
	got := map[string]bool{<-started: true, <-started: true}
	if !got["one"] || !got["two"] {
		t.Fatalf("first cycle = %v", got)
	}
	executor.dispatchTask(t.Context(), task, &rotation)
	select {
	case uuid := <-started:
		t.Fatalf("unbounded or duplicate worker for %s", uuid)
	default:
	}
	close(release)
	workers.Wait()
	deadline := time.Now().Add(time.Second)
	for {
		executor.mu.Lock()
		remaining := len(executor.inflight)
		executor.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workers did not retire")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPinnedScheduleDeduplicatesEvenWhenDispatchCompletesImmediately(t *testing.T) {
	var calls atomic.Int32
	executor := newPinnedPingExecutor(func(context.Context, string, models.PingTask) error {
		calls.Add(1)
		return nil
	}, 64)
	clients := make(models.StringArray, 1024)
	for i := range clients {
		clients[i] = "same-node"
	}
	task := models.PingTask{Id: 7, TargetFamily: "ipv6", Clients: clients}
	var rotation atomic.Uint64
	executor.dispatchTask(t.Context(), task, &rotation)
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("duplicate node dispatched %d times", got)
	}
}

func TestPinnedScheduleRotatesSaturatedPrefix(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	executor := newPinnedPingExecutor(func(_ context.Context, uuid string, _ models.PingTask) error {
		started <- uuid
		<-release
		return nil
	}, 2)
	for range 2 {
		executor.slots <- struct{}{}
	}
	task := models.PingTask{Id: 8, TargetFamily: "ipv6", Clients: models.StringArray{"one", "two", "three", "four", "five"}}
	var rotation atomic.Uint64
	executor.dispatchTask(t.Context(), task, &rotation)
	<-executor.slots
	<-executor.slots
	executor.dispatchTask(t.Context(), task, &rotation)
	got := map[string]bool{<-started: true, <-started: true}
	if !got["two"] || !got["three"] {
		t.Fatalf("rotated cycle = %v", got)
	}
	close(release)
}

func TestPinnedScheduleTaskRotationsAreIndependent(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	executor := newPinnedPingExecutor(func(_ context.Context, uuid string, _ models.PingTask) error {
		started <- uuid
		<-release
		return nil
	}, 2)
	for range 2 {
		executor.slots <- struct{}{}
	}
	first := models.PingTask{Id: 1, Clients: models.StringArray{"one", "two"}}
	second := models.PingTask{Id: 2, Clients: models.StringArray{"other-one", "other-two"}}
	var firstRotation, secondRotation atomic.Uint64
	executor.dispatchTask(t.Context(), first, &firstRotation)
	executor.dispatchTask(t.Context(), second, &secondRotation)
	<-executor.slots
	executor.dispatchTask(t.Context(), first, &firstRotation)
	if got := <-started; got != "two" || firstRotation.Load() != 2 || secondRotation.Load() != 1 {
		t.Fatalf("task rotation was shared: node=%s first=%d second=%d", got, firstRotation.Load(), secondRotation.Load())
	}
	close(release)
}

func TestPinnedLiteralDoesNotReportLegacyFamilySkips(t *testing.T) {
	pinned := models.PingTask{Id: 1, Target: "[2001:db8::1]:80", TargetFamily: "ipv6", Clients: models.StringArray{"v4"}}
	legacy := models.PingTask{Id: 2, Target: "[2001:db8::1]:80", Clients: models.StringArray{"v4"}}
	lookup := newClientAddressLookup()
	lookup.once.Do(func() { lookup.byUUID = map[string]models.Client{"v4": {UUID: "v4", IPv4: "192.0.2.1"}} })
	skips, err := pingTasksFamilySkips([]models.PingTask{pinned, legacy}, lookup)
	if err != nil || len(skips[pinned.Id]) != 0 || len(skips[legacy.Id]) != 1 || skips[legacy.Id][0] != "v4" {
		t.Fatalf("family skips = %v, err = %v", skips, err)
	}
	failedLookup := newClientAddressLookup()
	failedLookup.once.Do(func() { failedLookup.err = errors.New("must not query node addresses") })
	skips, err = pingTasksFamilySkips([]models.PingTask{pinned}, failedLookup)
	if err != nil || len(skips) != 0 {
		t.Fatalf("pinned-only lookup = %v, err = %v", skips, err)
	}
}

func TestPinnedScheduleReloadCancelsWork(t *testing.T) {
	s := scheduler.NewManager()
	started, canceled := make(chan struct{}, 1), make(chan struct{}, 1)
	manager := NewPingTaskManagerWithScheduler(s, func(ctx context.Context, _ string, _ models.PingTask) error {
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return ctx.Err()
	})
	defer manager.Stop()
	task := models.PingTask{Id: 7, TargetFamily: "ipv6", Interval: 1, Clients: models.StringArray{"node"}}
	if err := manager.Reload([]models.PingTask{task}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("scheduled pinned worker did not start")
	}
	if err := manager.Reload(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("reload did not cancel in-flight dispatch")
	}
}

func TestPingSchedulerShutdownWaitsForPinnedDispatch(t *testing.T) {
	s := scheduler.NewManager()
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	manager := NewPingTaskManagerWithScheduler(s, func(ctx context.Context, _ string, _ models.PingTask) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release // A dispatch may take time to finish even after cancellation.
		return nil
	})
	if err := manager.Reload([]models.PingTask{{Id: 7, TargetFamily: "ipv6", Interval: 1, Clients: models.StringArray{"node"}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("scheduled pinned worker did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	err := s.StopAndWait(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown while dispatch is running = %v", err)
	}
	<-canceled
	close(release)
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := s.StopAndWait(ctx); err != nil {
		t.Fatalf("retry after dispatch returned: %v", err)
	}
}

func TestPinnedScheduleNeverUsesLegacyQueueWhenEnabled(t *testing.T) {
	const uuid = "test-pinned-schedule-enabled"
	agent.MarkV2Client(uuid)
	t.Cleanup(func() { agent.ResetClient(uuid) })
	started := make(chan string, 1)
	m := newPingTaskManager(defaultPingScheduler{}, func(_ context.Context, got string, task models.PingTask) error {
		started <- fmt.Sprintf("%s/%s", got, task.TargetFamily)
		return nil
	})
	task := models.PingTask{Id: 7, TargetFamily: "ipv6", Clients: models.StringArray{uuid}}
	m.executePingTask(t.Context(), task, nil, new(atomic.Uint64))
	select {
	case got := <-started:
		if got != uuid+"/ipv6" {
			t.Fatalf("dispatch = %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("pinned dispatch not called")
	}
	if events := agent.TakeV2Events(uuid, nil, 0); len(events) != 0 {
		t.Fatalf("pinned task reached legacy queue: %+v", events)
	}
}
