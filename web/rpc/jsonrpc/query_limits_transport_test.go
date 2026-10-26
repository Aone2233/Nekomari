package jsonrpc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestQueryLimitsThroughHTTPAndWebSocket(t *testing.T) {
	fixture := seedCommonRecordsFixture(t)
	user, err := accounts.CreateAccount("query-transport-"+strconv.FormatInt(time.Now().UnixNano(), 10), "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	msgType := fixture.visible + "-logs"
	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Log{Message: "query-transport-log", MsgType: msgType}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Where("msg_type = ?", msgType).Delete(&models.Log{}).Error })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	var socketHandlers sync.WaitGroup
	engine.Any("/api/rpc2", func(c *gin.Context) {
		if api.IsWebSocketUpgrade(c) {
			socketHandlers.Add(1)
			defer socketHandlers.Done()
		}
		OnRpcRequest(c)
	})
	server := httptest.NewServer(engine)
	t.Cleanup(func() {
		server.Close()
		socketHandlers.Wait()
	})

	for _, transport := range []string{"http", "websocket"} {
		for _, admin := range []bool{false, true} {
			role := "guest"
			if admin {
				role = "session"
			}
			t.Run(transport+"/"+role, func(t *testing.T) {
				var socket *websocket.Conn
				if transport == "websocket" {
					header := http.Header{"Origin": {server.URL}}
					if admin {
						header.Set("Cookie", (&http.Cookie{Name: "session_token", Value: session}).String())
					}
					var err error
					socket, _, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", header)
					if err != nil {
						t.Fatal(err)
					}
					defer socket.Close()
				}
				call := func(t *testing.T, method string, params any) *rpc.JsonRpcResponse {
					t.Helper()
					request := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, ID: 1, Method: method, Params: params}
					var response rpc.JsonRpcResponse
					if socket != nil {
						if err := socket.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
							t.Fatal(err)
						}
						if err := socket.WriteJSON(request); err != nil {
							t.Fatal(err)
						}
						if err := socket.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
							t.Fatal(err)
						}
						if err := socket.ReadJSON(&response); err != nil {
							t.Fatal(err)
						}
						return &response
					}
					body, err := json.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					httpRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/rpc2", bytes.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					httpRequest.Header.Set("Content-Type", "application/json")
					if admin {
						httpRequest.AddCookie(&http.Cookie{Name: "session_token", Value: session})
					}
					httpResponse, err := server.Client().Do(httpRequest)
					if err != nil {
						t.Fatal(err)
					}
					defer httpResponse.Body.Close()
					if httpResponse.StatusCode != http.StatusOK {
						t.Fatalf("RPC HTTP status = %d", httpResponse.StatusCode)
					}
					if err := json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
						t.Fatal(err)
					}
					return &response
				}

				for _, kind := range []string{"load", "ping"} {
					for _, unsafe := range []struct {
						name   string
						params map[string]any
					}{
						{"hours above ceiling", map[string]any{"hours": 366*24 + 1}},
						{"duration overflow", map[string]any{"hours": int(^uint(0) >> 1)}},
						{"long explicit window", map[string]any{"start": fixture.end.Add(-367 * 24 * time.Hour), "end": fixture.end}},
						{"invalid point target", map[string]any{"maxCount": -2}},
					} {
						t.Run(kind+"/"+unsafe.name, func(t *testing.T) {
							unsafe.params["type"] = kind
							unsafe.params["uuid"] = fixture.visible
							response := call(t, "common:getRecords", unsafe.params)
							if response.Error == nil || response.Error.Code != rpc.InvalidParams {
								t.Fatalf("unsafe query returned %s", mustMarshalJSON(t, response))
							}
						})
					}
					for _, node := range []struct {
						name string
						uuid string
					}{
						{"visible", fixture.visible}, {"hidden", fixture.hidden},
						{"deleted", fixture.deleted}, {"unknown", fixture.visible + "-unknown"},
					} {
						t.Run(kind+"/"+node.name, func(t *testing.T) {
							response := call(t, "common:getRecords", fixture.request(kind, node.uuid).Params)
							if response.Error != nil {
								t.Fatal(response.Error)
							}
							result := response.Result.(map[string]any)
							want := float64(0)
							if node.name == "visible" || (admin && node.name != "unknown") {
								want = 2
							}
							if result["count"] != want {
								t.Fatalf("count = %v, want %v: %s", result["count"], want, mustMarshalJSON(t, result))
							}
							if want == 0 {
								unknown := call(t, "common:getRecords", fixture.request(kind, fixture.visible+"-unknown").Params)
								if mustMarshalJSON(t, result) != mustMarshalJSON(t, unknown.Result) {
									t.Fatalf("unreadable history differs from unknown: %s", mustMarshalJSON(t, response))
								}
							}
						})
					}
				}
				for _, limit := range []string{"1000", "1001"} {
					t.Run("logs/"+limit, func(t *testing.T) {
						response := call(t, "admin:getLogs", map[string]any{"limit": limit, "msg_type": msgType})
						wantError := 0
						if !admin {
							wantError = rpc.PermissionDenied
						} else if limit == "1001" {
							wantError = rpc.InvalidParams
						}
						if wantError != 0 {
							if response.Error == nil || response.Error.Code != wantError {
								t.Fatalf("logs returned %s, want error %d", mustMarshalJSON(t, response), wantError)
							}
							return
						}
						if response.Error != nil {
							t.Fatal(response.Error)
						}
						result := response.Result.(map[string]any)
						if result["total"] != float64(1) || len(result["logs"].([]any)) != 1 {
							t.Fatalf("valid CSV page/filter returned %s", mustMarshalJSON(t, result))
						}
					})
				}
			})
		}
	}
}
