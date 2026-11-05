package server

import (
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
// Probe dispatch does not use this state yet.
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
const pinnedAgentLease = 60 * time.Second

var pinnedProcessInstance = sync.OnceValues(func() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
})

type pinnedHandshakeConn interface {
	ReadMessageLimited(int64) (int, []byte, error)
	WriteJSON(interface{}) error
	Abort()
}

type pinnedWSController struct {
	mu           sync.Mutex
	handshake    *pinnedWSHandshake
	conn         pinnedHandshakeConn
	stopped      bool
	activeCancel chan struct{}
}

// Registry identity is the socket, never the Agent instance alone. It remains
// unused by task dispatch until the start/cancel fence is implemented.
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
	if err := c.conn.WriteJSON(req); err != nil {
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
	return c.send(c.handshake.begin())
}

func (c *pinnedWSController) maintain() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.handshake.unsupported {
		return nil
	}
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
	wasActive := c.handshake.active
	next, handled := c.handshake.handle(data)
	if wasActive && !c.handshake.active {
		c.retireLocked()
	}
	if !wasActive && c.handshake.active {
		c.activeCancel = make(chan struct{})
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
	if c.activeCancel != nil {
		close(c.activeCancel)
		c.activeCancel = nil
	}
}

func (c *pinnedWSController) current(now time.Time) (string, string, time.Time, <-chan struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || !c.handshake.active || !now.Before(c.handshake.validUntil) || c.activeCancel == nil {
		return "", "", time.Time{}, nil, false
	}
	return c.handshake.instance, c.handshake.challenge, c.handshake.validUntil, c.activeCancel, true
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
