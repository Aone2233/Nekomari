package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestPreparedEventWaitRejectsRetiredGenerationAndCancellation(t *testing.T) {
	isolateEventQueues(t)
	wait := PrepareV2EventWait("prepared", nil)
	DeleteV2EventQueue("prepared")
	event := EnqueueV2Event("prepared", "agent.exec", "new-generation")
	if got := wait.Wait(t.Context(), time.Minute); len(got) != 0 {
		t.Fatal("prepared old poll returned replacement work")
	}
	ctx, cancel := context.WithCancel(t.Context())
	wait = PrepareV2EventWait("prepared", nil)
	cancel()
	if got := wait.Wait(ctx, time.Minute); len(got) != 0 {
		t.Fatal("canceled poll returned events")
	}
	if got := TakeV2Events("prepared", nil, 0); len(got) != 1 || got[0].ID != event.ID {
		t.Fatal("old/canceled poll discarded replacement work")
	}
}

func TestOldDispatchCannotRecreateRetiredQueue(t *testing.T) {
	isolateEventQueues(t)
	const id = "retired-dispatch"
	MarkV2Client(id)
	mu.RLock()
	generation := v2Clients[id]
	mu.RUnlock()
	ResetClient(id)
	if enqueueCurrentV2Event(id, nil, generation, "agent.exec", "old") {
		t.Fatal("old dispatch recreated retired queue")
	}
	MarkV2Client(id)
	t.Cleanup(func() { ResetClient(id) })
	if enqueueCurrentV2Event(id, nil, generation, "agent.exec", "old") {
		t.Fatal("old dispatch injected command into new generation")
	}
	if !DispatchV2Event(id, "agent.exec", "fresh") {
		t.Fatal("fresh dispatch was refused")
	}
	if events := TakeV2Events(id, nil, 0); len(events) != 1 || events[0].Params != "fresh" {
		t.Fatal("new generation received stale work")
	}
}

func isolateEventQueues(t *testing.T) {
	t.Helper()
	v2EventMu.Lock()
	previous := v2EventQueues
	v2EventQueues = make(map[string]*v2EventQueue)
	v2EventMu.Unlock()
	t.Cleanup(func() {
		v2EventMu.Lock()
		v2EventQueues = previous
		v2EventMu.Unlock()
	})
}

func startEventWait(t *testing.T, uuid string) <-chan []v2.Event {
	t.Helper()
	done := make(chan []v2.Event, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- WaitV2Events(uuid, nil, time.Minute)
	}()
	t.Cleanup(func() {
		DeleteV2EventQueue(uuid)
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Error("event waiter did not exit")
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		v2EventMu.Lock()
		ready := v2EventQueues[uuid] != nil
		v2EventMu.Unlock()
		if ready {
			return done
		}
		if time.Now().After(deadline) {
			t.Fatal("long poll did not start")
		}
		time.Sleep(time.Millisecond)
	}
}

func readEventWait(t *testing.T, done <-chan []v2.Event) []v2.Event {
	t.Helper()
	select {
	case events := <-done:
		return events
	case <-time.After(2 * time.Second):
		t.Fatal("queue mutation did not wake long poll")
		return nil
	}
}

func TestEventQueueDeletionWakesWaiterWithoutRecreation(t *testing.T) {
	isolateEventQueues(t)
	if events := TakeV2Events("missing", nil, 0); len(events) != 0 {
		t.Fatal("missing queue returned commands")
	}
	WaitV2Events("missing", nil, 0)
	AckV2Events("missing", []string{"unknown"})
	if len(v2EventQueues) != 0 {
		t.Fatal("nonblocking read allocated fleet-lifetime state")
	}
	done := startEventWait(t, "removed")
	DeleteV2EventQueue("removed")
	DeleteV2EventQueue("removed")
	if events := readEventWait(t, done); len(events) != 0 || len(v2EventQueues) != 0 {
		t.Fatal("awakened poll recreated a deleted queue")
	}
}

func TestEventQueueOldWaitCannotReadReplacementGeneration(t *testing.T) {
	isolateEventQueues(t)
	done := startEventWait(t, "reused")
	// Retire and replace while holding the lock, so the waiter can resume only
	// after the replacement already has work.
	v2EventMu.Lock()
	deleteV2EventQueueLocked("reused", v2EventQueues["reused"])
	q := getV2EventQueueLocked("reused")
	q.events = []v2.Event{{ID: "new-generation", Params: "new command", ExpiresAt: time.Now().Add(time.Minute)}}
	v2EventMu.Unlock()
	if events := readEventWait(t, done); len(events) != 0 {
		t.Fatal("old poll consumed replacement generation commands")
	}
	if events := TakeV2Events("reused", nil, 0); len(events) != 1 || events[0].ID != "new-generation" {
		t.Fatal("replacement command was lost")
	}
}

func TestEventQueueWaitDeliveryAckAndTimeout(t *testing.T) {
	isolateEventQueues(t)
	done := startEventWait(t, "delivery")
	event := EnqueueV2Event("delivery", "agent.exec", "command")
	if events := readEventWait(t, done); len(events) != 1 || events[0].ID != event.ID {
		t.Fatal("enqueue did not deliver command")
	}
	if events := WaitV2Events("delivery", nil, time.Minute); len(events) != 1 || events[0].ID != event.ID {
		t.Fatal("unacknowledged command was not redelivered")
	}
	if events := WaitV2Events("delivery", []string{event.ID}, time.Millisecond); len(events) != 0 {
		t.Fatal("acknowledged command reappeared after timeout")
	}
}

func TestEventQueueSweepReclaimsIdleButKeepsActiveAndUnexpired(t *testing.T) {
	isolateEventQueues(t)
	for i := 0; i < 16; i++ {
		EnqueueV2Event(fmt.Sprintf("retired-%d", i), "agent.exec", "payload")
	}
	now := time.Now()
	v2EventMu.Lock()
	for _, q := range v2EventQueues {
		q.lastUsed = now.Add(-v2EventTTL)
		q.events[0].ExpiresAt = now
	}
	q := getV2EventQueueLocked("active-empty")
	q.lastUsed = now
	q = getV2EventQueueLocked("unexpired")
	q.lastUsed = now.Add(-v2EventTTL)
	q.events = []v2.Event{{ID: "keep", ExpiresAt: now.Add(time.Second)}}
	v2EventMu.Unlock()
	sweepV2EventQueues(now)
	if len(v2EventQueues) != 2 || v2EventQueues["active-empty"] == nil || v2EventQueues["unexpired"] == nil {
		t.Fatal("sweep retained retired state or evicted active/unexpired work")
	}
	done := startEventWait(t, "swept-waiter")
	v2EventMu.Lock()
	v2EventQueues["swept-waiter"].lastUsed = now.Add(-v2EventTTL)
	v2EventMu.Unlock()
	sweepV2EventQueues(now)
	if events := readEventWait(t, done); len(events) != 0 {
		t.Fatal("idle sweep returned work")
	}
	if v2EventQueues["swept-waiter"] != nil {
		t.Fatal("swept waiter recreated its queue")
	}
}

func TestEventQueueFiltersReleaseDiscardedPayloads(t *testing.T) {
	isolateEventQueues(t)
	for _, operation := range []string{"ack", "expire", "coalesce", "delete"} {
		t.Run(operation, func(t *testing.T) {
			event := EnqueueV2Event(operation, v2.MethodAgentPing, v2.PingParams{TaskID: 1})
			v2EventMu.Lock()
			q := v2EventQueues[operation]
			backing := q.events
			switch operation {
			case "ack":
				ackV2EventsLocked(q, []string{event.ID})
			case "expire":
				pruneExpiredV2EventsLocked(q, event.ExpiresAt)
			case "coalesce":
				coalesceV2EventLocked(q, event)
			case "delete":
				deleteV2EventQueueLocked(operation, q)
			}
			retained := backing[0].Params != nil || backing[0].ID != ""
			v2EventMu.Unlock()
			if retained {
				t.Fatal("discarded event payload retained in backing array")
			}
		})
	}
}

func TestEventQueueLimitTTLCoalescingAndReconnect(t *testing.T) {
	isolateEventQueues(t)
	for i := 0; i < v2EventQueueLimit+10; i++ {
		EnqueueV2Event("bounded", "agent.exec", i)
	}
	if events := TakeV2Events("bounded", nil, 0); len(events) != v2EventQueueLimit || events[0].Params != 10 {
		t.Fatal("queue no longer keeps the newest bounded commands")
	}
	v2EventMu.Lock()
	q := v2EventQueues["bounded"]
	backing := q.events[:cap(q.events)]
	tail := backing[len(q.events):]
	for _, event := range tail {
		if event.Params != nil || event.ID != "" {
			v2EventMu.Unlock()
			t.Fatal("overflow retained discarded payload references")
		}
	}
	v2EventMu.Unlock()
	for method, ttl := range map[string]time.Duration{
		v2.MethodAgentPing: v2PingEventTTL, v2.MethodAgentFile: v2FileEventTTL, "agent.exec": v2EventTTL,
	} {
		event := EnqueueV2Event("ttl", method, nil)
		if event.ExpiresAt.Sub(event.CreatedAt) != ttl {
			t.Fatalf("%s TTL changed", method)
		}
	}
	EnqueueV2Event("coalesced", v2.MethodAgentPing, v2.PingParams{TaskID: 1})
	EnqueueV2Event("coalesced", v2.MethodAgentPing, v2.PingParams{TaskID: 2})
	newest := EnqueueV2Event("coalesced", v2.MethodAgentPing, v2.PingParams{TaskID: 1})
	if events := TakeV2Events("coalesced", nil, 0); len(events) != 2 || events[1].ID != newest.ID {
		t.Fatal("ping task coalescing changed")
	}
	EnqueueV2Event("terminal", v2.MethodAgentTerminal, v2.TerminalRequestParams{RequestID: "same"})
	newest = EnqueueV2Event("terminal", v2.MethodAgentTerminal, v2.TerminalRequestParams{RequestID: "same"})
	DeleteClientConditionally("terminal", nil)
	if events := TakeV2Events("terminal", nil, 0); len(events) != 1 || events[0].ID != newest.ID {
		t.Fatal("disconnect lost valid queued command or terminal coalescing changed")
	}
}

func TestEventQueueConcurrentDispatchReadDeleteAndSweep(t *testing.T) {
	isolateEventQueues(t)
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				switch i % 6 {
				case 0:
					EnqueueV2Event("racy", v2.MethodAgentPing, v2.PingParams{TaskID: 1})
				case 1:
					TakeV2Events("racy", nil, 8)
				case 2:
					WaitV2Events("racy", nil, time.Microsecond)
				case 3:
					DeleteV2EventQueue("racy")
				case 4:
					SweepV2EventQueues()
				case 5:
					AckV2Events("racy", []string{"unknown"})
				}
			}
		}()
	}
	wg.Wait()
	DeleteV2EventQueue("racy")
}

func TestReportClampWarningsArePerEpisodeAndCleanedUp(t *testing.T) {
	const uuid = "report-clamp-warning"
	t.Cleanup(func() { DeleteLatestReport(uuid) })
	if !NoteReportClamp(uuid, "ram") || NoteReportClamp(uuid, "ram") || !NoteReportClamp(uuid, "ram,disk") {
		t.Fatal("clamp warning missing or repeats every report")
	}
	NoteReportClamp(uuid, "")
	if !NoteReportClamp(uuid, "ram") {
		t.Fatal("recovered node did not start a new anomaly episode")
	}
	DeleteLatestReport(uuid)
	if !NoteReportClamp(uuid, "ram") {
		t.Fatal("deleted node retained anomaly state")
	}
}
