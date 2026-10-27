package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

const (
	v2EventQueueLimit = 128
	v2EventTTL        = 5 * time.Minute
	v2PingEventTTL    = 3 * time.Second
	// File operations may include a remote read/search with a 90 second
	// deadline, so queued file commands need a little headroom while an agent
	// reconnects.
	v2FileEventTTL = 2 * time.Minute
)

type v2EventQueue struct {
	events   []v2.Event
	signal   chan struct{}
	lastUsed time.Time
}

var (
	v2EventMu     sync.Mutex
	v2EventQueues = make(map[string]*v2EventQueue)
)

func getV2EventQueueLocked(uuid string) *v2EventQueue {
	q := v2EventQueues[uuid]
	if q == nil {
		q = &v2EventQueue{signal: make(chan struct{})}
		v2EventQueues[uuid] = q
	}
	q.lastUsed = time.Now()
	return q
}

// DeleteV2EventQueue retires this generation and wakes its long polls. Ordinary
// disconnects must keep pending, unexpired commands for reconnect delivery.
func DeleteV2EventQueue(uuid string) {
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	if q := v2EventQueues[uuid]; q != nil {
		deleteV2EventQueueLocked(uuid, q)
	}
}

func deleteV2EventQueueLocked(uuid string, q *v2EventQueue) {
	clear(q.events)
	q.events = nil
	delete(v2EventQueues, uuid)
	close(q.signal)
}

// SweepV2EventQueues runs on the panel's existing scheduler, not a new worker.
func SweepV2EventQueues() {
	sweepV2EventQueues(time.Now())
}

func sweepV2EventQueues(now time.Time) {
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	for uuid, q := range v2EventQueues {
		pruneExpiredV2EventsLocked(q, now)
		if len(q.events) == 0 && now.Sub(q.lastUsed) >= v2EventTTL {
			deleteV2EventQueueLocked(uuid, q)
		}
	}
}

func DispatchV2Event(uuid, method string, params any) bool {
	mu.RLock()
	conn, generation := connectedClients[uuid], v2Clients[uuid]
	mu.RUnlock()
	if conn != nil {
		payload := v2.Request{JSONRPC: v2.Version, Method: method, Params: params}
		if conn.WriteJSON(payload) == nil {
			return true
		}
	}
	return enqueueCurrentV2Event(uuid, conn, generation, method, params)
}

func DispatchPing(uuid string, params v2.PingParams) bool {
	return DispatchV2Event(uuid, v2.MethodAgentPing, params)
}

func enqueueCurrentV2Event(uuid string, conn *connection.SafeConn, generation uint64, method string, params any) bool {
	// Retirement holds mu through queue deletion. A failed old socket write
	// cannot recreate its queue or inject commands into a fresh connection.
	mu.RLock()
	defer mu.RUnlock()
	if generation == 0 || v2Clients[uuid] != generation || connectedClients[uuid] != conn {
		return false
	}
	EnqueueV2Event(uuid, method, params)
	return true
}

func IsAgentOnline(uuid string) bool {
	if GetConnectedClient(uuid) != nil {
		return true
	}
	return IsV2Client(uuid)
}

func EnqueueV2Event(uuid, method string, params any) v2.Event {
	now := time.Now().UTC()
	ttl := v2EventTTL
	if method == v2.MethodAgentPing {
		ttl = v2PingEventTTL
	} else if method == v2.MethodAgentFile {
		ttl = v2FileEventTTL
	}
	event := v2.Event{
		ID:        newV2EventID(),
		Method:    method,
		Params:    params,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}

	v2EventMu.Lock()
	q := getV2EventQueueLocked(uuid)
	pruneExpiredV2EventsLocked(q, q.lastUsed)
	coalesceV2EventLocked(q, event)
	q.events = append(q.events, event)
	if len(q.events) > v2EventQueueLimit {
		remaining := copy(q.events, q.events[len(q.events)-v2EventQueueLimit:])
		clear(q.events[remaining:])
		q.events = q.events[:remaining]
	}
	close(q.signal)
	q.signal = make(chan struct{})
	v2EventMu.Unlock()

	return event
}

func newV2EventID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func coalesceV2EventLocked(q *v2EventQueue, event v2.Event) {
	key := v2EventCoalesceKey(event)
	if key == "" {
		return
	}
	filtered := q.events[:0]
	for _, existing := range q.events {
		if v2EventCoalesceKey(existing) != key {
			filtered = append(filtered, existing)
		}
	}
	clear(q.events[len(filtered):])
	q.events = filtered
}

func v2EventCoalesceKey(event v2.Event) string {
	if event.Method == v2.MethodAgentTerminal {
		var params v2.TerminalRequestParams
		if err := bindV2EventParams(event.Params, &params); err == nil && params.RequestID != "" {
			return event.Method + ":" + params.RequestID
		}
		return ""
	}
	if event.Method != v2.MethodAgentPing {
		return ""
	}
	var params v2.PingParams
	if err := bindV2EventParams(event.Params, &params); err != nil || params.TaskID == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", event.Method, params.TaskID)
}

func bindV2EventParams(raw any, target any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func ackV2EventsLocked(q *v2EventQueue, ackIDs []string) {
	if len(ackIDs) == 0 || len(q.events) == 0 {
		return
	}
	acked := make(map[string]struct{}, len(ackIDs))
	for _, id := range ackIDs {
		acked[id] = struct{}{}
	}
	filtered := q.events[:0]
	for _, event := range q.events {
		if _, ok := acked[event.ID]; !ok {
			filtered = append(filtered, event)
		}
	}
	clear(q.events[len(filtered):])
	q.events = filtered
}

func pruneExpiredV2EventsLocked(q *v2EventQueue, now time.Time) {
	if len(q.events) == 0 {
		return
	}
	filtered := q.events[:0]
	for _, event := range q.events {
		if event.ExpiresAt.IsZero() {
			filtered = append(filtered, event)
			continue
		}
		if event.ExpiresAt.After(now) {
			filtered = append(filtered, event)
		}
	}
	clear(q.events[len(filtered):])
	q.events = filtered
}

func TakeV2Events(uuid string, ackIDs []string, limit int) []v2.Event {
	v2EventMu.Lock()
	defer v2EventMu.Unlock()

	q := v2EventQueues[uuid]
	if q == nil {
		return []v2.Event{}
	}
	q.lastUsed = time.Now()
	ackV2EventsLocked(q, ackIDs)
	pruneExpiredV2EventsLocked(q, q.lastUsed)
	return takeV2EventsLocked(q, limit)
}

func AckV2Events(uuid string, ackIDs []string) {
	if len(ackIDs) == 0 {
		return
	}
	v2EventMu.Lock()
	defer v2EventMu.Unlock()

	q := v2EventQueues[uuid]
	if q == nil {
		return
	}
	q.lastUsed = time.Now()
	ackV2EventsLocked(q, ackIDs)
}

func takeV2EventsLocked(q *v2EventQueue, limit int) []v2.Event {
	if limit <= 0 || limit > len(q.events) {
		limit = len(q.events)
	}
	events := make([]v2.Event, limit)
	copy(events, q.events[:limit])
	return events
}

func WaitV2Events(uuid string, ackIDs []string, timeout time.Duration) []v2.Event {
	if timeout <= 0 {
		return TakeV2Events(uuid, ackIDs, v2EventQueueLimit)
	}
	return PrepareV2EventWait(uuid, ackIDs).Wait(context.Background(), timeout)
}

// V2EventWait captures queue identity during credential admission, but never
// holds that admission while waiting or returns another generation's work.
type V2EventWait struct {
	uuid  string
	queue *v2EventQueue
}

func PrepareV2EventWait(uuid string, ackIDs []string) V2EventWait {
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	q := getV2EventQueueLocked(uuid)
	ackV2EventsLocked(q, ackIDs)
	pruneExpiredV2EventsLocked(q, q.lastUsed)
	return V2EventWait{uuid: uuid, queue: q}
}

func (wait V2EventWait) Wait(ctx context.Context, timeout time.Duration) []v2.Event {
	v2EventMu.Lock()
	q := wait.queue
	if q == nil || v2EventQueues[wait.uuid] != q || ctx.Err() != nil {
		v2EventMu.Unlock()
		return []v2.Event{}
	}
	pruneExpiredV2EventsLocked(q, time.Now())
	if len(q.events) > 0 || timeout <= 0 {
		events := takeV2EventsLocked(q, v2EventQueueLimit)
		v2EventMu.Unlock()
		return events
	}
	signal := q.signal
	v2EventMu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
	case <-ctx.Done():
	}
	v2EventMu.Lock()
	defer v2EventMu.Unlock()
	// A delete followed by UUID reuse must not let an old poll read or revive
	// a new generation's commands, on either the signal or timeout path.
	if v2EventQueues[wait.uuid] != q || ctx.Err() != nil {
		return []v2.Event{}
	}
	q.lastUsed = time.Now()
	pruneExpiredV2EventsLocked(q, q.lastUsed)
	return takeV2EventsLocked(q, v2EventQueueLimit)
}
