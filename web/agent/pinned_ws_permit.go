package agent

import (
	"errors"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

const (
	pinnedPermitLimit = 64
	pinnedPermitTTL   = 5 * time.Second
)

var errPinnedReservation = errors.New("pinned permit reservation unavailable")

type pinnedPermitEntry struct {
	sequence uint64
	expires  time.Time
	result   chan pinnedPermitOutcome
}

type pinnedPermitOutcome struct {
	permit v2.PingFamilyPermitResult
	err    error
}

// retirePinnedWSLocked wakes pending reservations before dropping their
// session. Callers hold mu, the same lock used for connection replacement.
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
// write to a socket or execute a probe. Sequence reservation and actual
// WebSocket writes must be serialized together by the future transport.
func reservePinnedPermit(uuid string) (*connection.SafeConn, v2.Request, <-chan pinnedPermitOutcome, error) {
	dispatchID, err := newPinnedWSChallenge()
	if err != nil {
		return nil, v2.Request{}, nil, errPinnedReservation
	}
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	s, ok := pinnedWSSessions[uuid]
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
	entry.result <- pinnedPermitOutcome{permit: *permit}
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
