package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

// One handshake object belongs to one socket and is never shared on reconnect.
// It is not connected to the live reader or probe dispatch yet.
type pinnedWSHandshake struct {
	instance  string
	challenge string
	pendingID string
	phase     string
	sequence  uint64
	active    bool
}

var pinnedInstance = sync.OnceValues(func() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
})

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
	instance, err := pinnedInstance()
	if err != nil {
		return nil
	}
	return &pinnedWSHandshake{instance: instance}
}

func (h *pinnedWSHandshake) request(action string) v2.Request {
	h.sequence++
	h.pendingID = fmt.Sprintf("ping-family-%s-%d", h.instance, h.sequence)
	h.phase = action
	return v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPingFamilySession, ID: h.pendingID,
		Params: v2.PingFamilySessionParams{Action: action, Version: 1, Instance: h.instance, Challenge: h.challenge}}
}

func (h *pinnedWSHandshake) begin() v2.Request {
	h.challenge = ""
	h.active = false
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
	h.pendingID, h.phase = "", ""
	var result v2.PingFamilySessionResult
	if raw.Error != nil || json.Unmarshal(raw.Result, &result) != nil || !pinnedValidID(result.Challenge) {
		h.challenge = ""
		h.active = false
		return nil, true
	}
	if phase == "begin" {
		h.challenge = result.Challenge
		h.active = false
		ack := h.request("ack")
		return &ack, true
	}
	if phase != "ack" || h.challenge != result.Challenge {
		h.challenge = ""
		h.active = false
	} else {
		h.active = true
	}
	return nil, true
}

func (h *pinnedWSHandshake) renew() v2.Request {
	if h.challenge == "" {
		return h.begin()
	}
	return h.request("ack")
}
