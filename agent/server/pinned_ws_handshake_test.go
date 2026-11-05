package server

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/ws"
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
	outbound   chan v2.Request
	closed     chan struct{}
	once       sync.Once
	blockWrite bool
}

func newPinnedFakeConn(blockWrite bool) *pinnedFakeConn {
	return &pinnedFakeConn{inbound: make(chan []byte, 8), outbound: make(chan v2.Request, 8), closed: make(chan struct{}), blockWrite: blockWrite}
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
	if c.blockWrite {
		<-c.closed
		return errors.New("closed")
	}
	select {
	case c.outbound <- data.(v2.Request):
		return nil
	case <-c.closed:
		return errors.New("closed")
	}
}

func (c *pinnedFakeConn) Abort() { c.once.Do(func() { close(c.closed) }) }

func takePinnedRequest(t *testing.T, c *pinnedFakeConn) v2.Request {
	t.Helper()
	select {
	case req := <-c.outbound:
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
	current.activeCancel = make(chan struct{})
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
