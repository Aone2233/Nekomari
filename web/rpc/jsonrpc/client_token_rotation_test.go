package jsonrpc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/apikeys"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentrt "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
)

func TestClientImportCannotReplaceExistingCredential(t *testing.T) {
	id := uuid.NewString()
	const token = "fixture-import-token"
	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Client{UUID: id, Token: token}).Error; err != nil {
		t.Fatal(err)
	}
	document := map[string]any{"schema_version": configxport.SchemaVersion, "secrets_included": true,
		"clients": []map[string]any{{"uuid": id, "token": "stale-export-token", "name": "must-not-apply"}}}
	for _, method := range []string{"admin:planConfigImport", "admin:importConfig"} {
		request := &rpc.JsonRpcRequest{Params: map[string]any{"document": document}}
		if _, err := bindDocument(request); err == nil {
			t.Fatalf("%s did not reject stale credential before writes", method)
		}
	}
	if err := applyRecord("clients", id, map[string]any{"token": "unreviewed-token"}, false); err == nil {
		t.Fatal("import adapter bypassed rotation")
	}
	if err := applyRecord("clients", id, map[string]any{"token": token, "name": "valid-metadata"}, false); err != nil {
		t.Fatal("unchanged-secret export cannot update metadata")
	}
	if _, err := clients.RotateClientTokenAndReset(t.Context(), id, nil); err != nil {
		t.Fatal(err)
	}
	if err := applyRecord("clients", id, map[string]any{"token": token, "name": "must-not-apply"}, false); err == nil {
		t.Fatal("import admitted before rotation overwrote the replacement")
	}
	var row models.Client
	if err := db.Where("uuid = ?", id).Take(&row).Error; err != nil || row.Token == token || row.Name != "valid-metadata" {
		t.Fatalf("stale import changed credential/metadata: %v", err)
	}
}

func TestRotateClientTokenSessionStepUpAndAudit(t *testing.T) {
	account := createStepUpAccount(t, "rotate-client-session", "Rotate-Client-Passw0rd")
	secret, _, err := accounts.Generate2Fa()
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Enable2Fa(account.UUID, secret); err != nil {
		t.Fatal(err)
	}
	id, original := uuid.NewString(), "fixture-original-token"
	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Client{UUID: id, Token: original}).Error; err != nil {
		t.Fatal(err)
	}
	call := func(p *rpc.Principal, session, code, clientID string) *rpc.JsonRpcResponse {
		return dispatchWithSensitive(t.Context(), nil, &rpc.ContextMeta{Principal: p, Permission: p.PrimaryRole(),
			UserUUID: p.UserUUID, SessionToken: session, RemoteIP: "203.0.113.8"}, &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION, Method: "admin:rotateClientToken", Params: map[string]any{"uuid": clientID, "2fa_code": code},
		})
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, response := range map[string]*rpc.JsonRpcResponse{
		"missing TOTP": call(rpc.NewUserPrincipal(account.UUID), "fixture-session", "", id),
		"invalid TOTP": call(rpc.NewUserPrincipal(account.UUID), "fixture-session", "invalid", id),
		"no session":   call(rpc.NewUserPrincipal(account.UUID), "", code, id),
		"anonymous":    call(rpc.NewAnonymousPrincipal(), "", "", id),
		"agent":        call(rpc.NewAgentPrincipal(id), "", "", id),
		"all scopes":   call(rpc.NewScopedAPIKeyPrincipal(uuid.NewString(), rpc.APIKeyScopes()), "", code, id),
	} {
		if response == nil || response.Error == nil || response.Error.Code != rpc.PermissionDenied {
			t.Fatalf("%s bypassed session/step-up gate", name)
		}
	}
	if got, err := clients.GetClientUUIDByToken(original); err != nil || got != id {
		t.Fatal("denied rotation changed credential")
	}
	for _, badID := range []string{"", "not-a-uuid", uuid.NewString()} {
		response := call(rpc.NewUserPrincipal(account.UUID), "fixture-session", code, badID)
		if response.Error == nil || (response.Error.Code != rpc.InvalidParams && response.Error.Code != rpc.NotFound) {
			t.Fatal("invalid/missing node returned success")
		}
	}
	agentrt.RecordReport(v2.Report{UUID: id, CPU: v2.CPUReport{Usage: 8}})
	agentrt.MarkV2Client(id)
	agentrt.SetPresence(id, 123, true)
	agentrt.EnqueueV2Event(id, v2.MethodAgentTerminal, v2.TerminalRequestParams{RequestID: "old"})
	response := call(rpc.NewUserPrincipal(account.UUID), "fixture-session", code, id)
	if response == nil || response.Error != nil {
		t.Fatalf("authorized rotation refused: %v", response)
	}
	token, ok := response.Result.(map[string]any)["token"].(string)
	if !ok || !strings.HasPrefix(token, "na_") {
		t.Fatal("rotation did not return replacement credential")
	}
	if _, err := clients.GetClientUUIDByToken(original); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("old token remains usable")
	}
	if agentrt.IsV2Client(id) || len(agentrt.TakeV2Events(id, nil, 0)) != 0 || agentrt.GetLatestReport(id)[id] == nil {
		t.Fatal("rotation did not retire commands/presence while preserving measurements")
	}
	var entry models.Log
	if err := db.Where("message = ?", "rotate client token:"+id).Take(&entry).Error; err != nil || entry.UUID != account.UUID || entry.IP != "203.0.113.8" {
		t.Fatalf("rotation audit attribution invalid: %v", err)
	}
	if strings.Contains(entry.Message, original) || strings.Contains(entry.Message, token) {
		t.Fatal("credential leaked into audit")
	}
	plain := createStepUpAccount(t, "rotate-client-plain", "Rotate-Client-Plain1")
	if response := call(rpc.NewUserPrincipal(plain.UUID), "fixture-plain-session", "", id); response.Error != nil {
		t.Fatal("account without 2FA lost its existing sensitive-operation policy")
	}
}

func TestRotateClientTokenRESTAndWebSocket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := createStepUpAccount(t, "rotate-client-transport", "Rotate-Transport-Passw0rd")
	session, err := accounts.CreateSession(account.UUID, 3600, "", "", "password")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := dbcore.GetDBInstance().Create(&models.Client{UUID: id, Token: "fixture-transport-original"}).Error; err != nil {
		t.Fatal(err)
	}
	_, key, err := apikeys.Create(t.Context(), dbcore.GetDBInstance(), "rotation refusal", rpc.APIKeyScopes(), 1, account.UUID)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(api.IdentityMiddleware())
	router.POST("/api/admin/client/:uuid/token/rotate", api.RequireRole(api.RoleAdmin), Bind("admin:rotateClientToken", WithPath("uuid"), WithFlat()))
	router.POST("/api/rpc2", OnRpcRequest)
	router.GET("/api/rpc2", OnRpcRequest)
	server := httptest.NewServer(router)
	defer server.Close()
	for _, bearer := range []string{"", key} {
		writer := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/admin/client/"+id+"/token/rotate", strings.NewReader("{}"))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		router.ServeHTTP(writer, req)
		if writer.Code != http.StatusUnauthorized && writer.Code != http.StatusForbidden {
			t.Fatal("REST rotation accepted guest/API key")
		}
	}
	writer := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/admin/client/"+id+"/token/rotate", strings.NewReader(`{"uuid":"must-not-override-path"}`))
	req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
	router.ServeHTTP(writer, req)
	var result struct{ Status, Token string }
	if json.Unmarshal(writer.Body.Bytes(), &result) != nil || writer.Code != http.StatusOK || result.Status != "success" || result.Token == "" || writer.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("REST rotation contract failed: %d", writer.Code)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", http.Header{
		"Origin": {server.URL}, "Cookie": {"session_token=" + session},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: "admin:rotateClientToken", ID: 1, Params: map[string]any{"uuid": id}}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var response rpc.JsonRpcResponse
	if err := conn.ReadJSON(&response); err != nil || response.Error != nil {
		t.Fatalf("WebSocket rotation failed: %v", err)
	}
	if _, err := clients.GetClientUUIDByToken(result.Token); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("WebSocket rotation did not invalidate previous REST credential")
	}
}
