package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

func pinnedDispatchFixture(t *testing.T, uuid, kind string, reference bool) (pinnedPermitLease, pinnedDispatchSpec, time.Time) {
	t.Helper()
	conn, _ := activePanelPinnedSession(t, uuid)
	lease, err := requestPinnedWSPermitWithWriter(t.Context(), uuid, func(_ context.Context, got *connection.SafeConn, req v2.Request) error {
		result := permitResultForRequest(t, req)
		if got != conn || !resolvePinnedPermit(uuid, got, result.DispatchID, &result) {
			t.Fatal("permit response rejected")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := pinnedDispatchSpec{taskID: 7, configGeneration: pinnedTestInstanceB, family: "ipv6", taskType: kind, referenceEnabled: reference}
	now := time.Now()
	if err := registerPinnedDispatchAt(uuid, lease, spec, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	return lease, spec, now
}

func pinnedDispatchResult(lease pinnedPermitLease, spec pinnedDispatchSpec, role, kind, family string) v2.PingFamilyResultParams {
	value, micros := 2, int64(2345)
	return v2.PingFamilyResultParams{
		TaskID: spec.taskID, ConfigGeneration: spec.configGeneration, Instance: lease.instance,
		Session: lease.session, DispatchID: lease.dispatch, Role: role, PingType: kind,
		Family: family, Value: &value, ValueUS: &micros,
	}
}

func TestPinnedDispatchConsumesEachLegOnce(t *testing.T) {
	const uuid = "pinned-dispatch-legs"
	lease, spec, now := pinnedDispatchFixture(t, uuid, "dual", true)
	clock := func() time.Time { return now }
	for _, result := range []v2.PingFamilyResultParams{
		pinnedDispatchResult(lease, spec, "primary", "icmp", "ipv6"),
		pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv6"),
		pinnedDispatchResult(lease, spec, "reference", "icmp", "ipv4"),
	} {
		accepted, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, clock)
		if err != nil || !accepted.at.Equal(now) {
			t.Fatalf("valid leg rejected or wrong panel timestamp: %+v %v", accepted, err)
		}
		if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, clock); err == nil {
			t.Fatal("duplicate leg accepted")
		}
	}
	if err := registerPinnedDispatchAt(uuid, lease, spec, clock); err == nil {
		t.Fatal("consumed lease was registered again")
	}
}

func TestPinnedDispatchRejectsStaleAndMismatchedResults(t *testing.T) {
	for _, mode := range []string{"assignment", "config", "family", "session", "socket", "expiry", "replacement", "disconnect", "reset"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-dispatch-" + mode
			lease, spec, now := pinnedDispatchFixture(t, uuid, "tcp", false)
			result := pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv6")
			conn, assigned := lease.conn, true
			switch mode {
			case "assignment":
				assigned = false
			case "config":
				spec.configGeneration = pinnedTestInstanceA
			case "family":
				result.Family = "ipv4"
			case "session":
				result.Session = pinnedTestInstanceB
			case "socket":
				conn = &connection.SafeConn{}
			case "expiry":
				now = now.Add(pinnedDispatchTTL)
			case "replacement":
				SetConnectedClients(uuid, &connection.SafeConn{})
			case "disconnect":
				DeleteClientConditionally(uuid, conn)
			case "reset":
				ResetClient(uuid)
			}
			if _, err := consumePinnedDispatchAt(uuid, conn, spec, assigned, result, func() time.Time { return now }); err == nil {
				t.Fatal("stale or mismatched result accepted")
			}
		})
	}
}

func TestPinnedDispatchInvalidResultDoesNotConsumeLeg(t *testing.T) {
	const uuid = "pinned-dispatch-invalid"
	lease, spec, now := pinnedDispatchFixture(t, uuid, "auto", false)
	clock := func() time.Time { return now }
	result := pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv4")
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, clock); err == nil {
		t.Fatal("wrong-family result accepted")
	}
	result.Family = "ipv6"
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, clock); err != nil {
		t.Fatalf("valid result consumed by invalid result: %v", err)
	}
}

func TestPinnedDispatchDiagnosticsConsumeOnlyExpectedLeg(t *testing.T) {
	const uuid = "pinned-dispatch-diagnostic"
	lease, spec, now := pinnedDispatchFixture(t, uuid, "dual", true)
	clock := func() time.Time { return now }
	result := pinnedDispatchResult(lease, spec, "primary", "dual", "")
	result.Value, result.ValueUS, result.Diagnostic = nil, nil, "dns_error"
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, clock); err != nil {
		t.Fatal(err)
	}
	leg := pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv6")
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, leg, clock); err == nil {
		t.Fatal("dual diagnostic did not consume both primary legs")
	}
	ref := pinnedDispatchResult(lease, spec, "reference", "icmp", "ipv4")
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, ref, clock); err != nil {
		t.Fatalf("primary diagnostic consumed independent reference: %v", err)
	}
}

func TestPinnedDispatchCapacityExpiryAndLeaseReplay(t *testing.T) {
	const uuid = "pinned-dispatch-capacity"
	lease, spec, now := pinnedDispatchFixture(t, uuid, "tcp", false)
	clock := func() time.Time { return now }
	for i := 1; i < pinnedDispatchLimit; i++ {
		id, err := newPinnedWSChallenge()
		if err != nil {
			t.Fatal(err)
		}
		next := lease
		next.dispatch = id
		if err := registerPinnedDispatchAt(uuid, next, spec, clock); err != nil {
			t.Fatal(err)
		}
	}
	next := lease
	next.dispatch, _ = newPinnedWSChallenge()
	if err := registerPinnedDispatchAt(uuid, next, spec, clock); err == nil {
		t.Fatal("capacity evicted a live dispatch")
	}
	now = now.Add(pinnedDispatchTTL)
	if err := registerPinnedDispatchAt(uuid, lease, spec, clock); err == nil {
		t.Fatal("expired permit registered after tombstone expiry")
	}
	// A freshly issued lease may reuse the capacity after old registrations expire.
	next.validUntil = now.Add(time.Second)
	if err := registerPinnedDispatchAt(uuid, next, spec, clock); err != nil {
		t.Fatalf("expired registrations did not free capacity: %v", err)
	}
}

func TestPinnedDispatchConcurrentDuplicateResult(t *testing.T) {
	const uuid = "pinned-dispatch-concurrent"
	lease, spec, now := pinnedDispatchFixture(t, uuid, "tcp", false)
	result := pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv6")
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, func() time.Time { return now }); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("accepted %d copies, want one", successes.Load())
	}
}

func TestPinnedDispatchFailedDeliveryRetiresAllLegs(t *testing.T) {
	const uuid = "pinned-dispatch-canceled"
	lease, spec, now := pinnedDispatchFixture(t, uuid, "dual", true)
	clock := func() time.Time { return now }
	cancelPinnedDispatchAt(uuid, lease, clock)
	for _, result := range []v2.PingFamilyResultParams{
		pinnedDispatchResult(lease, spec, "primary", "icmp", "ipv6"),
		pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv6"),
		pinnedDispatchResult(lease, spec, "reference", "icmp", "ipv4"),
	} {
		if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, clock); err == nil {
			t.Fatal("result accepted after failed delivery")
		}
	}
	if err := registerPinnedDispatchAt(uuid, lease, spec, clock); err == nil {
		t.Fatal("failed delivery permit registered again")
	}
}

func TestPinnedDispatchOldCancellationCannotRetireSuccessor(t *testing.T) {
	const uuid = "pinned-dispatch-cancel-successor"
	old, _, _ := pinnedDispatchFixture(t, uuid, "tcp", false)
	lease, spec, now := pinnedDispatchFixture(t, uuid, "tcp", false)
	clock := func() time.Time { return now }
	cancelPinnedDispatchAt(uuid, old, clock)
	result := pinnedDispatchResult(lease, spec, "primary", "tcp", "ipv6")
	if _, err := consumePinnedDispatchAt(uuid, lease.conn, spec, true, result, clock); err != nil {
		t.Fatalf("old cancellation retired successor: %v", err)
	}
}
