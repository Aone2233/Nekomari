package agent

import (
	"sync"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
)

func activePanelPinnedSession(t *testing.T, uuid string) (*connection.SafeConn, string) {
	t.Helper()
	conn := &connection.SafeConn{}
	SetConnectedClients(uuid, conn)
	t.Cleanup(func() { ResetClient(uuid) })
	challenge, ok := BeginPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, 1)
	if !ok || !AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, challenge) {
		t.Fatal("failed to activate test session")
	}
	return conn, challenge
}

func awaitPanelPermit(t *testing.T, ch <-chan pinnedPermitOutcome, success bool) pinnedPermitOutcome {
	t.Helper()
	select {
	case result := <-ch:
		if (result.err == nil) != success {
			t.Fatalf("success=%v result=%+v", success, result)
		}
		return result
	case <-time.After(time.Second):
		t.Fatal("permit waiter did not complete")
		return pinnedPermitOutcome{}
	}
}

func permitResultForRequest(t *testing.T, request v2.Request) v2.PingFamilyPermitResult {
	t.Helper()
	p, ok := request.Params.(v2.PingFamilyPermitParams)
	if !ok || request.Method != v2.MethodAgentPingFamilyPermit || request.ID != p.DispatchID || !validPinnedWSID(p.DispatchID) {
		t.Fatalf("invalid request: %+v", request)
	}
	return v2.PingFamilyPermitResult{DispatchID: p.DispatchID, Sequence: p.Sequence, FreshnessTicket: pinnedTestInstanceB}
}

func TestPinnedPanelPermitReservationAndSingleUse(t *testing.T) {
	const uuid = "pinned-panel-permit"
	conn, challenge := activePanelPinnedSession(t, uuid)
	got, request, waiter, err := reservePinnedPermit(uuid)
	if err != nil || got != conn {
		t.Fatalf("reserve conn=%p err=%v", got, err)
	}
	params := request.Params.(v2.PingFamilyPermitParams)
	if params.Instance != pinnedTestInstanceA || params.Session != challenge || params.Sequence != 1 {
		t.Fatalf("permit params=%+v", params)
	}
	result := permitResultForRequest(t, request)
	if resolvePinnedPermit(uuid, &connection.SafeConn{}, result.DispatchID, &result) {
		t.Fatal("other socket resolved permit")
	}
	if !resolvePinnedPermit(uuid, conn, result.DispatchID, &result) {
		t.Fatal("current session could not resolve permit")
	}
	if got := awaitPanelPermit(t, waiter, true); got.permit != result {
		t.Fatalf("result=%+v", got)
	}
	if resolvePinnedPermit(uuid, conn, result.DispatchID, &result) {
		t.Fatal("replayed result resolved twice")
	}
	_, next, ch, err := reservePinnedPermit(uuid)
	if err != nil || next.Params.(v2.PingFamilyPermitParams).Sequence != 2 {
		t.Fatal("permit sequence did not advance")
	}
	cancelPinnedPermit(uuid, conn, next.ID.(string))
	awaitPanelPermit(t, ch, false)
}

func TestPinnedPanelPermitRetirementWakesWaiter(t *testing.T) {
	for _, mode := range []string{"replacement", "new challenge", "disconnect", "reset"} {
		t.Run(mode, func(t *testing.T) {
			uuid := "pinned-panel-retire-" + mode
			conn, _ := activePanelPinnedSession(t, uuid)
			_, req, waiter, err := reservePinnedPermit(uuid)
			if err != nil {
				t.Fatal(err)
			}
			result := permitResultForRequest(t, req)
			switch mode {
			case "replacement":
				SetConnectedClients(uuid, &connection.SafeConn{})
			case "new challenge":
				if _, ok := BeginPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, 1); !ok {
					t.Fatal("replacement challenge failed")
				}
			case "disconnect":
				DeleteClientConditionally(uuid, conn)
			case "reset":
				ResetClient(uuid)
			}
			awaitPanelPermit(t, waiter, false)
			if resolvePinnedPermit(uuid, conn, result.DispatchID, &result) {
				t.Fatal("retired session resolved a permit")
			}
		})
	}
}

func TestPinnedPanelPermitBoundsAndInvalidResults(t *testing.T) {
	const uuid = "pinned-panel-bounds"
	conn, _ := activePanelPinnedSession(t, uuid)
	waiters := make([]<-chan pinnedPermitOutcome, 0, pinnedPermitLimit)
	var first v2.Request
	for i := 0; i < pinnedPermitLimit; i++ {
		_, req, ch, err := reservePinnedPermit(uuid)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = req
		}
		waiters = append(waiters, ch)
	}
	if _, _, _, err := reservePinnedPermit(uuid); err == nil {
		t.Fatal("capacity limit evicted a live permit")
	}
	bad := permitResultForRequest(t, first)
	bad.Sequence++
	if resolvePinnedPermit(uuid, conn, bad.DispatchID, &bad) {
		t.Fatal("incorrect sequence accepted")
	}
	awaitPanelPermit(t, waiters[0], false)
	mu.Lock()
	s := pinnedWSSessions[uuid]
	for id, entry := range s.pending {
		entry.expires = time.Now().Add(-time.Second)
		s.pending[id] = entry
	}
	mu.Unlock()
	_, req, waiter, err := reservePinnedPermit(uuid)
	if err != nil || req.Params.(v2.PingFamilyPermitParams).Sequence != pinnedPermitLimit+1 {
		t.Fatalf("expired permit cleanup failed: %v", err)
	}
	for _, ch := range waiters[1:] {
		awaitPanelPermit(t, ch, false)
	}
	result := permitResultForRequest(t, req)
	result.FreshnessTicket = "invalid"
	if resolvePinnedPermit(uuid, conn, result.DispatchID, &result) {
		t.Fatal("malformed ticket accepted")
	}
	awaitPanelPermit(t, waiter, false)
}

func TestPinnedPanelPermitReplacementRacesReservation(t *testing.T) {
	const uuid = "pinned-panel-race"
	for i := 0; i < 100; i++ {
		conn, _ := activePanelPinnedSession(t, uuid)
		start := make(chan struct{})
		var request v2.Request
		var waiter <-chan pinnedPermitOutcome
		var reserveErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, request, waiter, reserveErr = reservePinnedPermit(uuid) }()
		go func() { defer wg.Done(); <-start; SetConnectedClients(uuid, &connection.SafeConn{}) }()
		close(start)
		wg.Wait()
		if reserveErr == nil {
			awaitPanelPermit(t, waiter, false)
			result := permitResultForRequest(t, request)
			if resolvePinnedPermit(uuid, conn, result.DispatchID, &result) {
				t.Fatal("old session delivered a result")
			}
		}
	}
}

func TestPinnedPanelPermitLeaseExpiryWakesAllWaiters(t *testing.T) {
	for _, action := range []string{"reserve", "resolve", "renew"} {
		t.Run(action, func(t *testing.T) {
			uuid := "pinned-panel-expired-" + action
			conn, challenge := activePanelPinnedSession(t, uuid)
			_, req, waiter, err := reservePinnedPermit(uuid)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			s := pinnedWSSessions[uuid]
			s.expires = time.Now().Add(-time.Second)
			pinnedWSSessions[uuid] = s
			mu.Unlock()
			switch action {
			case "reserve":
				if _, _, _, err := reservePinnedPermit(uuid); err == nil {
					t.Fatal("expired session reserved a permit")
				}
			case "resolve":
				result := permitResultForRequest(t, req)
				if resolvePinnedPermit(uuid, conn, result.DispatchID, &result) {
					t.Fatal("expired session resolved a permit")
				}
			case "renew":
				if AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, challenge) {
					t.Fatal("expired session renewed")
				}
			}
			awaitPanelPermit(t, waiter, false)
		})
	}
}
