package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/ws"
	"github.com/gorilla/websocket"
)

func pinnedResponse(t *testing.T, id any, challenge string, rpcErr *v2.RPCError) []byte {
	t.Helper()
	b, err := json.Marshal(v2.Response{JSONRPC: v2.Version, ID: id, Result: v2.PingFamilySessionResult{Challenge: challenge}, Error: rpcErr})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPinnedWSClientHandshakeRequiresMatchingResponses(t *testing.T) {
	h := newPinnedWSHandshake()
	if h == nil || !pinnedValidID(h.instance) {
		t.Fatal("failed to initialize process identity")
	}
	begin := h.begin()
	if begin.Method != v2.MethodAgentPingFamilySession || begin.Params.(v2.PingFamilySessionParams).Action != "begin" {
		t.Fatalf("begin = %+v", begin)
	}
	challenge := "0123456789abcdef0123456789abcdef"
	if _, handled := h.handle(pinnedResponse(t, "old-request", challenge, nil)); handled {
		t.Fatal("accepted response for a different request")
	}
	ack, handled := h.handle(pinnedResponse(t, begin.ID, challenge, nil))
	if !handled || ack == nil || h.active || ack.Params.(v2.PingFamilySessionParams).Action != "ack" || ack.Params.(v2.PingFamilySessionParams).Challenge != challenge {
		t.Fatalf("begin response produced ack=%+v handled=%v", ack, handled)
	}
	if _, handled := h.handle(pinnedResponse(t, begin.ID, challenge, nil)); handled {
		t.Fatal("replayed begin response changed pending ack")
	}
	if next, handled := h.handle(pinnedResponse(t, ack.ID, challenge, nil)); !handled || next != nil || h.challenge != challenge || !h.active {
		t.Fatal("matching ack did not complete handshake")
	}
	renew := h.renew()
	if renew.Params.(v2.PingFamilySessionParams).Action != "ack" {
		t.Fatal("active session did not renew via ack")
	}
	if _, handled := h.handle(pinnedResponse(t, renew.ID, "fedcba9876543210fedcba9876543210", nil)); !handled || h.challenge != "" || h.active {
		t.Fatal("mismatched ack kept a session")
	}
	if next := h.renew(); next.Params.(v2.PingFamilySessionParams).Action != "begin" {
		t.Fatal("failed ack did not restart handshake")
	}
}

func TestPinnedWSClientHandshakeFailsClosedOnOldPanel(t *testing.T) {
	h := newPinnedWSHandshake()
	begin := h.begin()
	if _, handled := h.handle(pinnedResponse(t, begin.ID, "", &v2.RPCError{Code: -32601, Message: "method not found"})); !handled || h.challenge != "" || !h.unsupported {
		t.Fatal("old panel error created a capability")
	}
	begin = h.begin()
	if _, handled := h.handle(pinnedResponse(t, begin.ID, "invalid", nil)); !handled || h.challenge != "" {
		t.Fatal("invalid server challenge created a capability")
	}
}

type pinnedFakeConn struct {
	inbound    chan []byte
	outbound   chan any
	closed     chan struct{}
	writing    chan struct{}
	once       sync.Once
	blockWrite bool
}

func newPinnedFakeConn(blockWrite bool) *pinnedFakeConn {
	return &pinnedFakeConn{inbound: make(chan []byte, 8), outbound: make(chan any, 8), closed: make(chan struct{}), writing: make(chan struct{}, 1), blockWrite: blockWrite}
}

func (c *pinnedFakeConn) ReadMessageLimited(int64) (int, []byte, error) {
	select {
	case data := <-c.inbound:
		return 1, data, nil
	case <-c.closed:
		return 0, nil, errors.New("closed")
	}
}

func (c *pinnedFakeConn) WriteJSON(data interface{}) error {
	select {
	case c.writing <- struct{}{}:
	default:
	}
	if c.blockWrite {
		<-c.closed
		return errors.New("closed")
	}
	select {
	case c.outbound <- data:
		return nil
	case <-c.closed:
		return errors.New("closed")
	}
}

func (c *pinnedFakeConn) WriteJSONContextWait(ctx context.Context, data interface{}) error {
	select {
	case c.writing <- struct{}{}:
	default:
	}
	if !c.blockWrite {
		select {
		case c.outbound <- data:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closed:
			return errors.New("closed")
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errors.New("closed")
	}
}

func (c *pinnedFakeConn) Abort() { c.once.Do(func() { close(c.closed) }) }

func takePinnedRequest(t *testing.T, c *pinnedFakeConn) v2.Request {
	t.Helper()
	select {
	case msg := <-c.outbound:
		req, ok := msg.(v2.Request)
		if !ok {
			t.Fatalf("unexpected handshake message %T", msg)
		}
		return req
	case <-time.After(time.Second):
		t.Fatal("handshake request not sent")
		return v2.Request{}
	}
}

func waitPinnedReader(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WebSocket reader did not stop")
	}
}

func TestPinnedWSReaderHandshakeRenewAndDisconnect(t *testing.T) {
	c := newPinnedFakeConn(false)
	h := newPinnedWSHandshake()
	done := make(chan struct{})
	go handleWebSocketMessagesWithHandshake(c, done, 20*time.Millisecond, h)
	begin := takePinnedRequest(t, c)
	challenge := "0123456789abcdef0123456789abcdef"
	c.inbound <- pinnedResponse(t, begin.ID, challenge, nil)
	ack := takePinnedRequest(t, c)
	if ack.Params.(v2.PingFamilySessionParams).Action != "ack" {
		t.Fatalf("expected ack, got %+v", ack)
	}
	c.inbound <- pinnedResponse(t, ack.ID, challenge, nil)
	renew := takePinnedRequest(t, c)
	if renew.Params.(v2.PingFamilySessionParams).Action != "ack" {
		t.Fatalf("expected renewal, got %+v", renew)
	}
	c.inbound <- pinnedResponse(t, renew.ID, challenge, nil)
	c.Abort()
	waitPinnedReader(t, done)
	if h.active || h.challenge != "" || h.pendingID != "" {
		t.Fatalf("disconnect retained session: %+v", h)
	}
}

func TestPinnedWSReaderDoesNotSpamOldPanel(t *testing.T) {
	c := newPinnedFakeConn(false)
	h := newPinnedWSHandshake()
	done := make(chan struct{})
	go handleWebSocketMessagesWithHandshake(c, done, 10*time.Millisecond, h)
	begin := takePinnedRequest(t, c)
	c.inbound <- pinnedResponse(t, begin.ID, "", &v2.RPCError{Code: -32601})
	time.Sleep(50 * time.Millisecond)
	select {
	case req := <-c.outbound:
		t.Fatalf("old panel received repeated handshake: %+v", req)
	default:
	}
	c.Abort()
	waitPinnedReader(t, done)
}

func TestPinnedWSRetirementHintCancelsWorkAndStartsFreshHandshake(t *testing.T) {
	controller, params := activePinnedController(t)
	conn := newPinnedFakeConn(false)
	controller.conn = conn
	_, _, _, canceled, ok := controller.current(time.Now())
	if !ok {
		t.Fatal("fixture session is not active")
	}
	hint := []byte(`{"jsonrpc":"2.0","method":"agent.pingFamily.retired.v1"}`)
	if handled, err := controller.handleRetirementHint(hint); !handled || err != nil {
		t.Fatalf("hint handled=%v err=%v", handled, err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("retirement hint did not cancel old work")
	}
	begin := takePinnedRequest(t, conn)
	if begin.Method != v2.MethodAgentPingFamilySession || begin.Params.(v2.PingFamilySessionParams).Action != "begin" {
		t.Fatalf("hint did not start a fresh handshake: %+v", begin)
	}
	if _, _, _, _, ok := controller.current(time.Now()); ok {
		t.Fatal("hint alone restored capability")
	}
	if _, err := controller.issueProbePermit(permitFor(params, 1)); err == nil {
		t.Fatal("old permit request survived retirement")
	}
	challenge := "fedcba9876543210fedcba9876543210"
	if handled, err := controller.handle(pinnedResponse(t, begin.ID, challenge, nil)); !handled || err != nil {
		t.Fatalf("begin response handled=%v err=%v", handled, err)
	}
	ack := takePinnedRequest(t, conn)
	if handled, err := controller.handle(pinnedResponse(t, ack.ID, challenge, nil)); !handled || err != nil {
		t.Fatalf("ack response handled=%v err=%v", handled, err)
	}
	_, current, _, _, ok := controller.current(time.Now())
	if !ok || current != challenge {
		t.Fatal("fresh challenge did not restore capability")
	}
	controller.clear()
}

func TestPinnedWSRetirementHintIgnoresRequestsAndOldPanels(t *testing.T) {
	controller, _ := activePinnedController(t)
	conn := newPinnedFakeConn(false)
	controller.conn = conn
	for _, raw := range []string{
		`{"jsonrpc":"2.0","method":"agent.pingFamily.retired.v1","id":"request"}`,
		`{"jsonrpc":"1.0","method":"agent.pingFamily.retired.v1"}`,
	} {
		if handled, err := controller.handleRetirementHint([]byte(raw)); !handled || err != nil {
			t.Fatalf("invalid hint handled=%v err=%v", handled, err)
		}
	}
	controller.mu.Lock()
	controller.handshake.unsupported = true
	controller.mu.Unlock()
	if handled, err := controller.handleRetirementHint([]byte(`{"jsonrpc":"2.0","method":"agent.pingFamily.retired.v1"}`)); !handled || err != nil {
		t.Fatalf("unsupported hint handled=%v err=%v", handled, err)
	}
	select {
	case req := <-conn.outbound:
		t.Fatalf("invalid hint triggered handshake: %+v", req)
	default:
	}
	controller.clear()
}

func TestPinnedWSRejectedRenewalRetiresSessionBeforeFreshHandshake(t *testing.T) {
	controller, params := activePinnedController(t)
	conn := newPinnedFakeConn(false)
	controller.conn = conn
	_, _, _, canceled, ok := controller.current(time.Now())
	if !ok {
		t.Fatal("fixture session is not active")
	}
	controller.mu.Lock()
	renew := controller.handshake.renew()
	controller.mu.Unlock()
	if handled, err := controller.handle(pinnedResponse(t, renew.ID, "", &v2.RPCError{Code: -32602})); !handled || err != nil {
		t.Fatalf("renewal refusal: handled=%v err=%v", handled, err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("old session remained live while starting a fresh handshake")
	}
	if _, _, _, _, ok := controller.current(time.Now()); ok {
		t.Fatal("refused session remained active")
	}
	begin := takePinnedRequest(t, conn)
	if p := begin.Params.(v2.PingFamilySessionParams); p.Action != "begin" || p.Challenge != "" || p.Instance != params.Instance {
		t.Fatalf("fresh begin = %+v", p)
	}
	challenge := "fedcba9876543210fedcba9876543210"
	if handled, err := controller.handle(pinnedResponse(t, begin.ID, challenge, nil)); !handled || err != nil {
		t.Fatalf("begin response: handled=%v err=%v", handled, err)
	}
	ack := takePinnedRequest(t, conn)
	if handled, err := controller.handle(pinnedResponse(t, ack.ID, challenge, nil)); !handled || err != nil {
		t.Fatalf("ack response: handled=%v err=%v", handled, err)
	}
	if _, session, _, _, ok := controller.current(time.Now()); !ok || session != challenge || session == params.Session {
		t.Fatalf("fresh session = %q active=%v", session, ok)
	}
	controller.clear()
}

func TestPinnedWSUnsupportedRenewalDoesNotRetry(t *testing.T) {
	controller, _ := activePinnedController(t)
	conn := newPinnedFakeConn(false)
	controller.conn = conn
	controller.mu.Lock()
	renew := controller.handshake.renew()
	controller.mu.Unlock()
	if handled, err := controller.handle(pinnedResponse(t, renew.ID, "", &v2.RPCError{Code: -32601})); !handled || err != nil {
		t.Fatalf("unsupported renewal: handled=%v err=%v", handled, err)
	}
	if _, _, _, _, ok := controller.current(time.Now()); ok {
		t.Fatal("unsupported panel retained an active session")
	}
	if err := controller.maintain(); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-conn.outbound:
		t.Fatalf("unsupported panel received a retry: %+v", req)
	default:
	}
	controller.clear()
}

func TestPinnedWSReaderAbortInterruptsBlockedHandshakeWrite(t *testing.T) {
	c := newPinnedFakeConn(true)
	done := make(chan struct{})
	go handleWebSocketMessagesWithHandshake(c, done, time.Second, newPinnedWSHandshake())
	c.Abort()
	waitPinnedReader(t, done)
}

func TestPinnedWSSessionLocalExpiryAndDroppedResponse(t *testing.T) {
	h := newPinnedWSHandshake()
	begin := h.begin()
	challenge := "0123456789abcdef0123456789abcdef"
	ack, _ := h.handle(pinnedResponse(t, begin.ID, challenge, nil))
	h.pendingSince = time.Now().Add(-pinnedAgentLease)
	if _, ok := h.handle(pinnedResponse(t, ack.ID, challenge, nil)); !ok || h.active || h.challenge != "" {
		t.Fatal("delayed ack activated expired session")
	}
	begin = h.begin()
	if retry := h.renew(); retry.Params.(v2.PingFamilySessionParams).Action != "begin" || retry.ID == begin.ID {
		t.Fatal("lost begin response did not restart with a new request")
	}
	controller := &pinnedWSController{handshake: h, conn: newPinnedFakeConn(false)}
	h.active, h.challenge, h.validUntil = true, challenge, time.Now().Add(-time.Second)
	if _, _, _, _, ok := controller.current(time.Now()); ok {
		t.Fatal("expired local session reported active")
	}
}

func TestPinnedWSControllerRejectsEventWithHandshakeID(t *testing.T) {
	c := newPinnedFakeConn(false)
	h := newPinnedWSHandshake()
	controller := &pinnedWSController{handshake: h, conn: c}
	if err := controller.begin(); err != nil {
		t.Fatal(err)
	}
	begin := takePinnedRequest(t, c)
	data, err := json.Marshal(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentMessage, ID: begin.ID,
		Params: v2.PingFamilySessionResult{Challenge: "0123456789abcdef0123456789abcdef"}})
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := controller.handle(data); handled || err != nil || h.pendingID != begin.ID {
		t.Fatal("event frame consumed handshake response state")
	}
	controller.clear()
}

func TestPinnedWSSessionRegistryFencesOldDetach(t *testing.T) {
	conn := &ws.SafeConn{}
	old := &pinnedWSController{handshake: newPinnedWSHandshake()}
	current := &pinnedWSController{handshake: newPinnedWSHandshake()}
	challenge := "0123456789abcdef0123456789abcdef"
	current.handshake.active = true
	current.handshake.challenge = challenge
	current.handshake.validUntil = time.Now().Add(time.Minute)
	current.sessionContext, current.sessionCancel = context.WithCancel(context.Background())
	attachPinnedWSSession(conn, old)
	attachPinnedWSSession(conn, current)
	detachPinnedWSSession(conn, old)
	instance, got, deadline, canceled, ok := currentPinnedWSSession(conn, time.Now())
	if !ok || instance != current.handshake.instance || got != challenge || deadline.IsZero() {
		t.Fatal("old detach removed replacement session")
	}
	current.clear()
	select {
	case <-canceled:
	default:
		t.Fatal("disconnect did not cancel in-flight session")
	}
	if _, _, _, _, ok := currentPinnedWSSession(conn, time.Now()); ok {
		t.Fatal("retired controller was still current")
	}
	detachPinnedWSSession(conn, current)
}

func TestPinnedWSWriterAbortRetiresSessionBeforeReturn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	raw, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	socket := ws.NewSafeConn(raw)
	c, p := activePinnedController(t)
	attachPinnedWSSession(socket, c)
	t.Cleanup(func() { detachPinnedWSSession(socket, c) })
	issueTestPermit(t, c, &p, 1)
	started, canceled := make(chan struct{}), make(chan struct{})
	if err := c.launchProbe(p, func(ctx context.Context) { close(started); <-ctx.Done(); close(canceled) }); err != nil {
		t.Fatal(err)
	}
	awaitPinnedSignal(t, started)
	abortPinnedWSSocket(socket)
	if _, err := c.issueProbePermit(permitFor(p, 2)); err == nil {
		t.Fatal("writer abort returned while old session could authorize work")
	}
	awaitPinnedSignal(t, canceled)
}
