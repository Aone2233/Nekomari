package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

func activePinnedController(t *testing.T) (*pinnedWSController, v2.PingFamilyProbeParams) {
	t.Helper()
	p := pinnedWireFixture()
	h := &pinnedWSHandshake{instance: p.Instance, challenge: p.Session, active: true, validUntil: time.Now().Add(time.Minute)}
	ctx, cancel := context.WithCancel(context.Background())
	c := &pinnedWSController{handshake: h, conn: newPinnedFakeConn(false), sessionContext: ctx, sessionCancel: cancel}
	t.Cleanup(c.clear)
	return c, p
}

func permitFor(p v2.PingFamilyProbeParams, sequence uint64) v2.PingFamilyPermitParams {
	return v2.PingFamilyPermitParams{Instance: p.Instance, Session: p.Session, DispatchID: p.DispatchID, Sequence: sequence}
}

func issueTestPermit(t *testing.T, c *pinnedWSController, p *v2.PingFamilyProbeParams, sequence uint64) {
	t.Helper()
	permit, err := c.issueProbePermit(permitFor(*p, sequence))
	if err != nil || !pinnedValidID(permit.FreshnessTicket) || permit.DispatchID != p.DispatchID || permit.Sequence != sequence {
		t.Fatalf("permit = %+v, err %v", permit, err)
	}
	p.FreshnessTicket = permit.FreshnessTicket
}

func awaitPinnedSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pinned probe")
	}
}

func TestPinnedPermitSingleUseAndRetirementCancels(t *testing.T) {
	c, p := activePinnedController(t)
	issueTestPermit(t, c, &p, 1)
	started, canceled := make(chan struct{}), make(chan struct{})
	if err := c.launchProbe(p, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(canceled)
	}); err != nil {
		t.Fatal(err)
	}
	awaitPinnedSignal(t, started)
	if err := c.launchProbe(p, func(context.Context) { t.Error("replayed ticket started") }); err == nil {
		t.Fatal("replayed ticket was accepted")
	}
	c.clear()
	awaitPinnedSignal(t, canceled)
	if _, err := c.issueProbePermit(permitFor(p, 2)); err == nil {
		t.Fatal("retired session issued permit")
	}
}

func TestPinnedPermitWebSocketResponseDoesNotStartProbe(t *testing.T) {
	c, p := activePinnedController(t)
	requestID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	data, err := json.Marshal(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyPermit, ID: requestID, Params: permitFor(p, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := c.handleProbePermit(data); !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	fake := c.conn.(*pinnedFakeConn)
	select {
	case message := <-fake.outbound:
		response, ok := message.(v2.Response)
		if !ok || response.ID != requestID || response.Error != nil {
			t.Fatalf("response = %+v", message)
		}
		permit, ok := response.Result.(v2.PingFamilyPermitResult)
		if !ok || permit.DispatchID != p.DispatchID || permit.Sequence != 1 || !pinnedValidID(permit.FreshnessTicket) {
			t.Fatalf("permit response = %+v", response.Result)
		}
	case <-time.After(time.Second):
		t.Fatal("permit response not sent")
	}
	if handled, err := c.handleProbePermit(data); !handled || err != nil {
		t.Fatalf("replayed request handled=%v err=%v", handled, err)
	}
	select {
	case message := <-fake.outbound:
		response, ok := message.(v2.Response)
		if !ok || response.Error == nil || response.Result != nil {
			t.Fatalf("replay did not produce error: %+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("replayed request did not produce a response")
	}
	c.mu.Lock()
	active := c.activeProbes
	c.mu.Unlock()
	if active != 0 {
		t.Fatal("permit issuance started a probe")
	}
	if processV2Event(nil, v2.MethodAgentPingFamilyPermit, permitFor(p, 2), "") {
		t.Fatal("POST event accepted pinned permit")
	}
}

func TestPinnedPermitInvalidOrUnrelatedFrames(t *testing.T) {
	c, p := activePinnedController(t)
	for _, input := range []string{
		`{"jsonrpc":"2.0","method":"agent.pingFamily.permit.v1","params":{}}`,
		`{"jsonrpc":"2.0","method":"agent.pingFamily.permit.v1","id":"bad","params":{}}`,
		`{"jsonrpc":"2.0","method":"agent.pingFamily.permit.v1","id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","params":null}`,
		`{"jsonrpc":"1.0","method":"agent.pingFamily.permit.v1","id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","params":{}}`,
	} {
		if handled, err := c.handleProbePermit([]byte(input)); !handled || err != nil {
			t.Fatalf("handled=%v err=%v for %s", handled, err, input)
		}
	}
	if handled, err := c.handleProbePermit([]byte(`{"jsonrpc":"2.0","method":"agent.message"}`)); handled || err != nil {
		t.Fatal("unrelated event consumed")
	}
	if len(c.conn.(*pinnedFakeConn).outbound) != 0 {
		t.Fatal("malformed frame produced a response")
	}
	issueTestPermit(t, c, &p, 1)
}

func TestPinnedPermitRejectsProbePayload(t *testing.T) {
	c, p := activePinnedController(t)
	request := v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyPermit,
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Params: map[string]any{
			"instance": p.Instance, "session": p.Session, "dispatch_id": p.DispatchID,
			"sequence": 1, "target": "target.test:80", "family": "ipv6",
		}}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := c.handleProbePermit(data); !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	select {
	case message := <-c.conn.(*pinnedFakeConn).outbound:
		response, ok := message.(v2.Response)
		if !ok || response.Error == nil || response.Result != nil {
			t.Fatalf("target-bearing permit accepted: %+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("invalid permit response missing")
	}
	issueTestPermit(t, c, &p, 1)
}

func TestPinnedPermitRejectsStaleRequestAndWrongDispatch(t *testing.T) {
	c, p := activePinnedController(t)
	issueTestPermit(t, c, &p, 4)
	for _, seq := range []uint64{0, 1, 4} {
		if _, err := c.issueProbePermit(permitFor(p, seq)); err == nil {
			t.Fatalf("stale sequence %d accepted", seq)
		}
	}
	wrong := p
	wrong.DispatchID = "55555555555555555555555555555555"
	if err := c.launchProbe(wrong, func(context.Context) { t.Error("wrong dispatch started") }); err == nil {
		t.Fatal("wrong dispatch accepted")
	}
	started := make(chan struct{})
	if err := c.launchProbe(p, func(context.Context) { close(started) }); err != nil {
		t.Fatal("a different dispatch consumed the ticket")
	}
	awaitPinnedSignal(t, started)
	issueTestPermit(t, c, &p, 5)
	other, _ := activePinnedController(t)
	if err := other.launchProbe(p, func(context.Context) { t.Error("foreign session started") }); err == nil {
		t.Fatal("ticket crossed controller boundary")
	}
}

func TestPinnedPermitBoundsAndExpiry(t *testing.T) {
	c, p := activePinnedController(t)
	for i := uint64(1); i <= pinnedMaxTickets; i++ {
		issueTestPermit(t, c, &p, i)
	}
	if _, err := c.issueProbePermit(permitFor(p, pinnedMaxTickets+1)); err == nil {
		t.Fatal("unexpired tickets evicted at capacity")
	}
	c.mu.Lock()
	for token, ticket := range c.tickets {
		ticket.expires = time.Now().Add(-time.Second)
		c.tickets[token] = ticket
	}
	c.mu.Unlock()
	if err := c.launchProbe(p, func(context.Context) { t.Error("expired ticket started") }); err == nil {
		t.Fatal("expired permit accepted")
	}
	if _, err := c.issueProbePermit(permitFor(p, pinnedMaxTickets+1)); err == nil {
		t.Fatal("capacity failure did not consume its sequence")
	}
	issueTestPermit(t, c, &p, pinnedMaxTickets+2)
	started, canceled := make(chan struct{}), make(chan struct{})
	if err := c.launchProbe(p, func(ctx context.Context) { close(started); <-ctx.Done(); close(canceled) }); err != nil {
		t.Fatal(err)
	}
	awaitPinnedSignal(t, started)
	c.mu.Lock()
	c.handshake.validUntil = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if _, err := c.issueProbePermit(permitFor(p, pinnedMaxTickets+3)); err == nil {
		t.Fatal("expired session issued permit")
	}
	awaitPinnedSignal(t, canceled)
}

func TestPinnedPermitActiveLimitFailsClosed(t *testing.T) {
	c, p := activePinnedController(t)
	c.mu.Lock()
	c.activeProbes = pinnedMaxActiveProbes
	c.mu.Unlock()
	issueTestPermit(t, c, &p, 1)
	if err := c.launchProbe(p, func(context.Context) { t.Error("capacity-limited probe started") }); err == nil {
		t.Fatal("active limit accepted a probe")
	}
	c.mu.Lock()
	c.activeProbes = 0
	c.mu.Unlock()
	if err := c.launchProbe(p, func(context.Context) { t.Error("consumed probe started") }); err == nil {
		t.Fatal("capacity rejection left ticket reusable")
	}
}

func TestPinnedPermitRejectsInvalidIdentityAndSequence(t *testing.T) {
	c, p := activePinnedController(t)
	for name, edit := range map[string]func(*v2.PingFamilyPermitParams){
		"instance":    func(v *v2.PingFamilyPermitParams) { v.Instance = strings.Repeat("a", 32) },
		"session":     func(v *v2.PingFamilyPermitParams) { v.Session = strings.Repeat("b", 32) },
		"dispatch ID": func(v *v2.PingFamilyPermitParams) { v.DispatchID = "invalid" },
		"sequence":    func(v *v2.PingFamilyPermitParams) { v.Sequence = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			permit := permitFor(p, 1)
			edit(&permit)
			if _, err := c.issueProbePermit(permit); err == nil {
				t.Fatal("invalid permit request accepted")
			}
		})
	}
	issueTestPermit(t, c, &p, 1)
}

func TestPinnedPermitLateRenewalCannotReviveExpiredSession(t *testing.T) {
	c, p := activePinnedController(t)
	issueTestPermit(t, c, &p, 1)
	renew := c.handshake.request("ack")
	c.handshake.validUntil = time.Now().Add(-time.Second)
	if handled, err := c.handle(pinnedResponse(t, renew.ID, p.Session, nil)); handled || err != nil {
		t.Fatalf("expired renewal handled=%v err=%v", handled, err)
	}
	if _, _, _, _, ok := c.current(time.Now()); ok {
		t.Fatal("late response revived expired session")
	}
	if err := c.launchProbe(p, func(context.Context) { t.Error("expired session started probe") }); err == nil {
		t.Fatal("late renewal preserved old ticket")
	}
}

func TestPinnedPermitRechecksLeaseAfterWaitingForLock(t *testing.T) {
	c, p := activePinnedController(t)
	c.mu.Lock()
	c.handshake.validUntil = time.Now().Add(-time.Second)
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := c.issueProbePermit(permitFor(p, 1))
		done <- err
	}()
	awaitPinnedSignal(t, started)
	c.mu.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired session issued permit after lock wait")
		}
	case <-time.After(time.Second):
		t.Fatal("permit request remained blocked")
	}
}

func TestPinnedPermitConcurrentConsumptionAndRetirement(t *testing.T) {
	for i := 0; i < 100; i++ {
		c, p := activePinnedController(t)
		issueTestPermit(t, c, &p, 1)
		start := make(chan struct{})
		var accepted, ran atomic.Int32
		var wg sync.WaitGroup
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if c.launchProbe(p, func(ctx context.Context) { ran.Add(1); <-ctx.Done() }) == nil {
					accepted.Add(1)
				}
			}()
		}
		wg.Add(1)
		go func() { defer wg.Done(); <-start; c.clear() }()
		close(start)
		wg.Wait()
		if accepted.Load() > 1 || ran.Load() > 1 {
			t.Fatalf("permit replay: accepted=%d ran=%d", accepted.Load(), ran.Load())
		}
		if err := c.launchProbe(p, func(context.Context) { t.Error("retired session started") }); err == nil {
			t.Fatal("retired session accepted a probe")
		}
	}
}
