package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/ws"
)

// One handshake object belongs to one socket and is never shared on reconnect.
type pinnedWSHandshake struct {
	instance     string
	challenge    string
	pendingID    string
	phase        string
	sequence     uint64
	active       bool
	validUntil   time.Time
	pendingSince time.Time
	unsupported  bool
}

// The server lease is 90 seconds. Starting the local lease at request send,
// rather than response receipt, makes delayed acknowledgements fail closed.
const (
	pinnedAgentLease          = 60 * time.Second
	pinnedControlWriteTimeout = 2 * time.Second
)

var pinnedProcessInstance = sync.OnceValues(func() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
})

type pinnedHandshakeConn interface {
	ReadMessageLimited(int64) (int, []byte, error)
	WriteJSONContextWait(context.Context, interface{}) error
	Abort()
}

type pinnedWSController struct {
	mu             sync.Mutex
	handshake      *pinnedWSHandshake
	conn           pinnedHandshakeConn
	stopped        bool
	sessionContext context.Context
	sessionCancel  context.CancelFunc
	sessionTimer   *time.Timer
	tickets        map[string]pinnedProbeTicket
	ticketSequence uint64
	activeProbes   int
	probeRunner    pinnedProbeRunner
	probeCache     pinnedProbeCache
	probeDispatch  func(v2.PingFamilyProbeParams)
}

// Registry identity is the socket, never the Agent instance alone.
var pinnedSocketSessions = struct {
	sync.RWMutex
	byConn map[*ws.SafeConn]*pinnedWSController
}{byConn: make(map[*ws.SafeConn]*pinnedWSController)}

func attachPinnedWSSession(conn *ws.SafeConn, controller *pinnedWSController) {
	pinnedSocketSessions.Lock()
	defer pinnedSocketSessions.Unlock()
	pinnedSocketSessions.byConn[conn] = controller
}

func detachPinnedWSSession(conn *ws.SafeConn, controller *pinnedWSController) {
	pinnedSocketSessions.Lock()
	defer pinnedSocketSessions.Unlock()
	if pinnedSocketSessions.byConn[conn] == controller {
		delete(pinnedSocketSessions.byConn, conn)
	}
}

// A transport failure can be observed by the writer before the reader exits.
// Abort first to release any blocked handshake write, then synchronously retire
// the associated controller before a replacement connection can be started.
func abortPinnedWSSocket(conn *ws.SafeConn) {
	if conn == nil {
		return
	}
	conn.Abort()
	pinnedSocketSessions.RLock()
	controller := pinnedSocketSessions.byConn[conn]
	pinnedSocketSessions.RUnlock()
	if controller != nil {
		controller.clear()
	}
}

func currentPinnedWSSession(conn *ws.SafeConn, now time.Time) (string, string, time.Time, <-chan struct{}, bool) {
	pinnedSocketSessions.RLock()
	controller := pinnedSocketSessions.byConn[conn]
	pinnedSocketSessions.RUnlock()
	if controller == nil {
		return "", "", time.Time{}, nil, false
	}
	return controller.current(now)
}

func (c *pinnedWSController) send(req v2.Request) error {
	// Holding c.mu orders requests with retirement. The bounded write cannot
	// retain that lock past either this lease or the control-write timeout.
	deadline := time.Now().Add(pinnedControlWriteTimeout)
	if c.handshake.active && c.handshake.validUntil.Before(deadline) {
		deadline = c.handshake.validUntil
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := c.conn.WriteJSONContextWait(ctx, req); err != nil {
		c.retireLocked()
		c.handshake.clear()
		return err
	}
	return nil
}

func (c *pinnedWSController) begin() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return nil
	}
	c.retireLocked()
	return c.send(c.handshake.begin())
}

// A POST may have retired this socket's panel-side lease. The hint contains no
// capability; only a fresh challenge and acknowledgement can restore one.
func (c *pinnedWSController) handleRetirementHint(data []byte) (bool, error) {
	if len(data) > 4096 {
		return false, nil
	}
	var frame struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.Method != v2.MethodAgentPingFamilyRetired {
		return false, nil
	}
	if frame.JSONRPC != v2.Version || len(frame.ID) != 0 {
		return true, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.handshake.unsupported {
		return true, nil
	}
	c.retireLocked()
	return true, c.send(c.handshake.begin())
}

func (c *pinnedWSController) maintain() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.handshake.unsupported {
		return nil
	}
	c.expireLocked(time.Now())
	request := c.handshake.renew()
	if !c.handshake.active {
		c.retireLocked()
	}
	return c.send(request)
}

func (c *pinnedWSController) handle(data []byte) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return false, nil
	}
	// Only responses belong to the handshake; inbound requests/events with an
	// attacker-selected ID must never consume its pending request.
	if len(data) > 4096 {
		// Do not parse a large unrelated event a second time as a response.
		return false, nil
	}
	var frame struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.Method != "" {
		return false, nil
	}
	c.expireLocked(time.Now())
	wasActive := c.handshake.active
	next, handled := c.handshake.handle(data)
	if wasActive && !c.handshake.active {
		c.retireLocked()
	}
	// An authenticated POST can retire the panel's copy of this socket lease.
	// A rejected renewal must first cancel local work, then establish a fresh
	// challenge rather than waiting for another 25-second maintenance tick.
	// Never retry an unsupported old panel or a failed initial handshake here.
	if wasActive && !c.handshake.active && handled && next == nil && !c.handshake.unsupported {
		request := c.handshake.begin()
		next = &request
	}
	if !wasActive && c.handshake.active {
		c.sessionContext, c.sessionCancel = context.WithCancel(context.Background())
	}
	if handled && c.handshake.active {
		c.armSessionExpiryLocked()
	}
	if next != nil {
		return handled, c.send(*next)
	}
	return handled, nil
}

func (c *pinnedWSController) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	c.retireLocked()
	c.handshake.clear()
}

func (c *pinnedWSController) retireLocked() {
	if c.sessionTimer != nil {
		c.sessionTimer.Stop()
		c.sessionTimer = nil
	}
	if c.sessionCancel != nil {
		c.sessionCancel()
		c.sessionCancel = nil
	}
	c.sessionContext = nil
	clear(c.tickets)
	c.ticketSequence = 0
}

// A renewed lease extends the current session, including in-flight probes.
// The timer takes the same lock as renewal and rechecks the latest deadline,
// so an already-queued callback cannot retire a successfully renewed session.
func (c *pinnedWSController) armSessionExpiryLocked() {
	if c.sessionTimer != nil {
		c.sessionTimer.Stop()
	}
	delay := time.Until(c.handshake.validUntil)
	if delay < 0 {
		delay = 0
	}
	c.sessionTimer = time.AfterFunc(delay, c.sessionExpiry)
}

func (c *pinnedWSController) sessionExpiry() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.handshake == nil || !c.handshake.active {
		return
	}
	if time.Now().Before(c.handshake.validUntil) {
		c.armSessionExpiryLocked()
		return
	}
	c.expireLocked(time.Now())
}

// A late renewal must not revive a locally expired session or its permits.
func (c *pinnedWSController) expireLocked(now time.Time) {
	if c.handshake != nil && c.handshake.active && !now.Before(c.handshake.validUntil) {
		c.retireLocked()
		c.handshake.clear()
	}
}

func (c *pinnedWSController) current(now time.Time) (string, string, time.Time, <-chan struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireLocked(now)
	if c.stopped || !c.handshake.active || c.sessionContext == nil {
		return "", "", time.Time{}, nil, false
	}
	return c.handshake.instance, c.handshake.challenge, c.handshake.validUntil, c.sessionContext.Done(), true
}

func pinnedValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func newPinnedWSHandshake() *pinnedWSHandshake {
	instance, err := pinnedProcessInstance()
	if err != nil {
		return nil
	}
	return &pinnedWSHandshake{instance: instance}
}

func (h *pinnedWSHandshake) request(action string) v2.Request {
	h.sequence++
	h.pendingSince = time.Now()
	h.pendingID = fmt.Sprintf("ping-family-%s-%d", h.instance, h.sequence)
	h.phase = action
	return v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilySession, ID: h.pendingID,
		Params: v2.PingFamilySessionParams{Action: action, Version: 1, Instance: h.instance, Challenge: h.challenge}}
}

func (h *pinnedWSHandshake) begin() v2.Request {
	h.challenge = ""
	h.active = false
	h.validUntil = time.Time{}
	h.unsupported = false
	return h.request("begin")
}

// handle accepts only the matching JSON-RPC response. A begin response must
// echo a syntactically valid server challenge; an ack must echo that challenge.
func (h *pinnedWSHandshake) handle(data []byte) (*v2.Request, bool) {
	var raw struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *v2.RPCError    `json:"error"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.JSONRPC != v2.Version {
		return nil, false
	}
	var id string
	if json.Unmarshal(raw.ID, &id) != nil || id != h.pendingID || h.pendingID == "" {
		return nil, false
	}
	phase := h.phase
	sentAt := h.pendingSince
	h.pendingID, h.phase = "", ""
	h.pendingSince = time.Time{}
	var result v2.PingFamilySessionResult
	if raw.Error != nil || json.Unmarshal(raw.Result, &result) != nil || !pinnedValidID(result.Challenge) {
		h.unsupported = raw.Error != nil && raw.Error.Code == -32601
		h.challenge = ""
		h.active = false
		h.validUntil = time.Time{}
		return nil, true
	}
	if phase == "begin" {
		h.challenge = result.Challenge
		h.active = false
		h.validUntil = time.Time{}
		ack := h.request("ack")
		return &ack, true
	}
	if phase != "ack" || h.challenge != result.Challenge || sentAt.IsZero() || !time.Now().Before(sentAt.Add(pinnedAgentLease)) {
		h.challenge = ""
		h.active = false
		h.validUntil = time.Time{}
	} else {
		h.active = true
		h.validUntil = sentAt.Add(pinnedAgentLease)
	}
	return nil, true
}

func (h *pinnedWSHandshake) renew() v2.Request {
	if h.pendingID != "" {
		return h.begin()
	}
	if h.active && !time.Now().Before(h.validUntil) {
		return h.begin()
	}
	if h.challenge == "" {
		return h.begin()
	}
	return h.request("ack")
}

func (h *pinnedWSHandshake) clear() {
	h.challenge, h.pendingID, h.phase = "", "", ""
	h.active = false
	h.validUntil, h.pendingSince = time.Time{}, time.Time{}
	h.unsupported = false
}
