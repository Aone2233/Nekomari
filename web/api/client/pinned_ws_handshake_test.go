package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentruntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gin-gonic/gin"
)

func pinnedHandshakeRequest(t *testing.T, params v2.PingFamilySessionParams) v2.RawRequest {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return v2.RawRequest{JSONRPC: v2.Version, ID: "test", Method: v2.MethodAgentPingFamilySession, Params: b}
}

func TestPinnedPOSTHandlerRetiresSessionAndRejectsUnauthorized(t *testing.T) {
	const uuid = "pinned-post-handler"
	const instance = "0123456789abcdef0123456789abcdef"
	conn := &connection.SafeConn{}
	agentruntime.SetConnectedClients(uuid, conn)
	t.Cleanup(func() { agentruntime.ResetClient(uuid) })
	challenge, ok := agentruntime.BeginPinnedWSHandshake(uuid, conn, instance, 1)
	if !ok || !agentruntime.AckPinnedWSHandshake(uuid, conn, instance, challenge) {
		t.Fatal("session did not activate")
	}
	gin.SetMode(gin.TestMode)
	upload := func(principal *rpc.Principal) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"fixture.unknown","params":{}}`))
		c.Set("client_uuid", uuid)
		api.SetPrincipal(c, principal)
		UploadV2RPC(c)
		return w
	}
	if got := upload(rpc.NewAnonymousPrincipal()); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST status = %d", got.Code)
	}
	if !agentruntime.PinnedWSSessionCurrent(uuid, conn, instance, challenge, time.Now()) {
		t.Fatal("unauthorized POST retired the session")
	}
	if got := upload(rpc.NewUserPrincipal("fixture-admin")); got.Code != http.StatusBadRequest {
		t.Fatalf("authenticated unknown method status = %d", got.Code)
	}
	if agentruntime.PinnedWSSessionCurrent(uuid, conn, instance, challenge, time.Now()) {
		t.Fatal("actual POST handler left pinned capability active")
	}
}

func TestPinnedPermitResponseRequiresCredentialAdmission(t *testing.T) {
	const uuid = "pinned-permit-credential"
	conn := &connection.SafeConn{}
	agentruntime.SetConnectedClients(uuid, conn)
	t.Cleanup(func() { agentruntime.ResetClient(uuid) })
	ran := false
	retired := func(context.Context, func() error) error { return api.ErrClientCredentialRetired }
	consumed, err := consumePinnedPermitResponse(t.Context(), uuid, conn, []byte(`{"jsonrpc":"2.0","id":"x","result":{}}`), retired)
	if consumed || !errors.Is(err, api.ErrClientCredentialRetired) {
		t.Fatalf("retired credential: consumed=%v err=%v", consumed, err)
	}
	admitted := func(_ context.Context, fn func() error) error { ran = true; return fn() }
	consumed, err = consumePinnedPermitResponse(t.Context(), uuid, conn, []byte(`{"jsonrpc":"2.0","id":"x","result":{}}`), admitted)
	if !ran || consumed || err != nil {
		t.Fatalf("unmatched response: called=%v consumed=%v err=%v", ran, consumed, err)
	}
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

func TestPinnedPOSTTransitionRequiresCredentialAndRetiresSession(t *testing.T) {
	const uuid = "pinned-post-transition"
	const instance = "0123456789abcdef0123456789abcdef"
	conn := &connection.SafeConn{}
	agentruntime.SetConnectedClients(uuid, conn)
	t.Cleanup(func() { agentruntime.ResetClient(uuid) })
	challenge, ok := agentruntime.BeginPinnedWSHandshake(uuid, conn, instance, 1)
	if !ok || !agentruntime.AckPinnedWSHandshake(uuid, conn, instance, challenge) {
		t.Fatal("session did not activate")
	}
	retired := func(context.Context, func() error) error { return api.ErrClientCredentialRetired }
	if err := retirePinnedWSForPOST(t.Context(), uuid, retired); !errors.Is(err, api.ErrClientCredentialRetired) {
		t.Fatalf("retired POST: %v", err)
	}
	if !agentruntime.PinnedWSSessionCurrent(uuid, conn, instance, challenge, time.Now()) {
		t.Fatal("retired credential could disturb the active session")
	}
	admitted := func(_ context.Context, fn func() error) error { return fn() }
	if err := retirePinnedWSForPOST(t.Context(), uuid, admitted); err != nil {
		t.Fatal(err)
	}
	if agentruntime.PinnedWSSessionCurrent(uuid, conn, instance, challenge, time.Now()) || agentruntime.GetConnectedClient(uuid) != conn {
		t.Fatal("POST did not retire only the pinned session")
	}
	if agentruntime.AckPinnedWSHandshake(uuid, conn, instance, challenge) {
		t.Fatal("old challenge revived after POST transition")
	}
	newChallenge, ok := agentruntime.BeginPinnedWSHandshake(uuid, conn, instance, 1)
	if !ok || newChallenge == challenge || !agentruntime.AckPinnedWSHandshake(uuid, conn, instance, newChallenge) {
		t.Fatal("new handshake did not recover")
	}
}
