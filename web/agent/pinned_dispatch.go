package agent

import (
	"errors"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

// This is an isolated state primitive, not a production dispatch or metric
// admission path. Callers must additionally fence task mutation, assignment,
// credential retirement and the actual metric write before integrating it.
const (
	pinnedDispatchLimit = 64
	pinnedDispatchTTL   = 35 * time.Second
)

const (
	pinnedPrimaryICMP = 1 << iota
	pinnedPrimaryTCP
)

var errPinnedDispatch = errors.New("pinned dispatch unavailable or stale")

type pinnedDispatchSpec struct {
	taskID           uint
	configGeneration string
	family           string
	taskType         string
	referenceEnabled bool
}

type pinnedDispatchEntry struct {
	spec      pinnedDispatchSpec
	instance  string
	session   string
	at        time.Time
	expires   time.Time
	primary   uint8
	reference bool
}

// pinnedAcceptedResult carries the panel's registration time, not an Agent
// clock. It does not itself authorize writing to the metric store.
type pinnedAcceptedResult struct {
	at     time.Time
	result v2.PingFamilyResultParams
}

func validPinnedDispatchSpec(spec pinnedDispatchSpec) bool {
	if spec.taskID == 0 || !validPinnedWSID(spec.configGeneration) || (spec.family != "ipv4" && spec.family != "ipv6") {
		return false
	}
	switch spec.taskType {
	case "icmp", "tcp", "http", "auto", "dual":
		return true
	default:
		return false
	}
}

func prunePinnedDispatchesLocked(s *pinnedWSSession, now time.Time) {
	for id, dispatch := range s.dispatches {
		if !now.Before(dispatch.expires) {
			delete(s.dispatches, id)
		}
	}
}

// registerPinnedDispatchAt binds a successful permit to the current socket.
// It never evicts an unexpired registration to make room for a new one.
func registerPinnedDispatchAt(uuid string, lease pinnedPermitLease, spec pinnedDispatchSpec, clock func() time.Time) error {
	if clock == nil || !validPinnedDispatchSpec(spec) || !validPinnedWSID(lease.instance) ||
		!validPinnedWSID(lease.session) || !validPinnedWSID(lease.dispatch) || !validPinnedWSID(lease.ticket) || lease.conn == nil {
		return errPinnedDispatch
	}
	mu.Lock()
	defer mu.Unlock()
	now := clock()
	s, ok := pinnedWSSessions[uuid]
	if !ok || connectedClients[uuid] != lease.conn || s.conn != lease.conn || !s.active ||
		s.instance != lease.instance || s.challenge != lease.session || !now.Before(s.expires) ||
		lease.validUntil.IsZero() || !now.Before(lease.validUntil) || lease.validUntil.After(now.Add(pinnedPermitTTL)) {
		return errPinnedDispatch
	}
	if s.dispatches == nil {
		s.dispatches = make(map[string]pinnedDispatchEntry)
	}
	prunePinnedDispatchesLocked(&s, now)
	if len(s.dispatches) >= pinnedDispatchLimit {
		return errPinnedDispatch
	}
	if _, exists := s.dispatches[lease.dispatch]; exists {
		return errPinnedDispatch
	}
	primary := uint8(pinnedPrimaryICMP)
	if spec.taskType == "dual" {
		primary |= pinnedPrimaryTCP
	}
	s.dispatches[lease.dispatch] = pinnedDispatchEntry{
		spec: spec, instance: lease.instance, session: lease.session,
		at: now, expires: now.Add(pinnedDispatchTTL), primary: primary, reference: spec.referenceEnabled,
	}
	pinnedWSSessions[uuid] = s
	return nil
}

// cancelPinnedDispatchAt retires all legs after an unsuccessful delivery;
// the tombstone remains until expiry to prevent reusing the permit.
func cancelPinnedDispatchAt(uuid string, lease pinnedPermitLease, clock func() time.Time) {
	if clock == nil || lease.conn == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	s, ok := pinnedWSSessions[uuid]
	if !ok || connectedClients[uuid] != lease.conn || s.conn != lease.conn || s.instance != lease.instance || s.challenge != lease.session {
		return
	}
	prunePinnedDispatchesLocked(&s, clock())
	dispatch, ok := s.dispatches[lease.dispatch]
	if !ok || dispatch.instance != lease.instance || dispatch.session != lease.session {
		return
	}
	dispatch.primary, dispatch.reference = 0, false
	s.dispatches[lease.dispatch] = dispatch
}

// consumePinnedDispatchAt checks and consumes one role/protocol leg. The
// caller-supplied current spec and assignment are not yet protected by a task
// mutation fence; do not connect this helper to production intake on its own.
func consumePinnedDispatchAt(uuid string, conn *connection.SafeConn, current pinnedDispatchSpec, assigned bool, result v2.PingFamilyResultParams, clock func() time.Time) (pinnedAcceptedResult, error) {
	if clock == nil || conn == nil || !assigned || !validPinnedDispatchSpec(current) {
		return pinnedAcceptedResult{}, errPinnedDispatch
	}
	mu.Lock()
	defer mu.Unlock()
	now := clock()
	s, ok := pinnedWSSessions[uuid]
	if !ok || connectedClients[uuid] != conn || s.conn != conn || !s.active || !now.Before(s.expires) {
		return pinnedAcceptedResult{}, errPinnedDispatch
	}
	prunePinnedDispatchesLocked(&s, now)
	dispatch, ok := s.dispatches[result.DispatchID]
	if !ok || dispatch.spec != current || s.instance != dispatch.instance || s.challenge != dispatch.session {
		return pinnedAcceptedResult{}, errPinnedDispatch
	}
	policy := pinnedResultPolicy{
		TaskID: current.taskID, ConfigGeneration: current.configGeneration,
		Instance: dispatch.instance, Session: dispatch.session, DispatchID: result.DispatchID,
		Family: current.family, TaskType: current.taskType, Assigned: true, SessionActive: true,
		DispatchPending: true, ReferenceEnabled: current.referenceEnabled,
	}
	if validatePinnedResult(policy, result) != nil {
		return pinnedAcceptedResult{}, errPinnedDispatch
	}
	if result.Role == "reference" {
		if !dispatch.reference {
			return pinnedAcceptedResult{}, errPinnedDispatch
		}
		dispatch.reference = false
	} else {
		leg := uint8(pinnedPrimaryICMP)
		if current.taskType == "dual" {
			switch result.PingType {
			case "tcp":
				leg = pinnedPrimaryTCP
			case "dual":
				leg = pinnedPrimaryICMP | pinnedPrimaryTCP
			}
		}
		if dispatch.primary&leg != leg {
			return pinnedAcceptedResult{}, errPinnedDispatch
		}
		dispatch.primary &^= leg
	}
	// Keep the consumed ID as a tombstone until expiry so the same lease
	// cannot register again after its last result is consumed.
	s.dispatches[result.DispatchID] = dispatch
	return pinnedAcceptedResult{at: dispatch.at, result: result}, nil
}
