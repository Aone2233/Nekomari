package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"gorm.io/gorm"
)

const (
	pinnedDiagnosticLimit = 4096
	// This exceeds the dispatch lifetime, so an expired observation cannot be
	// replaced by an older, still-admissible result from the same session.
	pinnedDiagnosticTTL = 2 * time.Minute
)

type pinnedDiagnosticKey struct {
	task uint
	uuid string
	role string
	leg  string
}

type pinnedDiagnosticEntry struct {
	generation string
	session    string
	sequence   uint64
	at         time.Time
	status     string
}

type pinnedDiagnosticStore struct {
	mu      sync.Mutex
	entries map[pinnedDiagnosticKey]pinnedDiagnosticEntry
	limit   int
	ttl     time.Duration
	dropped atomic.Uint64
}

func newPinnedDiagnosticStore(limit int, ttl time.Duration) *pinnedDiagnosticStore {
	return &pinnedDiagnosticStore{entries: make(map[pinnedDiagnosticKey]pinnedDiagnosticEntry), limit: limit, ttl: ttl}
}

var pinnedDiagnostics = newPinnedDiagnosticStore(pinnedDiagnosticLimit, pinnedDiagnosticTTL)

// PinnedDiagnosticSnapshot is a point-in-time administrative observation, not
// authorization to dispatch or proof that the next probe will run. Unknown
// and unavailable are deliberately distinct from a successful measurement.
type PinnedDiagnosticSnapshot struct {
	State          string                `json:"state"`
	ObservedAt     time.Time             `json:"observed_at"`
	ValidUntil     time.Time             `json:"valid_until"`
	DroppedUpdates uint64                `json:"dropped_updates"`
	Legs           []PinnedDiagnosticLeg `json:"legs"`
}

type PinnedDiagnosticLeg struct {
	Role       string     `json:"role"`
	Leg        string     `json:"leg"`
	Status     string     `json:"status"`
	At         *time.Time `json:"at,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

var ErrPinnedDiagnosticNotFound = errors.New("pinned task assignment not found")

// SnapshotPinnedDiagnostic reloads the task under its mutation guard, holds
// credential admission for the assigned node, and snapshots the current socket
// under its session guard. Never substitute a cached task or Agent version.
func SnapshotPinnedDiagnostic(ctx context.Context, taskID uint, uuid string) (PinnedDiagnosticSnapshot, error) {
	db := dbcore.ReadyDBInstance()
	if db == nil {
		return PinnedDiagnosticSnapshot{}, errors.New("database unavailable")
	}
	return snapshotPinnedDiagnosticWith(ctx, db, taskID, uuid, pinnedDiagnostics, time.Now)
}

func snapshotPinnedDiagnosticWith(ctx context.Context, db *gorm.DB, taskID uint, uuid string, store *pinnedDiagnosticStore, clock func() time.Time) (PinnedDiagnosticSnapshot, error) {
	if ctx == nil || db == nil || taskID == 0 || uuid == "" || len(uuid) > 64 || store == nil || clock == nil {
		return PinnedDiagnosticSnapshot{}, errors.New("invalid pinned diagnostic request")
	}
	var snapshot PinnedDiagnosticSnapshot
	err := clientguard.With(ctx, uuid, func() error {
		return pingguard.With(ctx, taskID, func() error {
			var node models.Client
			if err := db.WithContext(ctx).Select("uuid").Where("uuid = ?", uuid).Take(&node).Error; err != nil {
				return err
			}
			var task models.PingTask
			if err := db.WithContext(ctx).Take(&task, taskID).Error; err != nil {
				return err
			}
			if !task.AppliesToClient(uuid) || !validPinnedDispatchSpec(makePinnedDispatchSpec(task)) {
				return ErrPinnedDiagnosticNotFound
			}
			unlock, err := lockPinnedSession(ctx, uuid)
			if err != nil {
				return err
			}
			defer unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			now := clock()
			snapshot = pinnedDiagnosticSnapshotLocked(task, uuid, store, now)
			return nil
		})
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PinnedDiagnosticSnapshot{}, ErrPinnedDiagnosticNotFound
	}
	return snapshot, err
}

// The caller holds credential, task and node-session guards, in that order.
// The diagnostic store never blocks a credential/session retirement.
func pinnedDiagnosticSnapshotLocked(task models.PingTask, uuid string, store *pinnedDiagnosticStore, now time.Time) PinnedDiagnosticSnapshot {
	snapshot := PinnedDiagnosticSnapshot{State: "unknown", ObservedAt: now.UTC(), ValidUntil: now.Add(store.ttl).UTC(),
		DroppedUpdates: store.dropped.Load(), Legs: make([]PinnedDiagnosticLeg, 0, 3)}
	addLeg := func(role, leg string) {
		snapshot.Legs = append(snapshot.Legs, PinnedDiagnosticLeg{Role: role, Leg: leg, Status: "unknown"})
	}
	if task.Type == "dual" {
		addLeg("primary", "icmp")
		addLeg("primary", "tcp")
	} else {
		addLeg("primary", task.Type)
	}
	if strings.TrimSpace(task.Reference) != "" {
		addLeg("reference", "reference")
	}

	mu.RLock()
	conn := connectedClients[uuid]
	presence := presenceOnly[uuid]
	session, hasSession := pinnedWSSessions[uuid]
	mu.RUnlock()
	if conn == nil {
		if presence.expire.After(now) {
			snapshot.State = "post_only"
		} else {
			snapshot.State = "offline"
		}
		return snapshot
	}
	if !hasSession || session.conn != conn || !session.active || !now.Before(session.expires) {
		return snapshot
	}
	// A handshake is not proof that the Agent's execution hook is enabled.
	snapshot.State = "session_active"
	if expiry := session.expires.UTC(); expiry.Before(snapshot.ValidUntil) {
		snapshot.ValidUntil = expiry
	}
	if !store.mu.TryLock() {
		snapshot.State = "unavailable"
		return snapshot
	}
	defer store.mu.Unlock()
	for i := range snapshot.Legs {
		leg := &snapshot.Legs[i]
		key := pinnedDiagnosticKey{task: task.Id, uuid: uuid, role: leg.Role, leg: leg.Leg}
		entry, ok := store.entries[key]
		if !ok || entry.generation != task.PinnedGeneration || entry.session != session.challenge ||
			entry.at.After(now) || !now.Before(entry.at.Add(store.ttl)) {
			continue
		}
		at, validUntil := entry.at.UTC(), entry.at.Add(store.ttl).UTC()
		leg.Status, leg.At, leg.ValidUntil = entry.status, &at, &validUntil
		if validUntil.Before(snapshot.ValidUntil) {
			snapshot.ValidUntil = validUntil
		}
	}
	return snapshot
}

// record is called only while the task and current session fences are held,
// after the dispatch ledger has validated and consumed the result. Sequence is
// panel-assigned at permit issuance; late results cannot replace newer ones.
// Contention and capacity increment a dropped counter instead of delaying
// credential/session retirement.
func (s *pinnedDiagnosticStore) record(task models.PingTask, uuid string, accepted pinnedAcceptedResult, status string) bool {
	r := accepted.result
	if s == nil || s.limit <= 0 || s.ttl <= pinnedDispatchTTL || len(uuid) == 0 || len(uuid) > 64 ||
		task.Id == 0 || task.Id != r.TaskID || !validPinnedWSID(task.PinnedGeneration) || task.PinnedGeneration != r.ConfigGeneration ||
		!validPinnedWSID(r.Session) || accepted.sequence == 0 || accepted.at.IsZero() || status == "" || len(status) > 32 {
		return false
	}
	keys := pinnedDiagnosticKeys(task, uuid, accepted)
	if len(keys) == 0 {
		return false
	}
	return s.recordKeys(keys, task.PinnedGeneration, r.Session, accepted.sequence, accepted.at, status)
}

func (s *pinnedDiagnosticStore) recordKeys(keys []pinnedDiagnosticKey, generation, session string, sequence uint64, at time.Time, status string) bool {
	if !s.mu.TryLock() {
		s.dropped.Add(1)
		return false
	}
	defer s.mu.Unlock()
	var needed int
	for _, key := range keys {
		if _, ok := s.entries[key]; !ok {
			needed++
		}
	}
	if len(s.entries)+needed > s.limit {
		for key, entry := range s.entries {
			if !at.Before(entry.at.Add(s.ttl)) {
				delete(s.entries, key)
			}
		}
		needed = 0
		for _, key := range keys {
			if _, ok := s.entries[key]; !ok {
				needed++
			}
		}
		if len(s.entries)+needed > s.limit {
			s.dropped.Add(1)
			return false
		}
	}
	for _, key := range keys {
		old, ok := s.entries[key]
		if ok && at.Before(old.at.Add(s.ttl)) && old.generation == generation && old.session == session && old.sequence >= sequence {
			continue
		}
		s.entries[key] = pinnedDiagnosticEntry{generation: generation, session: session,
			sequence: sequence, at: at, status: status}
	}
	return true
}

// recordPinnedPermitFailureWith cannot authorize a probe or infer why the
// permit failed. It only records an actually reserved sequence after checking
// that the same node, assignment, task incarnation and socket remain current.
func recordPinnedPermitFailureWith(ctx context.Context, db *gorm.DB, uuid string, scheduled models.PingTask, attempt pinnedPermitLease, store *pinnedDiagnosticStore, clock func() time.Time) {
	if ctx == nil || db == nil || store == nil || clock == nil || uuid == "" || len(uuid) > 64 ||
		scheduled.Id == 0 || attempt.conn == nil || attempt.sequence == 0 ||
		!validPinnedWSID(attempt.instance) || !validPinnedWSID(attempt.session) || ctx.Err() != nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, pinnedWriteWait)
	defer cancel()
	_ = clientguard.With(bounded, uuid, func() error {
		return pingguard.With(bounded, scheduled.Id, func() error {
			var node models.Client
			if err := db.WithContext(bounded).Select("uuid").Where("uuid = ?", uuid).Take(&node).Error; err != nil {
				return err
			}
			var task models.PingTask
			if err := db.WithContext(bounded).Take(&task, scheduled.Id).Error; err != nil {
				return err
			}
			spec := makePinnedDispatchSpec(task)
			if !task.AppliesToClient(uuid) || !validPinnedDispatchSpec(spec) || spec != makePinnedDispatchSpec(scheduled) {
				return errPinnedDispatch
			}
			unlock, err := lockPinnedSession(bounded, uuid)
			if err != nil {
				return err
			}
			defer unlock()
			if err := bounded.Err(); err != nil {
				return err
			}
			now := clock()
			mu.RLock()
			session, ok := pinnedWSSessions[uuid]
			current := ok && connectedClients[uuid] == attempt.conn && session.conn == attempt.conn && session.active &&
				session.instance == attempt.instance && session.challenge == attempt.session &&
				now.Before(session.expires) && attempt.sequence <= session.sequence
			mu.RUnlock()
			if !current || now.IsZero() || store.limit <= 0 || store.ttl <= pinnedDispatchTTL {
				return errPinnedDispatch
			}
			accepted := pinnedAcceptedResult{}
			accepted.result.Role, accepted.result.PingType, accepted.result.Diagnostic = "primary", task.Type, "not_probed"
			keys := pinnedDiagnosticKeys(task, uuid, accepted)
			if strings.TrimSpace(task.Reference) != "" {
				accepted.result.Role = "reference"
				keys = append(keys, pinnedDiagnosticKeys(task, uuid, accepted)...)
			}
			if len(keys) > 0 && bounded.Err() == nil {
				store.recordKeys(keys, task.PinnedGeneration, attempt.session, attempt.sequence, now, "permit_unavailable")
			}
			return nil
		})
	})
}

func pinnedDiagnosticKeys(task models.PingTask, uuid string, accepted pinnedAcceptedResult) []pinnedDiagnosticKey {
	r := accepted.result
	key := pinnedDiagnosticKey{task: task.Id, uuid: uuid, role: r.Role}
	if r.Role == "reference" {
		if task.Reference == "" {
			return nil
		}
		key.leg = "reference"
		return []pinnedDiagnosticKey{key}
	}
	if r.Role != "primary" {
		return nil
	}
	if task.Type == "dual" {
		if r.PingType == "dual" && r.Diagnostic != "" {
			key.leg = "icmp"
			other := key
			other.leg = "tcp"
			return []pinnedDiagnosticKey{key, other}
		}
		if r.PingType != "icmp" && r.PingType != "tcp" {
			return nil
		}
		key.leg = r.PingType
	} else if task.Type == "auto" || task.Type == "icmp" || task.Type == "tcp" || task.Type == "http" {
		// Auto may switch between ICMP and TCP across dispatches. Keep one
		// current state for the task rather than an obsolete error per protocol.
		key.leg = task.Type
	} else {
		return nil
	}
	return []pinnedDiagnosticKey{key}
}

// get is a private primitive, not an authenticated API. Callers must first
// establish that the task assignment and session are still current.
func (s *pinnedDiagnosticStore) get(key pinnedDiagnosticKey, generation, session string, now time.Time) (pinnedDiagnosticEntry, bool, bool) {
	if !s.mu.TryLock() {
		return pinnedDiagnosticEntry{}, false, false
	}
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok || entry.generation != generation || entry.session != session || !now.Before(entry.at.Add(s.ttl)) {
		return pinnedDiagnosticEntry{}, false, true
	}
	return entry, true, true
}
