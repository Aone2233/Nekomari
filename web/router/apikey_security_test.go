package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/apikeys"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentruntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
)

func TestScopedAPIKeysAcrossHTTPAndWebSocket(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:scoped-key-router?mode=memory&cache=shared"
	t.Chdir(t.TempDir())
	db := dbcore.GetDBInstance()
	// Other test fixtures can rebind config; the real server always uses this DB.
	config.SetDb(db)
	user, err := accounts.CreateAccount("key-test-"+uuid.NewString(), "Test-Only-Key-Password1")
	if err != nil {
		t.Fatal(err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	node := models.Client{UUID: uuid.NewString(), Name: "key-test-node", Token: "test-only-node-token-" + uuid.NewString(), Hidden: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	agentruntime.RecordReport(v2.Report{UUID: node.UUID, CPU: v2.CPUReport{Usage: 12}})
	t.Cleanup(func() {
		agentruntime.DeleteLatestReport(node.UUID)
		db.Delete(&models.APIKey{}, "issued_by = ?", user.UUID)
		db.Delete(&models.Client{}, "uuid = ?", node.UUID)
		_ = accounts.DeleteSession(session)
		db.Delete(&models.User{}, "uuid = ?", user.UUID)
	})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	Register(router)
	// Installed plugins are not necessarily beneath /api/admin; fail closed in
	// the global identity boundary rather than relying on the admin route group.
	router.GET("/plugin-custom", func(c *gin.Context) { c.String(200, "privileged plugin response") })
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	request := func(verb, path, bearer string, payload any, cookie bool) (int, map[string]any, string) {
		t.Helper()
		body := ""
		if payload != nil {
			raw, _ := json.Marshal(payload)
			body = string(raw)
		}
		req := httptest.NewRequest(verb, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie {
			req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if strings.HasPrefix(path, "/api/admin/api-keys") && bearer == "" && cookie && response.Code == http.StatusOK && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("key-management response may be cached")
		}
		if path == "/api/rpc2" && response.Code == http.StatusOK && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("JSON-RPC response may cache a returned credential")
		}
		var data map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &data)
		return response.Code, data, response.Body.String()
	}
	rpcCall := func(method, bearer string, params any, cookie bool) map[string]any {
		t.Helper()
		status, data, _ := request("POST", "/api/rpc2", bearer, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, cookie)
		if status != http.StatusOK {
			t.Fatalf("RPC status for %s = %d", method, status)
		}
		return data
	}
	newKey := func(scopes ...string) (models.APIKey, string) {
		t.Helper()
		key, token, err := apikeys.Create(context.Background(), db, "test script", scopes, 1, user.UUID)
		if err != nil {
			t.Fatal(err)
		}
		return key, token
	}
	denied := func(data map[string]any) {
		t.Helper()
		error, ok := data["error"].(map[string]any)
		if !ok || error["code"] != float64(rpc.PermissionDenied) {
			t.Fatalf("expected a permission denial, got %v", data["error"])
		}
	}
	_, readToken := newKey(rpc.ScopeNodesRead)
	_, allToken := newKey(rpc.APIKeyScopes()...)

	t.Run("NoEscapeViaRPCOrDirectREST", func(t *testing.T) {
		for _, method := range []string{"admin:getSettings", "admin:editSettings", "admin:getSessions", "admin:dbQuery", "admin:dbExec",
			"admin:exportConfigSecrets", "admin:importConfig", "admin:createAPIKey", "admin:rotateAPIKey", "admin:revokeAPIKey", "admin:listAPIKeys", "admin:rotateClientToken",
			"admin:setPluginEnabled", "client:report"} {
			denied(rpcCall(method, allToken, map[string]any{"sql": "SELECT 1"}, false))
		}
		for _, method := range []string{"admin:exec", "admin:rollAgentUpdate", "admin:getClientToken", "admin:editClient", "admin:getTasks", "admin:getLogs"} {
			denied(rpcCall(method, readToken, map[string]any{"uuid": node.UUID}, true))
		}
		for _, route := range []struct{ verb, path string }{
			{"GET", "/api/admin/download/backup"}, {"POST", "/api/admin/upload/init"}, {"POST", "/api/admin/upload/merge"},
			{"GET", "/api/admin/theme/list"}, {"POST", "/api/admin/theme/market/install"}, {"GET", "/api/admin/2fa/generate"},
			{"POST", "/api/admin/2fa/enable"}, {"POST", "/api/admin/update/user"}, {"GET", "/api/admin/oauth2/bind"},
			{"GET", "/api/admin/client/" + node.UUID + "/terminal"}, {"GET", "/api/admin/client/" + node.UUID + "/file/download"},
			{"GET", "/api/admin/client/" + node.UUID + "/file/preview-token"}, {"GET", "/api/admin/plugin/list"},
			{"GET", "/api/admin/api-keys"}, {"POST", "/api/admin/api-keys"}, {"GET", "/plugin-custom"},
			{"POST", "/api/clients/v2/rpc"}, {"GET", "/api/admin/settings/"},
			{"POST", "/api/admin/client/" + node.UUID + "/token/rotate"},
		} {
			status, _, _ := request(route.verb, route.path, allToken, map[string]any{}, true)
			if status != http.StatusForbidden {
				t.Errorf("unreviewed route %s %s returned %d", route.verb, route.path, status)
			}
		}
		if status, _, _ := request("GET", "/api/admin/database/size", readToken, nil, false); status != http.StatusOK {
			t.Fatalf("monitoring REST bridge refused nodes:read: %d", status)
		}
		if status, _, _ := request("GET", "/api/admin/api-keys", "nk_invalid-credential", nil, true); status != http.StatusUnauthorized {
			t.Fatalf("invalid explicit bearer fell back to admin cookie: %d", status)
		}
	})

	t.Run("AgentCredentialsRemainCompatibleWithoutCookieEscalation", func(t *testing.T) {
		for _, target := range []string{"/api/rpc2?token=" + node.Token, "/api/rpc2?Authorization=" + node.Token} {
			status, data, _ := request("POST", target, "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "common:getMe"}, false)
			result, ok := data["result"].(map[string]any)
			if status != http.StatusOK || !ok || result["uuid"] != node.UUID || result["sso_id"] != "client" {
				t.Fatal("legacy query agent credential lost identity")
			}
		}
		status, data, _ := request("POST", "/api/rpc2", node.Token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "common:getMe"}, true)
		result, ok := data["result"].(map[string]any)
		if status != http.StatusOK || !ok || result["uuid"] != node.UUID || result["sso_id"] != "client" {
			t.Fatal("agent bearer inherited an administrator cookie")
		}
		denied(rpcCall("admin:getSettings", node.Token, nil, true))
	})

	t.Run("ClientReadsDoNotExposeTokensOrBroadenVisibility", func(t *testing.T) {
		for _, method := range []string{"admin:getClient", "admin:listClients", "common:getNodes"} {
			data := rpcCall(method, readToken, map[string]any{"uuid": node.UUID}, false)
			raw, _ := json.Marshal(data)
			if data["error"] != nil || strings.Contains(string(raw), node.Token) || strings.Contains(string(raw), `"token":`) {
				t.Fatalf("read-only method %s failed or exposed tokens", method)
			}
			if !strings.Contains(string(raw), node.Name) {
				t.Fatalf("nodes:read lost private node visibility via %s", method)
			}
		}
		_, unrelated := newKey(rpc.ScopeLogsRead)
		data := rpcCall("common:getNodes", unrelated, map[string]any{}, false)
		raw, _ := json.Marshal(data)
		if data["error"] != nil || strings.Contains(string(raw), node.UUID) || strings.Contains(string(raw), node.Token) {
			t.Fatal("unrelated scope disclosed private node data")
		}
		_, writeToken := newKey(rpc.ScopeNodesWrite)
		for _, field := range []string{"token", "Token", "target_version", "target_version_pending_clear", "ipv4", "version", "cpu_cores", "unknown"} {
			denied(rpcCall("admin:editClient", writeToken, map[string]any{"uuid": node.UUID, field: "untrusted-value"}, false))
			denied(rpcCall("admin:bulkEditClients", writeToken, map[string]any{"uuids": []string{node.UUID}, "update": map[string]any{field: "untrusted-value"}}, false))
		}
		if data := rpcCall("admin:editClient", writeToken, map[string]any{"uuid": node.UUID, "remark": "operator note"}, false); data["error"] != nil {
			t.Fatal("nodes:write cannot edit harmless metadata")
		}
		if data := rpcCall("admin:bulkEditClients", writeToken, map[string]any{"uuids": []string{node.UUID}, "update": map[string]any{"remark": "bulk operator note"}}, false); data["error"] != nil {
			t.Fatal("nodes:write cannot bulk edit harmless metadata")
		}
		data = rpcCall("admin:addClient", allToken, map[string]any{"name": "key-created-node"}, false)
		raw, _ = json.Marshal(data)
		if data["error"] != nil || strings.Contains(string(raw), `"token":`) {
			t.Fatal("addClient disclosed an unaudited agent credential")
		}
		if result, ok := data["result"].(map[string]any); ok {
			db.Delete(&models.Client{}, "uuid = ?", result["uuid"])
		}
		if status, _, raw := request("GET", "/api/admin/client/"+node.UUID+"/token", allToken, nil, false); status != 200 || !strings.Contains(raw, node.Token) {
			t.Fatal("explicit sensitive grant cannot use the audited REST token endpoint")
		}
	})

	t.Run("HermesStyleNodeManagement", func(t *testing.T) {
		_, managementToken := newKey(rpc.ScopeNodesRead, rpc.ScopeNodesWrite)
		for _, transport := range []string{"REST", "RPC"} {
			t.Run(transport, func(t *testing.T) {
				var created map[string]any
				if transport == "REST" {
					status, data, _ := request("POST", "/api/admin/client/add", managementToken, map[string]any{"name": "hermes-fixture"}, false)
					if status != http.StatusOK || data["status"] != "success" {
						t.Fatalf("node creation status = %d", status)
					}
					created = data
				} else {
					data := rpcCall("admin:addClient", managementToken, map[string]any{"name": "hermes-fixture"}, false)
					if data["error"] != nil {
						t.Fatal("node-management key could not create a node")
					}
					created, _ = data["result"].(map[string]any)
				}
				clientID, _ := created["uuid"].(string)
				if uuid.Validate(clientID) != nil {
					t.Fatal("node creation did not return a UUID")
				}
				t.Cleanup(func() { db.Delete(&models.Client{}, "uuid = ?", clientID) })
				if _, exposed := created["token"]; exposed {
					t.Fatal("node creation exposed an Agent token without a separate grant")
				}
				if transport == "REST" {
					status, data, _ := request("GET", "/api/admin/client/"+clientID, managementToken, nil, false)
					if status != http.StatusOK || data["uuid"] != clientID {
						t.Fatal("node-management key could not read the created node")
					}
					if _, exposed := data["token"]; exposed {
						t.Fatal("node read exposed an Agent token")
					}
					status, data, _ = request("POST", "/api/admin/client/"+clientID+"/edit", managementToken, map[string]any{"remark": "hermes-fixture-edit"}, false)
					if status != http.StatusOK || data["status"] != "success" {
						t.Fatal("node-management key could not edit metadata")
					}
				} else {
					data := rpcCall("admin:getClient", managementToken, map[string]any{"uuid": clientID}, false)
					read, _ := data["result"].(map[string]any)
					if data["error"] != nil || read["uuid"] != clientID {
						t.Fatal("node-management key could not read the created node")
					}
					if _, exposed := read["token"]; exposed {
						t.Fatal("node read exposed an Agent token")
					}
					if data := rpcCall("admin:editClient", managementToken, map[string]any{"uuid": clientID, "remark": "hermes-fixture-edit"}, false); data["error"] != nil {
						t.Fatal("node-management key could not edit metadata")
					}
				}
				var saved models.Client
				if err := db.Select("uuid", "remark").Where("uuid = ?", clientID).First(&saved).Error; err != nil || saved.Remark != "hermes-fixture-edit" {
					t.Fatal("metadata edit did not persist")
				}
				for _, method := range []string{"admin:getClientToken", "admin:rotateClientToken", "admin:exec", "admin:rollAgentUpdate", "admin:createAPIKey"} {
					denied(rpcCall(method, managementToken, map[string]any{"uuid": clientID}, true))
				}
				if status, _, _ := request("GET", "/api/admin/client/"+clientID+"/token", managementToken, nil, true); status != http.StatusForbidden {
					t.Fatalf("token read without grant returned %d", status)
				}
				if transport == "REST" {
					status, data, _ := request("POST", "/api/admin/client/"+clientID+"/remove", managementToken, map[string]any{}, false)
					if status != http.StatusOK || data["status"] != "success" {
						t.Fatal("node-management key could not remove its fixture node")
					}
				} else if data := rpcCall("admin:removeClient", managementToken, map[string]any{"uuid": clientID}, false); data["error"] != nil {
					t.Fatal("node-management key could not remove its fixture node")
				}
				var remaining int64
				if err := db.Model(&models.Client{}).Where("uuid = ?", clientID).Count(&remaining).Error; err != nil || remaining != 0 {
					t.Fatal("fixture node remained after removal")
				}
			})
		}
	})

	t.Run("ExplicitSensitiveGrantsAllowExecAndRolloutButNotEachOther", func(t *testing.T) {
		_, execToken := newKey(rpc.ScopeTasksExec)
		_, rolloutToken := newKey(rpc.ScopeAgentUpdate)
		agentruntime.KeepAlivePresence(node.UUID, 1, time.Minute)
		agentruntime.MarkV2Client(node.UUID)
		defer agentruntime.DeleteConnectedClients(node.UUID)
		defer agentruntime.SetPresence(node.UUID, 1, false)
		params := map[string]any{"command": "printf scoped-key-test", "clients": []string{node.UUID}}
		denied(rpcCall("admin:exec", rolloutToken, params, false))
		data := rpcCall("admin:exec", execToken, params, false)
		if data["error"] != nil {
			t.Fatalf("explicit tasks:exec grant refused: %v", data["error"])
		}
		if result, ok := data["result"].(map[string]any); ok {
			taskID := result["task_id"]
			t.Cleanup(func() {
				db.Delete(&models.TaskResult{}, "task_id = ?", taskID)
				db.Delete(&models.Task{}, "task_id = ?", taskID)
			})
		}
		rollParams := map[string]any{"uuids": []string{node.UUID}, "target": "v1.6.24", "dry_run": true}
		denied(rpcCall("admin:rollAgentUpdate", execToken, rollParams, false))
		if data := rpcCall("admin:rollAgentUpdate", rolloutToken, rollParams, false); data["error"] != nil {
			t.Fatalf("explicit agents:update grant refused: %v", data["error"])
		}
	})

	t.Run("SessionManagementStepupAndOneTimeSecret", func(t *testing.T) {
		status, data, _ := request("POST", "/api/admin/api-keys", "", map[string]any{"name": "managed key", "scopes": []string{rpc.ScopeNodesRead}, "expires_days": 10}, true)
		if status != 200 || data["status"] != "success" {
			t.Fatalf("session create failed: %d", status)
		}
		issued := data["data"].(map[string]any)
		managedToken := issued["token"].(string)
		id := issued["key"].(map[string]any)["id"].(string)
		status, _, raw := request("GET", "/api/admin/api-keys", "", nil, true)
		if status != 200 || strings.Contains(raw, managedToken) || strings.Contains(raw, "token_digest") || !strings.Contains(raw, id) {
			t.Fatal("key listing lost metadata or disclosed secrets")
		}
		secret, _, err := accounts.Generate2Fa()
		if err != nil {
			t.Fatal(err)
		}
		if err := accounts.Enable2Fa(user.UUID, secret); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"rotate", "revoke"} {
			if status, _, _ := request("POST", "/api/admin/api-keys/"+id+"/"+action, "", map[string]any{}, true); status == 200 {
				t.Fatalf("2FA-enabled session %s passed without step-up", action)
			}
		}
		if status, _, _ := request("POST", "/api/admin/api-keys", "", map[string]any{"name": "forbidden", "scopes": []string{rpc.ScopeTasksExec}}, true); status == 200 {
			t.Fatal("2FA-enabled session created a key without step-up")
		}
		code, err := totp.GenerateCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		status, data, _ = request("POST", "/api/admin/api-keys/"+id+"/rotate", "", map[string]any{"2fa_code": code}, true)
		if status != 200 || data["status"] != "success" {
			t.Fatal("session rotation with TOTP was refused")
		}
		replacement := data["data"].(map[string]any)["token"].(string)
		if _, err := apikeys.Authenticate(context.Background(), db, "Bearer "+managedToken); err == nil {
			t.Fatal("old managed token survived rotation")
		}
		if _, err := apikeys.Authenticate(context.Background(), db, "Bearer "+replacement); err != nil {
			t.Fatal("rotated managed token does not authenticate")
		}
		if err := accounts.Disable2Fa(user.UUID); err != nil {
			t.Fatal(err)
		}
		if status, _, _ := request("POST", "/api/admin/api-keys/"+id+"/revoke", "", map[string]any{}, true); status != 200 {
			t.Fatal("session revocation failed")
		}
		if _, err := apikeys.Authenticate(context.Background(), db, "Bearer "+replacement); err == nil {
			t.Fatal("revoked managed token authenticates")
		}
		if status, _, _ := request("POST", "/api/admin/settings/", "", map[string]any{"api_key": "retired-config-key"}, true); status != http.StatusBadRequest {
			t.Fatal("retired api_key setting was accepted")
		}
	})

	t.Run("LiveWebSocketRechecksScopeRevocationRotationAndExpiry", func(t *testing.T) {
		for _, action := range []string{"revoke", "rotate", "expire", "remove-scope"} {
			key, token := newKey(rpc.ScopeNodesRead)
			headers := http.Header{"Authorization": []string{"Bearer " + token}}
			connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", headers)
			if err != nil {
				t.Fatalf("RPC WebSocket handshake %s failed: %v", action, err)
			}
			if response != nil {
				_ = response.Body.Close()
			}
			call := func() (map[string]any, error) {
				if err := connection.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "admin:getClient", "params": map[string]any{"uuid": node.UUID}}); err != nil {
					return nil, err
				}
				_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
				var answer map[string]any
				err := connection.ReadJSON(&answer)
				return answer, err
			}
			if answer, err := call(); err != nil || answer["error"] != nil {
				t.Fatalf("initial WebSocket call %s failed: %v", action, err)
			}
			switch action {
			case "revoke":
				err = apikeys.Revoke(context.Background(), db, key.ID)
			case "rotate":
				_, _, err = apikeys.Rotate(context.Background(), db, key.ID)
			case "expire":
				err = db.Model(&models.APIKey{}).Where("id = ?", key.ID).Update("expires_at", time.Now().UTC().Add(-time.Second)).Error
			case "remove-scope":
				key.Scopes = []string{rpc.ScopeLogsRead}
				err = db.Save(&key).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			answer, err := call()
			if action == "remove-scope" {
				if err != nil {
					t.Fatalf("scope change should deny method, not fail transport: %v", err)
				}
				denied(answer)
			} else if err == nil {
				t.Fatalf("old WebSocket survived %s with response error %v", action, answer["error"])
			}
			_ = connection.Close()
		}
	})

	t.Run("StatusWebSocketNeverKeepsPrivateVisibilityAfterRevocation", func(t *testing.T) {
		key, token := newKey(rpc.ScopeNodesRead)
		headers := http.Header{"Authorization": []string{"Bearer " + token}}
		connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/clients", headers)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		if err := connection.WriteMessage(websocket.TextMessage, []byte("get "+node.UUID)); err != nil {
			t.Fatal(err)
		}
		_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, raw, err := connection.ReadMessage()
		if err != nil || !strings.Contains(string(raw), node.UUID) {
			t.Fatal("nodes:read cannot read the hidden node over status WebSocket")
		}
		if err := apikeys.Revoke(context.Background(), db, key.ID); err != nil {
			t.Fatal(err)
		}
		_ = connection.WriteMessage(websocket.TextMessage, []byte("get "+node.UUID))
		if _, _, err := connection.ReadMessage(); err == nil {
			t.Fatal("revoked status WebSocket retained access")
		}
	})
}
