package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/ws"
	"github.com/gorilla/websocket"
)

type pinnedProbeTestConn struct {
	*pinnedFakeConn
	failWrite bool
}

func (c *pinnedProbeTestConn) WriteJSONContext(ctx context.Context, data interface{}) error {
	if c.failWrite {
		return errors.New("simulated write failure")
	}
	select {
	case c.outbound <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errors.New("closed")
	}
}

func pinnedProbeFrame(t *testing.T, p v2.PingFamilyProbeParams) []byte {
	t.Helper()
	data, err := json.Marshal(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyProbe, Params: p})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func probeTestController(t *testing.T) (*pinnedWSController, *pinnedProbeTestConn, v2.PingFamilyProbeParams) {
	t.Helper()
	c, p := activePinnedController(t)
	conn := &pinnedProbeTestConn{pinnedFakeConn: newPinnedFakeConn(false)}
	c.conn = conn
	c.probeDispatch = c.launchPinnedDispatch
	return c, conn, p
}

func TestPinnedProbeDispatchDisabledByDefault(t *testing.T) {
	c, conn, p := probeTestController(t)
	c.probeDispatch = nil
	issueTestPermit(t, c, &p, 1)
	if handled, err := c.handleProbe(pinnedProbeFrame(t, p)); !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	c.mu.Lock()
	active, pending := c.activeProbes, len(c.tickets)
	c.mu.Unlock()
	if active != 0 || pending != 1 || len(conn.outbound) != 0 {
		t.Fatal("disabled dispatch started work or consumed the permit")
	}
}

func readPinnedResult(t *testing.T, c *pinnedProbeTestConn) v2.PingFamilyResultParams {
	t.Helper()
	select {
	case msg := <-c.outbound:
		req, ok := msg.(v2.Request)
		if !ok || req.Method != v2.MethodAgentPingFamilyResult || req.JSONRPC != v2.Version || req.ID != nil {
			t.Fatalf("result frame = %+v", msg)
		}
		result, ok := req.Params.(v2.PingFamilyResultParams)
		if !ok {
			t.Fatalf("result params = %T", req.Params)
		}
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pinned result")
		return v2.PingFamilyResultParams{}
	}
}

func TestPinnedProbeWireExecutesOnlyRequestedFamilyAndTicketOnce(t *testing.T) {
	c, conn, p := probeTestController(t)
	var dials atomic.Int32
	c.probeRunner.resolver = pinnedResolver{
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return testAddresses("192.0.2.1", "2001:db8::1"), nil
		},
		dial: func(_ context.Context, network, target string) (net.Conn, error) {
			dials.Add(1)
			if network != "tcp6" || target != "[2001:db8::1]:80" {
				t.Errorf("wrong-family dial %q %q", network, target)
			}
			return nil, errors.New("connection refused")
		},
	}
	issueTestPermit(t, c, &p, 1)
	frame := pinnedProbeFrame(t, p)
	if handled, err := c.handleProbe(frame); !handled || err != nil {
		t.Fatalf("probe handled=%v err=%v", handled, err)
	}
	result := readPinnedResult(t, conn)
	if result.TaskID != p.TaskID || result.ConfigGeneration != p.ConfigGeneration || result.Instance != p.Instance || result.Session != p.Session || result.DispatchID != p.DispatchID ||
		result.Role != "primary" || result.PingType != "tcp" || result.Family != "ipv6" || result.Value == nil || *result.Value != -1 || result.Diagnostic != "" {
		t.Fatalf("result = %+v", result)
	}
	if handled, _ := c.handleProbe(frame); !handled || dials.Load() != 1 {
		t.Fatal("replayed ticket started another probe")
	}
	if processV2Event(nil, v2.MethodAgentPingFamilyProbe, p, "") {
		t.Fatal("generic POST event accepted pinned probe")
	}
}

func TestPinnedProbeWireNonProbeIsDiagnostic(t *testing.T) {
	c, conn, p := probeTestController(t)
	c.probeRunner.resolver.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return testAddresses("192.0.2.1"), nil
	}
	c.probeRunner.resolver.dial = func(context.Context, string, string) (net.Conn, error) {
		t.Error("no-family result reached network")
		return nil, errors.New("unexpected dial")
	}
	issueTestPermit(t, c, &p, 1)
	if handled, _ := c.handleProbe(pinnedProbeFrame(t, p)); !handled {
		t.Fatal("probe not handled")
	}
	result := readPinnedResult(t, conn)
	if result.Diagnostic != "no_family_address" || result.Value != nil || result.ValueUS != nil || result.Family != "" {
		t.Fatalf("non-probe became loss: %+v", result)
	}
}

func TestPinnedProbeWireRejectsMalformedAndExpired(t *testing.T) {
	c, conn, p := probeTestController(t)
	var lookups atomic.Int32
	c.probeRunner.resolver.lookup = func(context.Context, string) ([]netip.Addr, error) {
		lookups.Add(1)
		return nil, nil
	}
	issueTestPermit(t, c, &p, 1)
	raw := string(pinnedProbeFrame(t, p))
	for _, bad := range []string{
		strings.Replace(raw, `"jsonrpc":"2.0"`, `"jsonrpc":"1.0"`, 1),
		strings.Replace(raw, `"params":{`, `"id":"unexpected","params":{`, 1),
		strings.Replace(raw, `"params":{`, `"params":{"unexpected":1,`, 1),
		strings.Replace(raw, p.Session, strings.Repeat("a", 32), 1),
	} {
		if handled, err := c.handleProbe([]byte(bad)); !handled || err != nil {
			t.Fatalf("malformed handled=%v err=%v", handled, err)
		}
	}
	c.mu.Lock()
	ticket := c.tickets[p.FreshnessTicket]
	ticket.expires = time.Now().Add(-time.Second)
	c.tickets[p.FreshnessTicket] = ticket
	c.mu.Unlock()
	if handled, _ := c.handleProbe([]byte(raw)); !handled || lookups.Load() != 0 {
		t.Fatal("expired ticket started a probe")
	}
	select {
	case msg := <-conn.outbound:
		t.Fatalf("invalid request produced output: %+v", msg)
	default:
	}
}

func TestPinnedProbeWireRetirementCancelsWithoutLoss(t *testing.T) {
	c, conn, p := probeTestController(t)
	started, stopped := make(chan struct{}), make(chan struct{})
	c.probeRunner.resolver.lookup = func(ctx context.Context, _ string) ([]netip.Addr, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}
	issueTestPermit(t, c, &p, 1)
	c.handleProbe(pinnedProbeFrame(t, p))
	awaitPinnedSignal(t, started)
	c.clear()
	awaitPinnedSignal(t, stopped)
	select {
	case msg := <-conn.outbound:
		t.Fatalf("retired probe reported result: %+v", msg)
	default:
	}
}

func TestPinnedProbeResultWriteFailureRetiresSocket(t *testing.T) {
	c, conn, p := probeTestController(t)
	conn.failWrite = true
	c.probeRunner.resolver.lookup = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("dns") }
	issueTestPermit(t, c, &p, 1)
	c.handleProbe(pinnedProbeFrame(t, p))
	awaitPinnedSignal(t, conn.closed)
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		stopped := c.stopped
		c.mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("write failure did not retire session")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPinnedProbePermitAndResultOverLoopback(t *testing.T) {
	peerChan := make(chan *websocket.Conn, 1)
	finished := make(chan struct{})
	stopServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer peer.Close()
		peerChan <- peer
		<-stopServer
	}))
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	peer := <-peerChan
	conn := ws.NewSafeConn(raw)
	c, p := activePinnedController(t)
	c.conn = conn
	c.probeDispatch = c.launchPinnedDispatch
	c.probeRunner.resolver.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return testAddresses("192.0.2.1"), nil
	}
	t.Cleanup(func() {
		c.clear()
		_ = conn.Close()
		_ = peer.Close()
		close(stopServer)
		server.Close()
		<-finished
	})
	readDone := make(chan error, 1)
	go func() {
		for range 2 {
			_, data, err := conn.ReadMessageLimited(8192)
			if err != nil {
				readDone <- err
				return
			}
			handled, err := c.handleProbePermit(data)
			if !handled && err == nil {
				handled, err = c.handleProbe(data)
			}
			if err != nil || !handled {
				readDone <- errors.New("unexpected loopback frame")
				return
			}
		}
		readDone <- nil
	}()
	if err := peer.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyPermit, ID: p.DispatchID, Params: permitFor(p, 1)}); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	var permit struct {
		ID     string                    `json:"id"`
		Result v2.PingFamilyPermitResult `json:"result"`
	}
	if err := peer.ReadJSON(&permit); err != nil || permit.ID != p.DispatchID || !pinnedValidID(permit.Result.FreshnessTicket) {
		t.Fatalf("permit=%+v err=%v", permit, err)
	}
	p.FreshnessTicket = permit.Result.FreshnessTicket
	if err := peer.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyProbe, Params: p}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Method string                    `json:"method"`
		Params v2.PingFamilyResultParams `json:"params"`
	}
	if err := peer.ReadJSON(&result); err != nil || result.Method != v2.MethodAgentPingFamilyResult || result.Params.Diagnostic != "no_family_address" || result.Params.Value != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
}
