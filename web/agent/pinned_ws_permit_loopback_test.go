package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gorilla/websocket"
)

func TestPinnedPermitLoopbackRoundTrip(t *testing.T) {
	const uuid = "pinned-permit-loopback"
	serverConn := make(chan *connection.SafeConn, 1)
	readerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(readerDone)
		raw, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn := connection.NewSafeConn(raw)
		defer conn.Close()
		serverConn <- conn
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			ConsumePinnedWSPermitResponse(uuid, conn, message)
		}
	}))
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close(); <-readerDone })
	t.Cleanup(func() { _ = peer.Close() })
	conn := <-serverConn
	SetConnectedClients(uuid, conn)
	t.Cleanup(func() { ResetClient(uuid) })
	challenge, ok := BeginPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, 1)
	if !ok || !AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, challenge) {
		t.Fatal("could not activate loopback session")
	}

	responseDone := make(chan error, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, message, err := peer.ReadMessage()
		if err != nil {
			responseDone <- err
			return
		}
		var req struct {
			JSONRPC string                    `json:"jsonrpc"`
			ID      string                    `json:"id"`
			Method  string                    `json:"method"`
			Params  v2.PingFamilyPermitParams `json:"params"`
		}
		if err := json.Unmarshal(message, &req); err != nil {
			responseDone <- err
			return
		}
		if req.JSONRPC != v2.Version || req.Method != v2.MethodAgentPingFamilyPermit || req.ID != req.Params.DispatchID || req.Params.Sequence != 1 {
			responseDone <- errPinnedReservation
			return
		}
		responseDone <- peer.WriteJSON(v2.Response{JSONRPC: v2.Version, ID: req.ID, Result: v2.PingFamilyPermitResult{
			DispatchID: req.ID, Sequence: req.Params.Sequence, FreshnessTicket: pinnedTestInstanceB,
		}})
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	lease, err := requestPinnedWSPermit(ctx, uuid)
	if err != nil || lease.conn != conn || lease.session != challenge || lease.ticket != pinnedTestInstanceB || lease.sequence != 1 {
		t.Fatalf("lease=%+v err=%v", lease, err)
	}
	if err := <-responseDone; err != nil {
		t.Fatal(err)
	}
}
