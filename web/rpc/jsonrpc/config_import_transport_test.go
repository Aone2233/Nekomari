package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
)

func TestDraftImportOriginalJSONThroughHTTPAndWebSocket(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "fixture-import-token", Name: "original", TrafficLimit: 1}).Error; err != nil {
		t.Fatal(err)
	}
	const method = "admin:draftRawImportFixture"
	if err := rpc.Register(method, func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		raw, err := rawImportDocument(req)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
		doc, err := configxport.Parse(raw)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
		plan, err := draftImportConfig(ctx, db, doc, false, func() error { return nil })
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
		return plan, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rpc.Unregister(method) })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	var sockets sync.WaitGroup
	engine.Any("/api/rpc2", func(c *gin.Context) {
		if api.IsWebSocketUpgrade(c) {
			sockets.Add(1)
			defer sockets.Done()
		}
		OnRpcRequest(c)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(func() { server.Close(); sockets.Wait() })
	account := createStepUpAccount(t, "raw-config-import-transport", "fixture-password")
	session, err := accounts.CreateSession(account.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	validDoc := fmt.Sprintf(`{"schema_version":3,"clients":[{"uuid":%q,"name":"updated","traffic_limit":9007199254740993}]}`, id)

	for _, transport := range []string{"post", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			var socket *websocket.Conn
			if transport == "websocket" {
				header := http.Header{"Origin": {server.URL}, "Cookie": {(&http.Cookie{Name: "session_token", Value: session}).String()}}
				var err error
				socket, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", header)
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
			}
			call := func(body string, admin bool) rpc.JsonRpcResponse {
				t.Helper()
				var response rpc.JsonRpcResponse
				if socket != nil {
					_ = socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := socket.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
						t.Fatal(err)
					}
					_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
					if err := socket.ReadJSON(&response); err != nil {
						t.Fatal(err)
					}
					return response
				}
				req, err := http.NewRequest(http.MethodPost, server.URL+"/api/rpc2", strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if admin {
					req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
				}
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
					t.Fatal(err)
				}
				return response
			}
			request := func(params string) string {
				return `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
			}
			readClient := func() models.Client {
				t.Helper()
				var client models.Client
				if err := db.Where("uuid = ?", id).Take(&client).Error; err != nil {
					t.Fatal(err)
				}
				return client
			}
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Updates(map[string]any{"name": "original", "traffic_limit": 1}).Error; err != nil {
				t.Fatal(err)
			}
			before := readClient()
			if transport == "post" {
				response := call(request(`{"document":`+validDoc+`}`), false)
				if response.Error == nil || response.Error.Code != rpc.PermissionDenied {
					t.Fatalf("anonymous request accepted: %+v", response)
				}
			}
			badText, _ := json.Marshal(fmt.Sprintf(`{"schema_version":3,"clients":[{"uuid":%q,"name":"bad","name":"worse"}]}`, id))
			invalid := []string{
				request(`{"document":{"schema_version":1,"schema_version":3}}`),
				request(`{"document_json":` + string(badText) + `}`),
				request(`{"document":` + validDoc + `,"document":` + validDoc + `}`),
				request(`{"document":` + validDoc + `,"document_json":"{}"}`),
				request(`{"Document":` + validDoc + `}`),
				request(`{"document":{"schema_version":3,"SCHEMA_VERSION":1}}`),
				request(`{"document":` + validDoc + `,"2fa_code":"one","2fa_code":"two"}`),
				request(`{"document_json":` + fmt.Sprintf("%q", `{"schema_version":3} {}`) + `}`),
			}
			for _, body := range invalid {
				response := call(body, true)
				if response.Error == nil || response.Error.Code != rpc.InvalidParams {
					t.Fatalf("ambiguous request accepted: %s: %+v", body, response)
				}
				if after := readClient(); !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected request modified client: %+v", after)
				}
			}
			for _, text := range []bool{false, true} {
				params := `{"document":` + validDoc + `}`
				if text {
					quoted, _ := json.Marshal(validDoc)
					params = `{"document_json":` + string(quoted) + `}`
				}
				response := call(request(params), true)
				if response.Error != nil {
					t.Fatalf("valid import rejected: %+v", response.Error)
				}
				if got := readClient(); got.Name != "updated" || got.TrafficLimit != 9007199254740993 {
					t.Fatalf("wrong imported value: name=%q traffic=%d", got.Name, got.TrafficLimit)
				}
			}
		})
	}
}

func TestReviewedDraftImportThroughHTTPAndWebSocket(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "review-transport-token", Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	const previewMethod = "admin:reviewedPreviewFixture"
	const importMethod = "admin:reviewedImportFixture"
	var reloadFailed atomic.Bool
	for method, preview := range map[string]bool{previewMethod: true, importMethod: false} {
		if err := rpc.Register(method, func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
			return reviewedDraftConfigImportRPC(ctx, req, db, preview, func() error {
				if reloadFailed.Load() {
					return fmt.Errorf("private schedule error")
				}
				return nil
			})
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rpc.Unregister(method) })
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	var sockets sync.WaitGroup
	engine.Any("/api/rpc2", func(c *gin.Context) {
		if api.IsWebSocketUpgrade(c) {
			sockets.Add(1)
			defer sockets.Done()
		}
		OnRpcRequest(c)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(func() { server.Close(); sockets.Wait() })
	account := createStepUpAccount(t, "review-import-transport", "fixture-password")
	session, err := accounts.CreateSession(account.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	doc := fmt.Sprintf(`{"schema_version":3,"clients":[{"uuid":%q,"name":"after","traffic_limit":9007199254740993}]}`, id)
	task := models.PingTask{Name: "original", Type: "tcp", Target: "127.0.0.1:80", Interval: 60, Clients: models.StringArray{id}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"post", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			reloadFailed.Store(false)
			if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("name", "original").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Updates(map[string]any{"name": "before", "group": "initial", "traffic_limit": 0}).Error; err != nil {
				t.Fatal(err)
			}
			var socket *websocket.Conn
			if transport == "websocket" {
				header := http.Header{"Origin": {server.URL}, "Cookie": {(&http.Cookie{Name: "session_token", Value: session}).String()}}
				var err error
				socket, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", header)
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
			}
			call := func(method string, params map[string]any, authenticated bool) rpc.JsonRpcResponse {
				t.Helper()
				body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
				if err != nil {
					t.Fatal(err)
				}
				var response rpc.JsonRpcResponse
				if socket != nil {
					_ = socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := socket.WriteMessage(websocket.TextMessage, body); err != nil {
						t.Fatal(err)
					}
					_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
					if err := socket.ReadJSON(&response); err != nil {
						t.Fatal(err)
					}
					return response
				}
				req, err := http.NewRequest(http.MethodPost, server.URL+"/api/rpc2", bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if authenticated {
					req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
				}
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
					t.Fatal(err)
				}
				return response
			}
			params := map[string]any{"document_json": doc}
			if socket == nil {
				if response := call(previewMethod, params, false); response.Error == nil || response.Error.Code != rpc.PermissionDenied {
					t.Fatalf("anonymous preview accepted: %+v", response)
				}
			}
			preview := func() string {
				t.Helper()
				response := call(previewMethod, params, true)
				data, ok := response.Result.(map[string]any)
				if response.Error != nil || !ok {
					t.Fatalf("preview failed: %+v", response)
				}
				token, _ := data["review_token"].(string)
				if len(token) != 64 {
					t.Fatalf("invalid preview token: %+v", data)
				}
				return token
			}
			token := preview()
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Update("group", "changed").Error; err != nil {
				t.Fatal(err)
			}
			if response := call(importMethod, map[string]any{"document_json": doc, "review_token": token}, true); response.Error == nil || response.Error.Code != rpc.InvalidParams {
				t.Fatalf("stale review accepted: %+v", response)
			}
			var client models.Client
			if err := db.Where("uuid = ?", id).Take(&client).Error; err != nil || client.Name != "before" {
				t.Fatalf("stale import wrote data: %+v err=%v", client, err)
			}
			token = preview()
			if response := call(importMethod, map[string]any{"document_json": doc, "review_token": token}, true); response.Error != nil {
				t.Fatalf("reviewed import failed: %+v", response)
			}
			if err := db.Where("uuid = ?", id).Take(&client).Error; err != nil || client.Name != "after" || client.TrafficLimit != 9007199254740993 {
				t.Fatalf("reviewed import lost data: %+v err=%v", client, err)
			}
			var audit models.Log
			if err := db.Where("uuid = ? AND message = ?", account.UUID, "import configuration").Order("id desc").Take(&audit).Error; err != nil {
				t.Fatalf("import audit missing or not attributed: %v", err)
			}
			taskDoc := fmt.Sprintf(`{"schema_version":3,"ping_tasks":[{"config_uid":%q,"name":"changed","clients":[%q]}]}`, task.ConfigUID, id)
			params["document_json"] = taskDoc
			token = preview()
			reloadFailed.Store(true)
			response := call(importMethod, map[string]any{"document_json": taskDoc, "review_token": token}, true)
			if response.Error == nil || response.Error.Code != rpc.Unavailable || strings.Contains(response.Error.Message, "private schedule error") {
				t.Fatalf("unsafe reload failure response: %+v", response)
			}
			data, ok := response.Error.Data.(map[string]any)
			if !ok || data["committed"] != true || data["error_key"] != "config_import_schedule_pending" {
				t.Fatalf("committed reload failure not identified: %+v", response.Error.Data)
			}
			var stored models.PingTask
			if err := db.First(&stored, task.Id).Error; err != nil || stored.Name != "changed" {
				t.Fatalf("committed task missing: %+v err=%v", stored, err)
			}
		})
	}
}

func TestReviewedDraftImportThroughSensitiveTransport(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "legacy-test-token", Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	// A fresh test process owns its registry; using the real method name lets
	// dispatchWithSensitive apply the production policy to the draft handler.
	if !rpc.Unregister("admin:importConfig") {
		t.Fatal("import method was not registered")
	}
	if err := rpc.Register("admin:importConfig", func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		return reviewedDraftConfigImportRPC(ctx, req, db, false, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		rpc.Unregister("admin:importConfig")
		if err := rpc.RegisterWithMeta("admin:importConfig", adminImportConfig, &rpc.MethodMeta{
			Name: "admin:importConfig", Summary: "Apply a document: create and update, never delete",
			Params:  []rpc.ParamMeta{{Name: "document", Type: "object", Required: true, Description: "A document from exportConfig"}},
			Returns: "ImportPlan, the same report the dry run produces for the same document",
		}); err != nil {
			t.Errorf("restore import handler: %v", err)
		}
	})
	account := createStepUpAccount(t, "draft-import-stepup-transport", "fixture-password")
	secret, _, err := accounts.Generate2Fa()
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Enable2Fa(account.UUID, secret); err != nil {
		t.Fatal(err)
	}
	session, err := accounts.CreateSession(account.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	var sockets sync.WaitGroup
	engine.Any("/api/rpc2", func(c *gin.Context) {
		if api.IsWebSocketUpgrade(c) {
			sockets.Add(1)
			defer sockets.Done()
		}
		OnRpcRequest(c)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(func() { server.Close(); sockets.Wait() })

	for _, transport := range []string{"post", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Update("name", "before").Error; err != nil {
				t.Fatal(err)
			}
			doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
			preview, err := reviewedDraftImportConfig(t.Context(), db, doc, true, "", func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			docJSON, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			var socket *websocket.Conn
			if transport == "websocket" {
				header := http.Header{"Origin": {server.URL}, "Cookie": {(&http.Cookie{Name: "session_token", Value: session}).String()}}
				socket, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", header)
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
			}
			call := func(code string, authenticated bool) rpc.JsonRpcResponse {
				t.Helper()
				params := map[string]any{"document_json": string(docJSON), "review_token": preview.ReviewToken}
				if code != "" {
					params["2fa_code"] = code
				}
				body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "admin:importConfig", "params": params})
				if err != nil {
					t.Fatal(err)
				}
				var response rpc.JsonRpcResponse
				if socket != nil {
					_ = socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := socket.WriteMessage(websocket.TextMessage, body); err != nil {
						t.Fatal(err)
					}
					_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
					if err := socket.ReadJSON(&response); err != nil {
						t.Fatal(err)
					}
					return response
				}
				req, err := http.NewRequest(http.MethodPost, server.URL+"/api/rpc2", bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if authenticated {
					req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
				}
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
					t.Fatal(err)
				}
				return response
			}
			for _, code := range []string{"", "not-a-code"} {
				if response := call(code, true); response.Error == nil || response.Error.Code != rpc.PermissionDenied {
					t.Fatalf("missing or invalid second factor accepted: %+v", response)
				}
			}
			if socket == nil {
				if response := call("", false); response.Error == nil || response.Error.Code != rpc.PermissionDenied {
					t.Fatalf("anonymous import accepted: %+v", response)
				}
			}
			var before models.Client
			var audits int64
			if err := db.Where("uuid = ?", id).Take(&before).Error; err != nil || before.Name != "before" {
				t.Fatalf("refused import wrote data: %+v err=%v", before, err)
			}
			if err := db.Model(&models.Log{}).Count(&audits).Error; err != nil || audits != int64(map[string]int{"post": 0, "websocket": 1}[transport]) {
				t.Fatalf("refused import wrote audit: count=%d err=%v", audits, err)
			}
			code, err := totp.GenerateCode(secret, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if response := call(code, true); response.Error != nil {
				t.Fatalf("valid second factor rejected: %+v", response.Error)
			}
			var after models.Client
			if err := db.Where("uuid = ?", id).Take(&after).Error; err != nil || after.Name != "after" {
				t.Fatalf("authorized import did not commit: %+v err=%v", after, err)
			}
			if err := db.Model(&models.Log{}).Where("uuid = ? AND message = ?", account.UUID, "import configuration").Count(&audits).Error; err != nil || audits != int64(map[string]int{"post": 1, "websocket": 2}[transport]) {
				t.Fatalf("authorized audit missing: count=%d err=%v", audits, err)
			}
		})
	}
}

func TestExportRecordAndTextPreserveLargeInteger(t *testing.T) {
	const limit = int64(9007199254740993)
	record, err := toRecord(models.Client{TrafficLimit: limit}, []string{"traffic_limit"})
	if err != nil || record["traffic_limit"] != json.Number("9007199254740993") {
		t.Fatalf("record rounded: %v err=%v", record, err)
	}
	text, jerr := encodeExportText(configxport.Document{SchemaVersion: 3, Clients: []map[string]any{record}}, nil)
	if jerr != nil || !strings.Contains(text.(string), `"traffic_limit": 9007199254740993`) {
		t.Fatalf("text export rounded: %v err=%v", text, jerr)
	}
	parsed, err := configxport.Parse([]byte(text.(string)))
	if err != nil || parsed.Clients[0]["traffic_limit"] != json.Number("9007199254740993") {
		t.Fatalf("round trip rounded: %+v err=%v", parsed, err)
	}
}

func TestSensitiveTextExportThroughHTTPAndWebSocket(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := uuid.NewString()
	const token = "test-text-export-token"
	if err := db.Create(&models.Client{UUID: id, Name: "text-export-fixture", Token: token,
		TrafficLimit: 9007199254740993}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&models.Client{}, "uuid = ?", id).Error })
	account := createStepUpAccount(t, "sensitive-text-export-transport", "fixture-password")
	secret, _, err := accounts.Generate2Fa()
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Enable2Fa(account.UUID, secret); err != nil {
		t.Fatal(err)
	}
	session, err := accounts.CreateSession(account.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	var sockets sync.WaitGroup
	engine.Any("/api/rpc2", func(c *gin.Context) {
		if api.IsWebSocketUpgrade(c) {
			sockets.Add(1)
			defer sockets.Done()
		}
		OnRpcRequest(c)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(func() { server.Close(); sockets.Wait() })
	const auditMessage = "export configuration including secrets"
	auditCount := func() int64 {
		t.Helper()
		var count int64
		if err := db.Model(&models.Log{}).Where("uuid = ? AND message = ?", account.UUID, auditMessage).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}
	for _, transport := range []string{"post", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			var socket *websocket.Conn
			if transport == "websocket" {
				header := http.Header{"Origin": {server.URL}, "Cookie": {(&http.Cookie{Name: "session_token", Value: session}).String()}}
				var err error
				socket, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", header)
				if err != nil {
					t.Fatal(err)
				}
				defer socket.Close()
			}
			call := func(code string, admin bool) rpc.JsonRpcResponse {
				t.Helper()
				body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1,
					"method": "admin:exportConfigSecretsText", "params": map[string]any{"2fa_code": code}})
				if err != nil {
					t.Fatal(err)
				}
				var response rpc.JsonRpcResponse
				if socket != nil {
					_ = socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := socket.WriteMessage(websocket.TextMessage, body); err != nil {
						t.Fatal(err)
					}
					_ = socket.SetReadDeadline(time.Now().Add(5 * time.Second))
					if err := socket.ReadJSON(&response); err != nil {
						t.Fatal(err)
					}
					return response
				}
				req, err := http.NewRequest(http.MethodPost, server.URL+"/api/rpc2", strings.NewReader(string(body)))
				if err != nil {
					t.Fatal(err)
				}
				if admin {
					req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
				}
				resp, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.Header.Get("Cache-Control") != "no-store" {
					t.Fatalf("sensitive response is cacheable: %q", resp.Header.Get("Cache-Control"))
				}
				if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
					t.Fatal(err)
				}
				return response
			}
			before := auditCount()
			for _, code := range []string{"", "not-a-code"} {
				response := call(code, true)
				if response.Error == nil || response.Error.Code != rpc.PermissionDenied {
					t.Fatalf("export without valid second factor accepted: %+v", response)
				}
			}
			if transport == "post" {
				response := call("", false)
				if response.Error == nil || response.Error.Code != rpc.PermissionDenied {
					t.Fatalf("anonymous export accepted: %+v", response)
				}
			}
			if auditCount() != before {
				t.Fatal("rejected export was audited as successful")
			}
			code, err := totp.GenerateCode(secret, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			response := call(code, true)
			text, ok := response.Result.(string)
			if response.Error != nil || !ok || !strings.Contains(text, token) || !strings.Contains(text, `"traffic_limit": 9007199254740993`) {
				t.Fatalf("authorized export failed or lost precision: error=%+v result type=%T", response.Error, response.Result)
			}
			if auditCount() != before+1 {
				t.Fatal("successful text export was not attributed to the user")
			}
		})
	}
	principal := rpc.NewScopedAPIKeyPrincipal(uuid.NewString(), rpc.APIKeyScopes())
	response := dispatchWithSensitive(t.Context(), nil, &rpc.ContextMeta{Principal: principal, Permission: principal.PrimaryRole()},
		&rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: "admin:exportConfigSecretsText", Params: map[string]any{}})
	if response.Error == nil || response.Error.Code != rpc.PermissionDenied {
		t.Fatalf("fully scoped API key exported credentials: %+v", response)
	}
}

// This uses the registered public methods and normal TestMain database. The
// separate fixture above injects a DB; neither touches a deployed panel.
func TestPublicReviewedImportThroughHTTP(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := uuid.NewString()
	node := models.Client{UUID: id, Token: "public-import-fixture-token", Name: "before"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&node).Error })
	account := createStepUpAccount(t, "public-config-import", "test-password")
	session, err := accounts.CreateSession(account.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	engine.POST("/api/rpc2", OnRpcRequest)
	server := httptest.NewServer(engine)
	defer server.Close()
	doc := fmt.Sprintf(`{"schema_version":3,"clients":[{"uuid":%q,"name":"after"}]}`, id)
	call := func(method string, params map[string]any, authenticated bool) rpc.JsonRpcResponse {
		t.Helper()
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/rpc2", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out rpc.JsonRpcResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	params := map[string]any{"document_json": doc}
	if response := call("admin:planConfigImport", params, false); response.Error == nil || response.Error.Code != rpc.PermissionDenied {
		t.Fatalf("anonymous preview accepted: %+v", response)
	}
	response := call("admin:planConfigImport", params, true)
	if response.Error != nil {
		t.Fatalf("public preview failed: %+v", response.Error)
	}
	plan, ok := response.Result.(map[string]any)
	if !ok {
		t.Fatalf("invalid preview: %#v", response.Result)
	}
	token, ok := plan["review_token"].(string)
	if !ok || len(token) != 64 {
		t.Fatalf("invalid review token: %#v", plan)
	}
	if response = call("admin:importConfig", map[string]any{"document_json": doc}, true); response.Error == nil {
		t.Fatal("unreviewed import succeeded")
	}
	if response = call("admin:importConfig", map[string]any{"document_json": doc, "review_token": token}, true); response.Error != nil {
		t.Fatalf("reviewed public import failed: %+v", response.Error)
	}
	var got models.Client
	if err := db.Where("uuid = ?", id).Take(&got).Error; err != nil || got.Name != "after" || got.Token != node.Token {
		t.Fatalf("public import state = %+v, err=%v", got, err)
	}
	if response = call("admin:importConfig", map[string]any{"document_json": doc, "review_token": token}, true); response.Error == nil {
		t.Fatal("stale public review succeeded")
	}
	var audit models.Log
	if err := db.Where("uuid = ? AND message = ?", account.UUID, "import configuration").Order("id DESC").Take(&audit).Error; err != nil {
		t.Fatalf("public import audit missing: %v", err)
	}
}
