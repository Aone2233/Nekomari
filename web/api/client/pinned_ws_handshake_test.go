package client

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentruntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/connection"
)

func pinnedHandshakeRequest(t *testing.T, params v2.PingFamilySessionParams) v2.RawRequest {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return v2.RawRequest{JSONRPC: v2.Version, ID: "test", Method: v2.MethodAgentPingFamilySession, Params: b}
}

func TestPinnedWSHandlerRequiresCurrentSocketAndCredential(t *testing.T) {
	const uuid = "pinned-handshake-handler"
	const instance = "0123456789abcdef0123456789abcdef"
	a, b := &connection.SafeConn{}, &connection.SafeConn{}
	agentruntime.SetConnectedClients(uuid, a)
	t.Cleanup(func() { agentruntime.ResetClient(uuid) })
	admitted := func(_ context.Context, fn func() error) error { return fn() }
	retired := func(context.Context, func() error) error { return api.ErrClientCredentialRetired }
	begin := pinnedHandshakeRequest(t, v2.PingFamilySessionParams{Action: "begin", Version: 1, Instance: instance})
	if response := handlePinnedWSHandshake(t.Context(), uuid, a, begin, retired); response.Error == nil || response.Error.Code != -32001 {
		t.Fatal("retired credential began session")
	}
	response := handlePinnedWSHandshake(t.Context(), uuid, a, begin, admitted)
	if response.Error != nil {
		t.Fatalf("begin: %+v", response.Error)
	}
	challenge, ok := response.Result.(v2.PingFamilySessionResult)
	if !ok || challenge.Challenge == "" {
		t.Fatalf("begin result = %+v", response.Result)
	}
	ack := pinnedHandshakeRequest(t, v2.PingFamilySessionParams{Action: "ack", Version: 1, Instance: instance, Challenge: challenge.Challenge})
	if response := handlePinnedWSHandshake(t.Context(), uuid, a, ack, retired); response.Error == nil || response.Error.Code != -32001 {
		t.Fatal("retired credential acknowledged session")
	}
	if response := handlePinnedWSHandshake(t.Context(), uuid, a, ack, admitted); response.Error != nil || !agentruntime.PinnedWSSessionCurrent(uuid, a, instance, challenge.Challenge, time.Now()) {
		t.Fatal("current session did not activate")
	}
	agentruntime.SetConnectedClients(uuid, b)
	if response := handlePinnedWSHandshake(t.Context(), uuid, a, ack, admitted); response.Error == nil {
		t.Fatal("replaced socket renewed session")
	}
	if response := handleV2RPCOperation(t.Context(), uuid, begin, false, admitted); response.Error == nil || response.Error.Code != -32601 {
		t.Fatal("generic POST/legacy path accepted WebSocket-only handshake")
	}
}
