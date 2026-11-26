package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

const (
	pinnedPermitLimit = 64
	pinnedPermitTTL   = 5 * time.Second
	pinnedWriteWait   = 2 * time.Second
)

var errPinnedReservation = errors.New("pinned permit reservation unavailable")

type pinnedPermitError string

func (e pinnedPermitError) Error() string        { return errPinnedReservation.Error() }
func (e pinnedPermitError) Is(target error) bool { return target == errPinnedReservation }

type pinnedPermitEntry struct {
	sequence uint64
	expires  time.Time
	result   chan pinnedPermitOutcome
}

type pinnedPermitOutcome struct {
	permit     v2.PingFamilyPermitResult
	validUntil time.Time
	err        error
}

type pinnedPermitLease struct {
	conn       *connection.SafeConn
	instance   string
	session    string
	dispatch   string
	ticket     string
	sequence   uint64
	validUntil time.Time
}

// retirePinnedWSLocked wakes pending reservations before dropping their
// session. Callers hold the node's session guard followed by mu.
func retirePinnedWSLocked(uuid string) {
	if s, ok := pinnedWSSessions[uuid]; ok {
		for _, entry := range s.pending {
			entry.result <- pinnedPermitOutcome{err: errPinnedReservation}
		}
	}
	delete(pinnedWSSessions, uuid)
}

func prunePinnedPermitsLocked(s *pinnedWSSession, now time.Time) {
	for id, entry := range s.pending {
		if !now.Before(entry.expires) {
			delete(s.pending, id)
			entry.result <- pinnedPermitOutcome{err: errPinnedReservation}
		}
	}
}

// reservePinnedPermit is an isolated state primitive, not authorization to
// write to a socket or execute a probe. Production senders must use the
// session lock to serialize sequence reservation and the actual write.
func reservePinnedPermit(uuid string) (*connection.SafeConn, v2.Request, <-chan pinnedPermitOutcome, error) {
	return reservePinnedPermitForSession(uuid, nil, "")
}

func reservePinnedPermitForSession(uuid string, expectedConn *connection.SafeConn, expectedSession string) (*connection.SafeConn, v2.Request, <-chan pinnedPermitOutcome, error) {
	dispatchID, err := newPinnedWSChallenge()
	if err != nil {
		return nil, v2.Request{}, nil, errPinnedReservation
	}
	unlock := lockPinnedSessionLifecycle(uuid)
	defer unlock()
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	s, ok := pinnedWSSessions[uuid]
	if expectedConn != nil && (!ok || s.conn != expectedConn || s.challenge != expectedSession) {
		return nil, v2.Request{}, nil, errPinnedReservation
	}
	if !ok || s.conn == nil || connectedClients[uuid] != s.conn || !s.active || !now.Before(s.expires) {
		if ok && s.conn != nil && connectedClients[uuid] == s.conn && !now.Before(s.expires) {
			retirePinnedWSLocked(uuid)
		}
		return nil, v2.Request{}, nil, errPinnedReservation
	}
	if s.pending == nil {
		s.pending = make(map[string]pinnedPermitEntry)
	}
	prunePinnedPermitsLocked(&s, now)
	if len(s.pending) >= pinnedPermitLimit || s.sequence == ^uint64(0) {
		return nil, v2.Request{}, nil, errPinnedReservation
	}
	if _, exists := s.pending[dispatchID]; exists {
		return nil, v2.Request{}, nil, errPinnedReservation
	}
	s.sequence++
	result := make(chan pinnedPermitOutcome, 1)
	s.pending[dispatchID] = pinnedPermitEntry{sequence: s.sequence, expires: now.Add(pinnedPermitTTL), result: result}
	pinnedWSSessions[uuid] = s
	request := v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyPermit, ID: dispatchID,
		Params: v2.PingFamilyPermitParams{Instance: s.instance, Session: s.challenge, DispatchID: dispatchID, Sequence: s.sequence}}
	return s.conn, request, result, nil
}

// resolvePinnedPermit consumes only the exact pending request on the current
// connection. An RPC error completes the waiter as a failure, never a permit.
func resolvePinnedPermit(uuid string, conn *connection.SafeConn, requestID string, permit *v2.PingFamilyPermitResult) bool {
	unlock := lockPinnedSessionLifecycle(uuid)
	defer unlock()
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	s, ok := pinnedWSSessions[uuid]
	if !ok || conn == nil || connectedClients[uuid] != conn || s.conn != conn || !s.active || !now.Before(s.expires) || !validPinnedWSID(requestID) {
		if ok && conn != nil && connectedClients[uuid] == conn && s.conn == conn && !now.Before(s.expires) {
			retirePinnedWSLocked(uuid)
		}
		return false
	}
	entry, ok := s.pending[requestID]
	if !ok {
		return false
	}
	delete(s.pending, requestID)
	if !now.Before(entry.expires) || permit == nil || permit.DispatchID != requestID || permit.Sequence != entry.sequence || !validPinnedWSID(permit.FreshnessTicket) {
		entry.result <- pinnedPermitOutcome{err: errPinnedReservation}
		return false
	}
	entry.result <- pinnedPermitOutcome{permit: *permit, validUntil: entry.expires}
	return true
}

// ConsumePinnedWSPermitResponse recognizes only a pending response on this
// socket. The RPC reader must call it under current-credential admission.
// A nonempty method is never a response, even when its ID matches a permit.
func ConsumePinnedWSPermitResponse(uuid string, conn *connection.SafeConn, data []byte) bool {
	if len(data) > 4096 {
		return false
	}
	var frame struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.JSONRPC != v2.Version || frame.Method != "" ||
		(len(frame.Result) == 0 && len(frame.Error) == 0) {
		return false
	}
	var id string
	if json.Unmarshal(frame.ID, &id) != nil || !validPinnedWSID(id) {
		return false
	}
	unlock := lockPinnedSessionLifecycle(uuid)
	defer unlock()
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	s, ok := pinnedWSSessions[uuid]
	if !ok || conn == nil || connectedClients[uuid] != conn || s.conn != conn || !s.active {
		return false
	}
	entry, ok := s.pending[id]
	if !ok {
		return false
	}
	if !now.Before(s.expires) {
		retirePinnedWSLocked(uuid)
		return true
	}
	delete(s.pending, id)
	var result v2.PingFamilyPermitResult
	decoder := json.NewDecoder(bytes.NewReader(frame.Result))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&result)
	if !now.Before(entry.expires) || len(frame.Error) != 0 ||
		err != nil || decoder.Decode(new(any)) != io.EOF || result.DispatchID != id || result.Sequence != entry.sequence || !validPinnedWSID(result.FreshnessTicket) {
		entry.result <- pinnedPermitOutcome{err: errPinnedReservation}
		return true
	}
	entry.result <- pinnedPermitOutcome{permit: result, validUntil: entry.expires}
	return true
}

func cancelPinnedPermit(uuid string, conn *connection.SafeConn, requestID string) {
	mu.Lock()
	defer mu.Unlock()
	s, ok := pinnedWSSessions[uuid]
	if !ok || conn == nil || s.conn != conn || connectedClients[uuid] != conn {
		return
	}
	if entry, ok := s.pending[requestID]; ok {
		delete(s.pending, requestID)
		entry.result <- pinnedPermitOutcome{err: errPinnedReservation}
	}
}

// requestPinnedWSPermit is deliberately unexported and is not called by the
// scheduler. It never enters the generic event queue. One session write lock
// covers both sequence allocation and transmission to preserve wire order.
func requestPinnedWSPermit(ctx context.Context, uuid string) (pinnedPermitLease, error) {
	return requestPinnedWSPermitWithWriter(ctx, uuid, func(ctx context.Context, conn *connection.SafeConn, req v2.Request) error {
		return conn.WriteJSONContext(ctx, req)
	})
}

func requestPinnedWSPermitWithWriter(ctx context.Context, uuid string, write func(context.Context, *connection.SafeConn, v2.Request) error) (pinnedPermitLease, error) {
	if ctx == nil || write == nil {
		return pinnedPermitLease{}, pinnedPermitError("permit_context")
	}
	mu.RLock()
	s, ok := pinnedWSSessions[uuid]
	if !ok || s.conn == nil || connectedClients[uuid] != s.conn || !s.active || s.writeLock == nil || !time.Now().Before(s.expires) {
		mu.RUnlock()
		return pinnedPermitLease{}, pinnedPermitError("permit_session")
	}
	conn, session, lock := s.conn, s.challenge, s.writeLock
	mu.RUnlock()
	return requestPinnedWSPermitLocked(ctx, uuid, conn, session, lock, write)
}

func requestPinnedWSPermitLocked(ctx context.Context, uuid string, conn *connection.SafeConn, session string, lock chan struct{}, write func(context.Context, *connection.SafeConn, v2.Request) error) (pinnedPermitLease, error) {
	// Do not queue an unbounded number of goroutines behind a slow connection.
	select {
	case lock <- struct{}{}:
	default:
		return pinnedPermitLease{}, pinnedPermitError("permit_busy")
	}
	if ctx.Err() != nil {
		<-lock
		return pinnedPermitLease{}, pinnedPermitError("permit_context")
	}
	reservedConn, req, waiter, err := reservePinnedPermitForSession(uuid, conn, session)
	if err != nil {
		<-lock
		return pinnedPermitLease{}, pinnedPermitError("permit_reserve")
	}
	p := req.Params.(v2.PingFamilyPermitParams)
	// Preserve only the identity of an attempted reservation on failure. It
	// cannot authorize a dispatch, but lets the caller record a fenced,
	// deliberately nonspecific diagnostic for this sequence.
	attempt := pinnedPermitLease{conn: conn, instance: p.Instance, session: p.Session, sequence: p.Sequence}
	writeCtx, cancelWrite := context.WithTimeout(ctx, pinnedWriteWait)
	err = write(writeCtx, reservedConn, req)
	cancelWrite()
	<-lock
	if err != nil {
		cancelPinnedPermit(uuid, conn, p.DispatchID)
		return attempt, pinnedPermitError("permit_write")
	}
	timer := time.NewTimer(pinnedPermitTTL)
	defer timer.Stop()
	select {
	case outcome := <-waiter:
		if outcome.err != nil || ctx.Err() != nil || !PinnedWSSessionCurrent(uuid, conn, p.Instance, session, time.Now()) {
			return attempt, pinnedPermitError("permit_response")
		}
		return pinnedPermitLease{conn: conn, instance: p.Instance, session: p.Session,
			dispatch: p.DispatchID, ticket: outcome.permit.FreshnessTicket, sequence: p.Sequence,
			validUntil: outcome.validUntil}, nil
	case <-ctx.Done():
		cancelPinnedPermit(uuid, conn, p.DispatchID)
		return attempt, pinnedPermitError("permit_context")
	case <-timer.C:
		cancelPinnedPermit(uuid, conn, p.DispatchID)
		return attempt, pinnedPermitError("permit_timeout")
	}
}
