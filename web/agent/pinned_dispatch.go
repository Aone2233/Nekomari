package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

// This ledger does not itself fence task mutation, assignment, credential
// retirement or the metric write. Production delivery and admission must use
// the surrounding guards rather than calling these primitives alone.
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
	target           string
	reference        string
}

// makePinnedDispatchSpec reads the internal incarnation from the persisted
// row. Membership is checked separately while the caller holds the task guard.
func makePinnedDispatchSpec(task models.PingTask) pinnedDispatchSpec {
	return pinnedDispatchSpec{taskID: task.Id, configGeneration: pinnedTaskGeneration(task), family: task.TargetFamily,
		taskType: task.Type, target: task.Target, reference: task.Reference}
}

type pinnedDispatchEntry struct {
	spec      pinnedDispatchSpec
	instance  string
	session   string
	sequence  uint64
	expires   time.Time
	primary   uint8
	reference bool
}

// pinnedAcceptedResult carries the panel's admission time, not an Agent clock.
// It does not itself authorize writing to the metric store.
type pinnedAcceptedResult struct {
	at       time.Time
	sequence uint64
	result   v2.PingFamilyResultParams
}

func validPinnedDispatchSpec(spec pinnedDispatchSpec) bool {
	if spec.taskID == 0 || !validPinnedWSID(spec.configGeneration) || (spec.family != "ipv4" && spec.family != "ipv6") || spec.target == "" {
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
		!validPinnedWSID(lease.session) || !validPinnedWSID(lease.dispatch) || !validPinnedWSID(lease.ticket) || lease.sequence == 0 || lease.conn == nil {
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
	for _, existing := range s.dispatches {
		if existing.spec.taskID != spec.taskID {
			continue
		}
		// Do not supersede an in-flight probe: when a task interval is shorter
		// than the probe time, doing so can reject every result indefinitely.
		// Tombstones still prevent delayed older permits from overtaking it.
		if existing.sequence >= lease.sequence || existing.primary != 0 || existing.reference {
			return errPinnedDispatch
		}
	}
	primary := uint8(pinnedPrimaryICMP)
	if spec.taskType == "dual" {
		primary |= pinnedPrimaryTCP
	}
	s.dispatches[lease.dispatch] = pinnedDispatchEntry{
		spec: spec, instance: lease.instance, session: lease.session,
		sequence: lease.sequence, expires: now.Add(pinnedDispatchTTL), primary: primary, reference: strings.TrimSpace(spec.reference) != "",
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
	unlock := lockPinnedSessionLifecycle(uuid)
	defer unlock()
	cancelPinnedDispatchLocked(uuid, lease, clock)
}

// cancelPinnedDispatchLocked requires the node session guard.
func cancelPinnedDispatchLocked(uuid string, lease pinnedPermitLease, clock func() time.Time) {
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
	return consumePinnedDispatchWithWriterAt(context.Background(), uuid, conn, current, assigned, result, clock, func(pinnedAcceptedResult) error { return nil })
}

// The caller holds credential and task admission. The per-node session guard
// orders retirement with the writer, while mu covers only the state update.
// A failed writer still consumes the leg to prevent duplicate observations.
func consumePinnedDispatchWithWriterAt(ctx context.Context, uuid string, conn *connection.SafeConn, current pinnedDispatchSpec, assigned bool, result v2.PingFamilyResultParams, clock func() time.Time, write func(pinnedAcceptedResult) error) (pinnedAcceptedResult, error) {
	if ctx == nil || clock == nil || conn == nil || !assigned || !validPinnedDispatchSpec(current) || write == nil {
		return pinnedAcceptedResult{}, errPinnedDispatch
	}
	unlock, err := lockPinnedSession(ctx, uuid)
	if err != nil {
		return pinnedAcceptedResult{}, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return pinnedAcceptedResult{}, err
	}
	accepted, err := consumePinnedDispatchLocked(uuid, conn, current, result, clock)
	if err != nil {
		return pinnedAcceptedResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return pinnedAcceptedResult{}, err
	}
	if err := write(accepted); err != nil {
		return pinnedAcceptedResult{}, err
	}
	return accepted, nil
}

// consumePinnedDispatchLocked mutates only the session ledger under mu.
// Its caller holds the node's session guard until its metric write completes.
func consumePinnedDispatchLocked(uuid string, conn *connection.SafeConn, current pinnedDispatchSpec, result v2.PingFamilyResultParams, clock func() time.Time) (pinnedAcceptedResult, error) {
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
	// A later scheduled probe may finish first. Results are timestamped on
	// admission, so accepting an older dispatch afterwards would invert the
	// measurements and could replace a newer diagnostic. A registered newer
	// dispatch supersedes every older leg, even if its delivery later fails.
	for _, newer := range s.dispatches {
		if newer.spec.taskID == dispatch.spec.taskID && newer.sequence > dispatch.sequence {
			return pinnedAcceptedResult{}, errPinnedDispatch
		}
	}
	policy := pinnedResultPolicy{
		TaskID: current.taskID, ConfigGeneration: current.configGeneration,
		Instance: dispatch.instance, Session: dispatch.session, DispatchID: result.DispatchID,
		Family: current.family, TaskType: current.taskType, Assigned: true, SessionActive: true,
		DispatchPending: true, ReferenceEnabled: strings.TrimSpace(current.reference) != "",
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
	return pinnedAcceptedResult{at: now, sequence: dispatch.sequence, result: result}, nil
}
