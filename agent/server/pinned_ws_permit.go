package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

const (
	pinnedTicketTTL         = 5 * time.Second
	pinnedProbeExecutionTTL = 30 * time.Second
	pinnedMaxTickets        = 64
	pinnedMaxActiveProbes   = 32
)

var errPinnedPermit = errors.New("invalid, expired or unavailable pinned permit")

// Permit requests have no target and cannot start a probe. This separate
// response path is limited to the existing WebSocket reader; POST events do
// not have a controller and remain unsupported for pinned work.
func (c *pinnedWSController) handleProbePermit(data []byte) (bool, error) {
	if len(data) > 4096 {
		return false, nil
	}
	var frame struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
		Params  json.RawMessage `json:"params"`
	}
	if json.Unmarshal(data, &frame) != nil || frame.Method != v2.MethodAgentPingFamilyPermit {
		return false, nil
	}
	var id string
	if frame.JSONRPC != v2.Version || json.Unmarshal(frame.ID, &id) != nil ||
		len(id) > 64 || !pinnedValidID(id) || bytes.Equal(bytes.TrimSpace(frame.Params), []byte("null")) {
		return true, nil
	}
	var p v2.PingFamilyPermitParams
	resp := v2.Response{JSONRPC: v2.Version, ID: id}
	decoder := json.NewDecoder(bytes.NewReader(frame.Params))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&p)
	if decodeErr == nil && decoder.Decode(new(any)) == io.EOF {
		if result, permitErr := c.issueProbePermit(p); permitErr == nil {
			resp.Result = result
		} else {
			resp.Error = &v2.RPCError{Code: -32602, Message: "invalid or expired permit request"}
		}
	} else {
		resp.Error = &v2.RPCError{Code: -32602, Message: "invalid or expired permit request"}
	}
	return true, c.conn.WriteJSON(resp)
}

type pinnedProbeTicket struct {
	dispatchID string
	expires    time.Time
}

func newPinnedTicket() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// The controller lock serializes ticket consumption and probe admission with
// handshake expiry and socket retirement. The permit request is wired to the
// WebSocket reader, but no production caller starts a pinned probe yet.
func (c *pinnedWSController) issueProbePermit(p v2.PingFamilyPermitParams) (v2.PingFamilyPermitResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.expireLocked(now)
	if c.stopped || c.handshake == nil || !c.handshake.active || c.sessionContext == nil ||
		!pinnedValidID(p.Instance) || !pinnedValidID(p.Session) || !pinnedValidID(p.DispatchID) ||
		p.Instance != c.handshake.instance || p.Session != c.handshake.challenge ||
		p.Sequence == 0 || p.Sequence <= c.ticketSequence {
		return v2.PingFamilyPermitResult{}, errPinnedPermit
	}
	// A sequence is consumed even on a transient capacity or RNG failure: an
	// old permit request must not become valid later by being replayed.
	c.ticketSequence = p.Sequence
	for token, ticket := range c.tickets {
		if !now.Before(ticket.expires) {
			delete(c.tickets, token)
		}
	}
	if len(c.tickets) >= pinnedMaxTickets {
		return v2.PingFamilyPermitResult{}, errPinnedPermit
	}
	token, err := newPinnedTicket()
	if err != nil {
		return v2.PingFamilyPermitResult{}, errPinnedPermit
	}
	if _, exists := c.tickets[token]; exists {
		return v2.PingFamilyPermitResult{}, errPinnedPermit
	}
	if c.tickets == nil {
		c.tickets = make(map[string]pinnedProbeTicket)
	}
	c.tickets[token] = pinnedProbeTicket{dispatchID: p.DispatchID, expires: now.Add(pinnedTicketTTL)}
	return v2.PingFamilyPermitResult{DispatchID: p.DispatchID, Sequence: p.Sequence, FreshnessTicket: token}, nil
}

// launchProbe consumes an exact permit and registers the goroutine before
// releasing the retirement lock. Its context is canceled on session retirement
// and at the local lease deadline; no remote wall-clock comparison is needed.
func (c *pinnedWSController) launchProbe(p v2.PingFamilyProbeParams, run func(context.Context)) error {
	if run == nil {
		return errPinnedPermit
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.expireLocked(now)
	if c.stopped || c.handshake == nil || !c.handshake.active || c.sessionContext == nil ||
		validatePinnedProbeEnvelope(p, c.handshake.instance, c.handshake.challenge) != nil {
		return errPinnedPermit
	}
	ticket, ok := c.tickets[p.FreshnessTicket]
	if !ok || ticket.dispatchID != p.DispatchID {
		return errPinnedPermit
	}
	delete(c.tickets, p.FreshnessTicket)
	if !now.Before(ticket.expires) || c.activeProbes >= pinnedMaxActiveProbes {
		return errPinnedPermit
	}
	deadline := now.Add(pinnedProbeExecutionTTL)
	if c.handshake.validUntil.Before(deadline) {
		deadline = c.handshake.validUntil
	}
	ctx, cancel := context.WithDeadline(c.sessionContext, deadline)
	c.activeProbes++
	go func() {
		defer func() {
			cancel()
			c.mu.Lock()
			c.activeProbes--
			c.mu.Unlock()
		}()
		run(ctx)
	}()
	return nil
}
