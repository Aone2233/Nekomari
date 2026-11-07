package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

func diagnosticFixture() (models.PingTask, pinnedAcceptedResult) {
	task := models.PingTask{Id: 7, Type: "auto", TargetFamily: "ipv6", PinnedGeneration: pinnedTestInstanceA}
	return task, pinnedAcceptedResult{at: time.Now(), sequence: 1, result: v2.PingFamilyResultParams{
		TaskID: task.Id, ConfigGeneration: task.PinnedGeneration, Session: pinnedTestInstanceB, Role: "primary", PingType: "auto", Diagnostic: "no_family_address",
	}}
}

func TestPinnedDiagnosticsRejectOlderResultsAndFenceSessions(t *testing.T) {
	task, result := diagnosticFixture()
	store := newPinnedDiagnosticStore(4, pinnedDiagnosticTTL)
	const uuid = "diagnostic-order"
	key := pinnedDiagnosticKey{task: task.Id, uuid: uuid, role: "primary", leg: "auto"}
	result.sequence = 2
	if !store.record(task, uuid, result, "recorded") {
		t.Fatal("newer result rejected")
	}
	result.sequence = 1
	result.at = result.at.Add(time.Second)
	store.record(task, uuid, result, "no_family_address")
	entry, found, available := store.get(key, task.PinnedGeneration, result.result.Session, result.at)
	if !available || !found || entry.status != "recorded" || entry.sequence != 2 {
		t.Fatalf("older result replaced newer: %+v found=%v available=%v", entry, found, available)
	}
	if _, found, available := store.get(key, pinnedTestInstanceB, result.result.Session, result.at); found || !available {
		t.Fatal("stale task generation visible")
	}
	if _, found, available := store.get(key, task.PinnedGeneration, pinnedTestInstanceA, result.at); found || !available {
		t.Fatal("stale session visible")
	}
	result.result.Session = pinnedTestInstanceA
	result.sequence = 1
	if !store.record(task, uuid, result, "dns_error") {
		t.Fatal("current replacement session rejected")
	}
	entry, found, available = store.get(key, task.PinnedGeneration, result.result.Session, result.at)
	if !available || !found || entry.status != "dns_error" {
		t.Fatal("replacement session not visible")
	}
	if _, found, available := store.get(key, task.PinnedGeneration, result.result.Session, result.at.Add(pinnedDiagnosticTTL)); found || !available {
		t.Fatal("expired diagnostic remains visible")
	}
}

func TestPinnedDiagnosticsBoundedCapacityAndContention(t *testing.T) {
	task, result := diagnosticFixture()
	store := newPinnedDiagnosticStore(1, pinnedDiagnosticTTL)
	if !store.record(task, "first", result, "dns_error") {
		t.Fatal("initial diagnostic rejected")
	}
	if store.record(task, "second", result, "dns_error") {
		t.Fatal("capacity evicted a live diagnostic")
	}
	result.at = result.at.Add(pinnedDiagnosticTTL)
	if !store.record(task, "second", result, "recorded") {
		t.Fatal("expired diagnostic did not free capacity")
	}
	store.mu.Lock()
	if store.record(task, "second", result, "dns_error") {
		t.Error("contended writer did not fail closed")
	}
	if _, _, available := store.get(pinnedDiagnosticKey{}, task.PinnedGeneration, result.result.Session, result.at); available {
		t.Error("contended reader claimed availability")
	}
	store.mu.Unlock()
	if got := store.dropped.Load(); got != 2 {
		t.Fatalf("dropped=%d, want capacity and contention failures", got)
	}
}

func TestPinnedDiagnosticsDualRolesAndAtomicCapacity(t *testing.T) {
	task, result := diagnosticFixture()
	task.Type, task.Reference = "dual", "gateway"
	result.result.PingType = "dual"
	store := newPinnedDiagnosticStore(1, pinnedDiagnosticTTL)
	if store.record(task, "node", result, "dns_error") || len(store.entries) != 0 {
		t.Fatal("combined dual diagnostic was only partially recorded")
	}
	store = newPinnedDiagnosticStore(4, pinnedDiagnosticTTL)
	if !store.record(task, "node", result, "dns_error") {
		t.Fatal("dual diagnostic rejected")
	}
	result.sequence++
	result.result.PingType, result.result.Diagnostic = "tcp", ""
	if !store.record(task, "node", result, "recorded") {
		t.Fatal("TCP recovery rejected")
	}
	for leg, want := range map[string]string{"icmp": "dns_error", "tcp": "recorded"} {
		entry, found, available := store.get(pinnedDiagnosticKey{task: task.Id, uuid: "node", role: "primary", leg: leg}, task.PinnedGeneration, result.result.Session, result.at)
		if !available || !found || entry.status != want {
			t.Fatalf("leg %s: %+v found=%v available=%v", leg, entry, found, available)
		}
	}
	result.result.Role, result.result.PingType = "reference", "icmp"
	if !store.record(task, "node", result, "write_unavailable") {
		t.Fatal("independent reference rejected")
	}
	entry, found, available := store.get(pinnedDiagnosticKey{task: task.Id, uuid: "node", role: "reference", leg: "reference"}, task.PinnedGeneration, result.result.Session, result.at)
	if !available || !found || entry.status != "write_unavailable" {
		t.Fatal("reference status missing")
	}
}

func TestPinnedDiagnosticsCapacityCountsExpiredRequestedLeg(t *testing.T) {
	task, result := diagnosticFixture()
	task.Type = "dual"
	result.result.PingType = "icmp"
	store := newPinnedDiagnosticStore(2, pinnedDiagnosticTTL)
	if !store.record(task, "node", result, "dns_error") {
		t.Fatal("initial leg rejected")
	}
	later := result
	later.at = later.at.Add(pinnedDiagnosticTTL - time.Second)
	if !store.record(task, "other", later, "dns_error") {
		t.Fatal("second node rejected")
	}
	result.at = result.at.Add(pinnedDiagnosticTTL)
	result.result.PingType = "dual"
	if store.record(task, "node", result, "dns_error") || len(store.entries) != 1 {
		t.Fatal("expired requested leg bypassed the dual capacity check")
	}
	key := pinnedDiagnosticKey{task: task.Id, uuid: "other", role: "primary", leg: "icmp"}
	if _, found, available := store.get(key, task.PinnedGeneration, result.result.Session, result.at); !available || !found {
		t.Fatal("unrelated live status was evicted")
	}
}

func TestPinnedDiagnosticsConcurrentReadsAndWrites(t *testing.T) {
	task, result := diagnosticFixture()
	store := newPinnedDiagnosticStore(64, pinnedDiagnosticTTL)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			uuid := fmt.Sprintf("node-%d", i)
			for j := 0; j < 100; j++ {
				store.record(task, uuid, result, "recorded")
				store.get(pinnedDiagnosticKey{task: task.Id, uuid: uuid, role: "primary", leg: "auto"}, task.PinnedGeneration, result.result.Session, result.at)
			}
		}(i)
	}
	wg.Wait()
	if len(store.entries) > store.limit {
		t.Fatal("diagnostic store exceeded its bound")
	}
}

func TestPinnedAdmissionDiagnosticAndWriteFailureAreRecorded(t *testing.T) {
	const uuid = "pinned-diagnostic-admission"
	db, task, lease, result := pinnedAdmissionFixture(t, uuid)
	key := pinnedDiagnosticKey{task: task.Id, uuid: uuid, role: "primary", leg: "auto"}
	result.Value, result.ValueUS, result.Family, result.Diagnostic = nil, nil, "", "no_family_address"
	if err := admitPinnedResultWith(t.Context(), db, uuid, lease.conn, result, func(context.Context, models.PingRecord) error {
		t.Fatal("diagnostic attempted a metric write")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entry, found, available := pinnedDiagnostics.get(key, task.PinnedGeneration, lease.session, time.Now())
	if !available || !found || entry.status != "no_family_address" {
		t.Fatalf("diagnostic not recorded: %+v found=%v available=%v", entry, found, available)
	}
	next, err := requestPinnedWSPermitWithWriter(t.Context(), uuid, func(_ context.Context, conn *connection.SafeConn, req v2.Request) error {
		response := permitResultForRequest(t, req)
		if !resolvePinnedPermit(uuid, conn, response.DispatchID, &response) {
			return errors.New("permit response rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerPinnedDispatchAt(uuid, next, makePinnedDispatchSpec(task), time.Now); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("fixture writer failed")
	nextResult := pinnedDispatchResult(next, makePinnedDispatchSpec(task), "primary", "tcp", "ipv6")
	if err := admitPinnedResultWith(t.Context(), db, uuid, next.conn, nextResult, func(context.Context, models.PingRecord) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("write error = %v", err)
	}
	entry, found, available = pinnedDiagnostics.get(key, task.PinnedGeneration, next.session, time.Now())
	if !available || !found || entry.status != "write_unavailable" || entry.sequence != next.sequence {
		t.Fatalf("write failure not recorded: %+v found=%v available=%v", entry, found, available)
	}
}

func TestPinnedPermitFailureDiagnosticOrdersLegsAndRecovery(t *testing.T) {
	const uuid = "pinned-permit-diagnostic-order"
	db, task, old, oldResult := pinnedAdmissionFixture(t, uuid)
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Client{UUID: uuid, Token: "fixture"}).Error; err != nil {
		t.Fatal(err)
	}
	task.Type, task.Reference = "dual", "gateway"
	if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(map[string]any{"type": task.Type, "reference": task.Reference}).Error; err != nil {
		t.Fatal(err)
	}
	store := newPinnedDiagnosticStore(4, pinnedDiagnosticTTL)
	attempt, err := requestPinnedWSPermitWithWriter(t.Context(), uuid, func(_ context.Context, conn *connection.SafeConn, req v2.Request) error {
		response := permitResultForRequest(t, req)
		if resolvePinnedPermit(uuid, conn, response.DispatchID, nil) {
			t.Fatal("invalid response authorized a permit")
		}
		return nil
	})
	if err == nil || attempt.sequence <= old.sequence || attempt.dispatch != "" || attempt.ticket != "" {
		t.Fatalf("failed attempt has authorization or lacks ordering: attempt=%+v err=%v", attempt, err)
	}
	now := time.Now()
	recordPinnedPermitFailureWith(t.Context(), db, uuid, task, attempt, store, func() time.Time { return now })
	snapshot, err := snapshotPinnedDiagnosticWith(t.Context(), db, task.Id, uuid, store, func() time.Time { return now })
	if err != nil || snapshot.State != "session_active" || len(snapshot.Legs) != 3 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	for _, leg := range snapshot.Legs {
		if leg.Status != "permit_unavailable" {
			t.Fatalf("failed permit did not cover %s/%s: %+v", leg.Role, leg.Leg, leg)
		}
	}
	// An older dispatch can finish after a failed permit, but its stale
	// diagnostic must not replace the later skipped cycle on either leg.
	oldResult.PingType, oldResult.Diagnostic = "dual", "dns_error"
	store.record(task, uuid, pinnedAcceptedResult{at: now.Add(time.Second), sequence: old.sequence, result: oldResult}, "dns_error")
	snapshot, err = snapshotPinnedDiagnosticWith(t.Context(), db, task.Id, uuid, store, func() time.Time { return now.Add(time.Second) })
	if err != nil || snapshot.Legs[0].Status != "permit_unavailable" || snapshot.Legs[1].Status != "permit_unavailable" {
		t.Fatalf("older result replaced newer failed attempt: %+v err=%v", snapshot, err)
	}
	newer, err := requestPinnedWSPermitWithWriter(t.Context(), uuid, func(_ context.Context, conn *connection.SafeConn, req v2.Request) error {
		response := permitResultForRequest(t, req)
		if !resolvePinnedPermit(uuid, conn, response.DispatchID, &response) {
			t.Fatal("recovery permit rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered := pinnedDispatchResult(newer, makePinnedDispatchSpec(task), "primary", "tcp", "ipv6")
	store.record(task, uuid, pinnedAcceptedResult{at: now.Add(2 * time.Second), sequence: newer.sequence, result: recovered}, "recorded")
	recordPinnedPermitFailureWith(t.Context(), db, uuid, task, attempt, store, func() time.Time { return now.Add(3 * time.Second) })
	snapshot, err = snapshotPinnedDiagnosticWith(t.Context(), db, task.Id, uuid, store, func() time.Time { return now.Add(3 * time.Second) })
	if err != nil || snapshot.Legs[0].Status != "permit_unavailable" || snapshot.Legs[1].Status != "recorded" || snapshot.Legs[2].Status != "permit_unavailable" {
		t.Fatalf("late failure replaced recovery or other legs: %+v err=%v", snapshot, err)
	}
}

func TestPinnedPermitFailureDiagnosticFencesAndCancellation(t *testing.T) {
	for _, mode := range []string{"assignment", "generation", "node-deleted", "task-deleted", "replacement", "rehandshake", "expired", "canceled", "no-sequence"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-permit-diag-" + mode
			db, task, lease, _ := pinnedAdmissionFixture(t, uuid)
			if err := db.AutoMigrate(&models.Client{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&models.Client{UUID: uuid, Token: "fixture"}).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			clock := time.Now
			switch mode {
			case "assignment":
				if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("clients", models.StringArray{"other"}).Error; err != nil {
					t.Fatal(err)
				}
			case "generation":
				if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("pinned_generation", pinnedTestInstanceB).Error; err != nil {
					t.Fatal(err)
				}
			case "node-deleted":
				if err := db.Where("uuid = ?", uuid).Delete(&models.Client{}).Error; err != nil {
					t.Fatal(err)
				}
			case "task-deleted":
				if err := db.Delete(&task).Error; err != nil {
					t.Fatal(err)
				}
			case "replacement":
				SetConnectedClients(uuid, &connection.SafeConn{})
			case "rehandshake":
				if _, ok := BeginPinnedWSHandshake(uuid, lease.conn, pinnedTestInstanceA, 1); !ok {
					t.Fatal("handshake failed")
				}
			case "expired":
				clock = func() time.Time { return time.Now().Add(pinnedWSLease) }
			case "canceled":
				cancel()
			case "no-sequence":
				lease.sequence = 0
			}
			store := newPinnedDiagnosticStore(4, pinnedDiagnosticTTL)
			recordPinnedPermitFailureWith(ctx, db, uuid, task, lease, store, clock)
			if len(store.entries) != 0 || store.dropped.Load() != 0 {
				t.Fatalf("stale/canceled attempt recorded: %+v", store.entries)
			}
		})
	}
}
