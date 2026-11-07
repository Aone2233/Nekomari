package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/ws"
	"github.com/gorilla/websocket"
	"golang.org/x/net/dns/dnsmessage"
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
	enablePinnedProbeExecution(c)
	return c, conn, p
}

func TestPinnedProbeDispatchDisabledByDefault(t *testing.T) {
	c, conn, p := probeTestController(t)
	issueTestPermit(t, c, &p, 1)
	c.probeDispatch = nil
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

// This uses the actual production WebSocket entrypoint and real IPv6 loopback,
// not the injected dispatcher/resolver used by the multi-Agent fixtures.
func TestProductionWebSocketRunsOnlyPermittedFamilyProbe(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
			accepted <- struct{}{}
		}
	}()

	upgraded := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			upgraded <- conn
		}
	}))
	defer server.Close()
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	panel := <-upgraded
	defer panel.Close()
	done := make(chan struct{})
	go handleWebSocketMessages(ws.NewSafeConn(raw), done)
	defer func() { _ = panel.Close(); waitPinnedReader(t, done) }()
	if err := panel.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var begin struct {
		ID     string                     `json:"id"`
		Method string                     `json:"method"`
		Params v2.PingFamilySessionParams `json:"params"`
	}
	if err := panel.ReadJSON(&begin); err != nil || begin.Method != v2.MethodAgentPingFamilySession || begin.Params.Action != "begin" {
		t.Fatalf("production begin = %+v, err = %v", begin, err)
	}
	const challenge = "0123456789abcdef0123456789abcdef"
	if err := panel.WriteJSON(v2.Response{JSONRPC: v2.Version, ID: begin.ID, Result: v2.PingFamilySessionResult{Challenge: challenge}}); err != nil {
		t.Fatal(err)
	}
	var ack struct {
		ID     string                     `json:"id"`
		Params v2.PingFamilySessionParams `json:"params"`
	}
	if err := panel.ReadJSON(&ack); err != nil || ack.Params.Action != "ack" || ack.Params.Challenge != challenge {
		t.Fatalf("production ack = %+v, err = %v", ack, err)
	}
	if err := panel.WriteJSON(v2.Response{JSONRPC: v2.Version, ID: ack.ID, Result: v2.PingFamilySessionResult{Challenge: challenge}}); err != nil {
		t.Fatal(err)
	}
	p := pinnedWireFixture()
	p.Instance, p.Session = begin.Params.Instance, challenge
	p.Target = net.JoinHostPort("::1", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	if err := panel.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyPermit, ID: p.DispatchID, Params: permitFor(p, 1)}); err != nil {
		t.Fatal(err)
	}
	var permit struct {
		ID     string                    `json:"id"`
		Result v2.PingFamilyPermitResult `json:"result"`
		Error  *v2.RPCError              `json:"error"`
	}
	if err := panel.ReadJSON(&permit); err != nil || permit.Error != nil || permit.ID != p.DispatchID || !pinnedValidID(permit.Result.FreshnessTicket) {
		t.Fatalf("production permit = %+v, err = %v", permit, err)
	}
	p.FreshnessTicket = permit.Result.FreshnessTicket
	request := v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyProbe, Params: p}
	if err := panel.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Method string                    `json:"method"`
		Params v2.PingFamilyResultParams `json:"params"`
	}
	if err := panel.ReadJSON(&result); err != nil {
		t.Fatal(err)
	}
	if result.Method != v2.MethodAgentPingFamilyResult || result.Params.DispatchID != p.DispatchID || result.Params.Family != pinnedIPv6 ||
		result.Params.Value == nil || *result.Params.Value < 0 || result.Params.Diagnostic != "" {
		t.Fatalf("unexpected production result: %+v", result)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("real IPv6 listener was not reached")
	}
	if err := panel.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
		t.Fatal("production entrypoint replayed a consumed permit")
	case <-time.After(150 * time.Millisecond):
	}
}

// Exercise the unmodified production entrypoint and resolver with an actual
// DNS exchange; no test hook substitutes lookup or dial functions.
func TestProductionWebSocketResolvesHostnameUsingNodeDNS(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
			accepted <- struct{}{}
		}
	}()
	dns, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	previous := dnsresolver.CustomDNSServer
	dnsresolver.SetCustomDNSServer(dns.LocalAddr().String())
	dnsDone := make(chan struct{})
	questions := make(chan dnsmessage.Question, 8)
	go func() {
		defer close(dnsDone)
		buf := make([]byte, 4096)
		for {
			n, peer, err := dns.ReadFrom(buf)
			if err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(buf[:n]) != nil || len(request.Questions) != 1 {
				continue
			}
			question := request.Questions[0]
			questions <- question
			answer := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: request.Questions}
			if question.Type == dnsmessage.TypeAAAA {
				answer.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 1},
					Body: &dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("::1").As16()}}}
			}
			if wire, err := answer.Pack(); err == nil {
				_, _ = dns.WriteTo(wire, peer)
			}
		}
	}()
	defer func() {
		_ = dns.Close()
		<-dnsDone
		dnsresolver.CustomDNSServer = previous
	}()

	upgraded := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			upgraded <- conn
		}
	}))
	defer server.Close()
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	panel := <-upgraded
	defer panel.Close()
	done := make(chan struct{})
	go handleWebSocketMessages(ws.NewSafeConn(raw), done)
	defer func() { _ = panel.Close(); waitPinnedReader(t, done) }()
	if err := panel.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var begin struct {
		ID     string                     `json:"id"`
		Params v2.PingFamilySessionParams `json:"params"`
	}
	if err := panel.ReadJSON(&begin); err != nil || begin.Params.Action != "begin" {
		t.Fatalf("production begin = %+v, err = %v", begin, err)
	}
	const challenge = "0123456789abcdef0123456789abcdef"
	if err := panel.WriteJSON(v2.Response{JSONRPC: v2.Version, ID: begin.ID, Result: v2.PingFamilySessionResult{Challenge: challenge}}); err != nil {
		t.Fatal(err)
	}
	var ack struct {
		ID     string                     `json:"id"`
		Params v2.PingFamilySessionParams `json:"params"`
	}
	if err := panel.ReadJSON(&ack); err != nil || ack.Params.Action != "ack" || ack.Params.Challenge != challenge {
		t.Fatalf("production ack = %+v, err = %v", ack, err)
	}
	if err := panel.WriteJSON(v2.Response{JSONRPC: v2.Version, ID: ack.ID, Result: v2.PingFamilySessionResult{Challenge: challenge}}); err != nil {
		t.Fatal(err)
	}
	p := pinnedWireFixture()
	p.Instance, p.Session = begin.Params.Instance, challenge
	p.Target = net.JoinHostPort("probe.example.invalid.", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	if err := panel.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyPermit, ID: p.DispatchID, Params: permitFor(p, 1)}); err != nil {
		t.Fatal(err)
	}
	var permit struct {
		ID     string                    `json:"id"`
		Result v2.PingFamilyPermitResult `json:"result"`
		Error  *v2.RPCError              `json:"error"`
	}
	if err := panel.ReadJSON(&permit); err != nil || permit.Error != nil || permit.ID != p.DispatchID || !pinnedValidID(permit.Result.FreshnessTicket) {
		t.Fatalf("production permit = %+v, err = %v", permit, err)
	}
	p.FreshnessTicket = permit.Result.FreshnessTicket
	if err := panel.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyProbe, Params: p}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Method string                    `json:"method"`
		Params v2.PingFamilyResultParams `json:"params"`
	}
	if err := panel.ReadJSON(&result); err != nil || result.Method != v2.MethodAgentPingFamilyResult || result.Params.Family != pinnedIPv6 ||
		result.Params.Value == nil || *result.Params.Value < 0 || result.Params.Diagnostic != "" {
		t.Fatalf("production hostname probe = %+v, err = %v", result, err)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("resolved IPv6 listener was not reached")
	}
	select {
	case question := <-questions:
		if question.Name.String() != "probe.example.invalid." || question.Type != dnsmessage.TypeAAAA {
			t.Fatalf("unexpected DNS query: %+v", question)
		}
	default:
		t.Fatal("production resolver did not query the configured DNS server")
	}
	select {
	case question := <-questions:
		if question.Type != dnsmessage.TypeAAAA {
			t.Fatalf("production IPv6 pin queried another address family: %+v", question)
		}
	default:
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

// Exercise the actual socket registry and reader on the Agent's report-write
// failure path. A started probe must be canceled and its unused ticket must
// not become usable by a replacement controller.
func TestPinnedProductionReaderReportWriteFailureRetiresProbe(t *testing.T) {
	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		peers <- peer
	}))
	defer server.Close()
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	peer := <-peers
	defer peer.Close()
	socket := ws.NewSafeConn(raw)
	done := make(chan struct{})
	controllers := make(chan *pinnedWSController, 1)
	started, canceled := make(chan struct{}), make(chan struct{})
	go handleWebSocketMessagesWithController(socket, done, time.Hour, newPinnedWSHandshake(), func(c *pinnedWSController) {
		c.probeDispatch = c.launchPinnedDispatch
		c.probeRunner.resolver.lookup = func(ctx context.Context, _ string) ([]netip.Addr, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return nil, ctx.Err()
		}
		controllers <- c
	})
	t.Cleanup(func() {
		socket.Abort()
		waitPinnedReader(t, done)
	})
	controller := <-controllers
	read := func(out any) {
		t.Helper()
		if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := peer.ReadJSON(out); err != nil {
			t.Fatal(err)
		}
	}
	var begin v2.Request
	read(&begin)
	var handshake v2.PingFamilySessionParams
	if err := v2.BindParams(begin.Params, &handshake); err != nil {
		t.Fatal(err)
	}
	challenge := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := peer.WriteJSON(v2.Response{JSONRPC: v2.Version, ID: begin.ID, Result: v2.PingFamilySessionResult{Challenge: challenge}}); err != nil {
		t.Fatal(err)
	}
	var ack v2.Request
	read(&ack)
	if err := peer.WriteJSON(v2.Response{JSONRPC: v2.Version, ID: ack.ID, Result: v2.PingFamilySessionResult{Challenge: challenge}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, session, _, _, active := currentPinnedWSSession(socket, time.Now())
		if active && session == challenge {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("production socket registry did not observe the active session")
		}
		time.Sleep(time.Millisecond)
	}
	p := pinnedWireFixture()
	p.Instance, p.Session = handshake.Instance, challenge
	permit := func(sequence uint64) string {
		t.Helper()
		if err := peer.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyPermit, ID: p.DispatchID, Params: permitFor(p, sequence)}); err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Result v2.PingFamilyPermitResult `json:"result"`
			Error  *v2.RPCError              `json:"error"`
		}
		read(&reply)
		if reply.Error != nil || !pinnedValidID(reply.Result.FreshnessTicket) {
			t.Fatalf("permit response = %+v", reply)
		}
		return reply.Result.FreshnessTicket
	}
	p.FreshnessTicket = permit(1)
	if err := peer.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilyProbe, Params: p}); err != nil {
		t.Fatal(err)
	}
	awaitPinnedSignal(t, started)
	p.DispatchID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	p.FreshnessTicket = permit(2)
	if err := socket.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		t.Fatal(err)
	}
	if err := socket.WriteMessage(websocket.TextMessage, []byte("report")); err == nil {
		t.Fatal("expected the production report writer to fail after close")
	}
	// EstablishWebSocketConnection uses this exact failure handler before it
	// drops the old socket and starts a replacement.
	abortPinnedWSSocket(socket)
	awaitPinnedSignal(t, canceled)
	if _, _, _, _, active := currentPinnedWSSession(socket, time.Now()); active {
		t.Fatal("write failure retained the old session")
	}
	if err := controller.launchProbe(p, func(context.Context) { t.Error("retired ticket launched") }); err == nil {
		t.Fatal("unused ticket survived transport failure")
	}
	replacement, other := activePinnedController(t)
	if err := replacement.launchProbe(p, func(context.Context) { t.Error("old ticket launched on replacement") }); err == nil {
		t.Fatalf("ticket crossed sessions: replacement=%+v", other)
	}
}
