package server

import (
	"encoding/json"
	"testing"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
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
	if _, handled := h.handle(pinnedResponse(t, begin.ID, "", &v2.RPCError{Code: -32601, Message: "method not found"})); !handled || h.challenge != "" {
		t.Fatal("old panel error created a capability")
	}
	begin = h.begin()
	if _, handled := h.handle(pinnedResponse(t, begin.ID, "invalid", nil)); !handled || h.challenge != "" {
		t.Fatal("invalid server challenge created a capability")
	}
}
