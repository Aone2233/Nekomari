package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentrt "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestClientTokenRotationTransportFence(t *testing.T) {
	const marker = "NEKOMARI_TOKEN_ROTATION_CHILD"
	if os.Getenv(marker) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestClientTokenRotationTransportFence$", "-test.v")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("credential transport fixture: %v\n%s", err, out)
		}
		return
	}
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:rotation-transport?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if err := config.Set(metricstore.MetricDBDSNKey, "file:rotation-transport-metrics?mode=memory&cache=shared"); err != nil {
		t.Fatal(err)
	}
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = metricstore.CloseStoreContext(context.Background()) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	held, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	router.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Hold") == "1" {
			held <- struct{}{}
			select {
			case <-release:
			case <-c.Request.Context().Done():
				c.Abort()
			}
		}
		c.Next()
	})
	group := router.Group("/api/clients", api.RequireRole(api.RoleClient))
	group.POST("/v2/rpc", UploadV2RPC)
	var sockets sync.WaitGroup
	group.GET("/v2/rpc", func(c *gin.Context) {
		sockets.Add(1)
		defer sockets.Done()
		WebSocketV2RPC(c)
	})
	server := httptest.NewServer(router)
	t.Cleanup(func() {
		server.Close()
		sockets.Wait()
	})
	client := &http.Client{Timeout: 5 * time.Second}
	create := func(id string) string {
		t.Helper()
		token := "fixture-token-" + id
		if err := db.Create(&models.Client{UUID: id, Token: token}).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ResetClientState(id) })
		return token
	}
	post := func(token, method string, params any, hold bool) (int, v2.Response) {
		t.Helper()
		body, err := json.Marshal(v2.Request{JSONRPC: v2.Version, Method: method, Params: params, ID: 1})
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/api/clients/v2/rpc", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if hold {
			req.Header.Set("X-Test-Hold", "1")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Error(err)
			return 0, v2.Response{}
		}
		defer resp.Body.Close()
		var result v2.Response
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Error(err)
		}
		return resp.StatusCode, result
	}
	rotate := func(id string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		token, err := clients.RotateClientTokenAndReset(ctx, id, func() { ResetClientState(id) })
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	await := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for admitted operation")
			}
			time.Sleep(time.Millisecond)
		}
	}
	t.Run("http_inflight_and_new_credential", func(t *testing.T) {
		const id = "rotation-http"
		old := create(id)
		status := make(chan int, 1)
		go func() {
			code, _ := post(old, v2.MethodAgentBasicInfo, map[string]any{"info": map[string]any{"cpu_name": "stale"}}, true)
			status <- code
		}()
		<-held
		current := rotate(id)
		release <- struct{}{}
		if code := <-status; code != http.StatusUnauthorized {
			t.Fatalf("pre-authenticated old HTTP request returned %d", code)
		}
		if code, _ := post(old, v2.MethodAgentBasicInfo, map[string]any{}, false); code != http.StatusUnauthorized {
			t.Fatal("old credential still authenticates")
		}
		if code, resp := post(current, v2.MethodAgentBasicInfo, map[string]any{"info": map[string]any{"cpu_name": "fresh"}}, false); code != http.StatusOK || resp.Error != nil {
			t.Fatal("replacement credential cannot write")
		}
		var row models.Client
		if err := db.Where("uuid = ?", id).Take(&row).Error; err != nil || row.CpuName != "fresh" {
			t.Fatalf("fresh basic-info not saved: %v", err)
		}
	})
	t.Run("long_poll_does_not_hold_retirement", func(t *testing.T) {
		const id = "rotation-poll"
		old := create(id)
		status := make(chan int, 1)
		go func() { code, _ := post(old, v2.MethodAgentPull, v2.PullParams{}, false); status <- code }()
		await(func() bool { return agentrt.IsV2Client(id) })
		rotate(id)
		select {
		case code := <-status:
			if code != http.StatusUnauthorized {
				t.Fatalf("retired long poll status = %d", code)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("rotation did not wake old long poll")
		}
		if agentrt.IsV2Client(id) || len(agentrt.TakeV2Events(id, nil, 0)) != 0 {
			t.Fatal("old poll revived runtime or queue state")
		}
	})
	t.Run("delayed_websocket_handshake_cannot_revive_retired_client", func(t *testing.T) {
		const id = "rotation-late-handshake"
		old := create(id)
		done := make(chan error, 1)
		go func() {
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/clients/v2/rpc", http.Header{
				"Origin": {server.URL}, "Authorization": {"Bearer " + old}, "X-Test-Hold": {"1"},
			})
			if err == nil {
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, _, err = conn.ReadMessage()
			}
			done <- err
		}()
		<-held
		rotate(id)
		release <- struct{}{}
		if err := <-done; err == nil {
			t.Fatal("old authenticated handshake remained usable")
		}
		if agentrt.GetConnectedClient(id) != nil || agentrt.IsV2Client(id) {
			t.Fatal("delayed handshake revived retired runtime state")
		}
	})
	t.Run("deletion_orders_inflight_writes", func(t *testing.T) {
		const id = "rotation-deletion"
		old := create(id)
		status := make(chan int, 1)
		go func() {
			code, _ := post(old, v2.MethodAgentReport, v2.ReportParams{Report: v2.Report{CPU: v2.CPUReport{Usage: 3}}}, true)
			status <- code
		}()
		<-held
		if err := clients.DeleteClientAndReset(t.Context(), id, func() { ResetClientState(id) }); err != nil {
			t.Fatal(err)
		}
		release <- struct{}{}
		if code := <-status; code != http.StatusUnauthorized {
			t.Fatalf("deleted-client in-flight write returned %d", code)
		}
		if agentrt.GetLatestReport(id)[id] != nil || agentrt.IsV2Client(id) {
			t.Fatal("in-flight request revived deleted client state")
		}
	})
	t.Run("websocket_retirement_and_reconnect", func(t *testing.T) {
		const id = "rotation-websocket"
		old := create(id)
		dial := func(token string) *websocket.Conn {
			t.Helper()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/clients/v2/rpc", http.Header{
				"Origin": {server.URL}, "Authorization": {"Bearer " + token},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			return conn
		}
		conn := dial(old)
		await(func() bool { return agentrt.GetConnectedClient(id) != nil })
		report := v2.ReportParams{Report: v2.Report{CPU: v2.CPUReport{Usage: 12}}}
		if err := conn.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentReport, Params: report, ID: 2}); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var reply v2.Response
		if err := conn.ReadJSON(&reply); err != nil || reply.Error != nil {
			t.Fatalf("pre-rotation websocket report failed: %v", err)
		}
		agentrt.EnqueueV2Event(id, v2.MethodAgentTerminal, v2.TerminalRequestParams{RequestID: "old-terminal"})
		current := rotate(id)
		if _, _, err := conn.ReadMessage(); err == nil {
			t.Fatal("old WebSocket survived rotation")
		}
		if agentrt.GetConnectedClient(id) != nil || agentrt.IsV2Client(id) || len(agentrt.TakeV2Events(id, nil, 0)) != 0 {
			t.Fatal("rotation retained active credential state")
		}
		if agentrt.GetLatestReport(id)[id] == nil {
			t.Fatal("rotation removed historical/latest measurements")
		}
		fresh := dial(current)
		if err := fresh.WriteJSON(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentBasicInfo,
			Params: map[string]any{"info": map[string]any{"cpu_name": "replacement"}}, ID: 3}); err != nil {
			t.Fatal(err)
		}
		_ = fresh.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := fresh.ReadJSON(&reply); err != nil || reply.Error != nil {
			t.Fatalf("replacement WebSocket failed: %v", err)
		}
	})
	t.Run("body_credential_survives_parser_consumption", func(t *testing.T) {
		const id = "rotation-body"
		token := create(id)
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": v2.MethodAgentBasicInfo, "id": 1,
			"token": token, "params": map[string]any{"info": map[string]any{"cpu_name": "body-auth"}}})
		response, err := client.Post(server.URL+"/api/clients/v2/rpc", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("body-auth HTTP status = %d", response.StatusCode)
		}
	})
}
