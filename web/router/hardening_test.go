package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agentruntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/upload"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
)

func TestSecurityAndResourceRegressions(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:hardening?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	user, err := accounts.CreateAccount("hardening"+strings.ReplaceAll(uuid.NewString(), "-", ""), "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	// Insert a legacy hash exactly as an existing installation would store it.
	sum := sha256.Sum256([]byte("test-only-password" + "06Wm4Jv1Hkxx"))
	if err := db.Model(&models.User{}).Where("uuid = ?", user.UUID).Update("passwd", base64.StdEncoding.EncodeToString(sum[:])).Error; err != nil {
		t.Fatal(err)
	}
	if id, ok, err := accounts.CheckPassword(user.Username, "test-only-password"); err != nil || !ok || id != user.UUID {
		t.Fatalf("legacy login failed: ok=%v err=%v", ok, err)
	}
	migrated, err := accounts.GetUserByUUID(user.UUID)
	if err != nil || !strings.HasPrefix(migrated.Passwd, "$argon2id$") {
		t.Fatalf("password not migrated: %v", err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	Register(r)
	r.GET("/asset.js", func(c *gin.Context) { c.Status(200) })
	srv := httptest.NewServer(r)
	defer srv.Close()

	t.Run("UploadStatsHTTPRequiresAdmin", func(t *testing.T) {
		const apiKey = "test-only-upload-stats-key"
		agentID := uuid.NewString()
		agentToken := "test-only-upload-stats-agent-token-" + agentID
		previousKey, err := config.GetAs[string](config.ApiKeyKey, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := config.Set(config.ApiKeyKey, apiKey); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := config.Set(config.ApiKeyKey, previousKey); err != nil {
				t.Error(err)
			}
		})
		if err := db.Create(&models.Client{UUID: agentID, Name: "test agent", Token: agentToken}).Error; err != nil {
			t.Fatal(err)
		}
		agentContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		agentContext.Request = httptest.NewRequest(http.MethodPost, "/api/rpc2?Authorization="+url.QueryEscape(agentToken), nil)
		if principal := api.IdentifyPrincipal(agentContext); principal.Type != rpc.PrincipalAgent || principal.ClientUUID != agentID {
			t.Fatalf("fixture token was not recognized as an agent: %+v", principal)
		}
		root := filepath.Join(t.TempDir(), "upload-root")
		store := &upload.Store{Root: root, MaxSize: 1024, FreeSpace: func(string) (int64, error) { return 1 << 60, nil }}
		previousStore := upload.DefaultStore
		upload.DefaultStore = store
		t.Cleanup(func() { upload.DefaultStore = previousStore })
		uploadSession, err := store.Init(upload.PurposeTheme, "example.zip", 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CleanupExpired(); err != nil {
			t.Fatal(err)
		}
		metadata := filepath.Join(uploadSession.Directory, "upload.json")
		if err := os.Remove(metadata); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(metadata, 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		store.RunCleanup(ctx)
		want := store.Stats()
		if !strings.Contains(want.LastError, root) || want.LastScan.IsZero() {
			t.Fatalf("test fixture lacks sensitive error and successful scan: %+v", want)
		}

		for _, tc := range []struct {
			name   string
			target string
			cookie *http.Cookie
			bearer string
			admin  bool
		}{
			{name: "anonymous", target: "/api/rpc2"},
			{name: "agent token", target: "/api/rpc2?Authorization=" + url.QueryEscape(agentToken)},
			{name: "admin session", target: "/api/rpc2", cookie: &http.Cookie{Name: "session_token", Value: session}, admin: true},
			{name: "admin API key", target: "/api/rpc2", bearer: "Bearer " + apiKey, admin: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"admin:getUploadStats","params":{}}`))
				req.Header.Set("Content-Type", "application/json")
				if tc.cookie != nil {
					req.AddCookie(tc.cookie)
				}
				if tc.bearer != "" {
					req.Header.Set("Authorization", tc.bearer)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("HTTP status = %d: %s", w.Code, w.Body.String())
				}
				var response struct {
					Version string            `json:"jsonrpc"`
					ID      int               `json:"id"`
					Result  json.RawMessage   `json:"result"`
					Error   *rpc.JsonRpcError `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Version != rpc.RPC_VERSION || response.ID != 1 {
					t.Fatalf("invalid JSON-RPC envelope: %s", w.Body.String())
				}
				if !tc.admin {
					if response.Error == nil || response.Error.Code != rpc.PermissionDenied || len(response.Result) != 0 {
						t.Fatalf("unauthorized caller accessed stats: %s", w.Body.String())
					}
					if strings.Contains(w.Body.String(), root) {
						t.Fatalf("unauthorized caller received a store path: %s", w.Body.String())
					}
					return
				}
				if response.Error != nil || len(response.Result) == 0 {
					t.Fatalf("admin did not receive stats: %s", w.Body.String())
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(response.Result, &fields); err != nil {
					t.Fatal(err)
				}
				if _, ok := fields["last_scan_duration_ns"]; !ok {
					t.Fatalf("admin result lacks scan duration in nanoseconds: %s", response.Result)
				}
				var got upload.CleanupStats
				if err := json.Unmarshal(response.Result, &got); err != nil {
					t.Fatal(err)
				}
				if got.LastError != want.LastError || got.LastScanDurationNS != want.LastScanDurationNS || !got.LastScan.Equal(want.LastScan) {
					t.Fatalf("admin snapshot mismatch: got=%+v want=%+v", got, want)
				}
			})
		}
	})

	t.Run("ExistingFactorCannotBeReplaced", func(t *testing.T) {
		old, _ := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "old"})
		next, _ := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "next"})
		if err := accounts.Enable2Fa(user.UUID, old.Secret()); err != nil {
			t.Fatal(err)
		}
		if err := accounts.Enable2Fa(user.UUID, next.Secret()); err == nil {
			t.Fatal("database allowed factor replacement")
		}
		code, _ := totp.GenerateCode(next.Secret(), time.Now())
		req := httptest.NewRequest("POST", "/api/admin/2fa/enable?code="+code, nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		req.AddCookie(&http.Cookie{Name: "2fa_secret", Value: next.Secret()})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		current, _ := accounts.GetUserByUUID(user.UUID)
		if w.Code == 200 || current.TwoFactor != old.Secret() {
			t.Fatal("forged enrollment replaced factor")
		}
	})
	t.Run("RebindRouteIsRegisteredAndNeedsNoFactorCode", func(t *testing.T) {
		// No session: 401 rather than 404 proves the route exists and is gated as an
		// admin route.
		req := httptest.NewRequest("POST", "/api/admin/2fa/rebind", strings.NewReader(`{"password":"test-only-password"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated rebind status %d, want 401", w.Code)
		}
		// With a session but no password the handler answers. A 2FA-code complaint
		// would mean the route sits behind RequireSensitive2FA, which is exactly the
		// dead end re-enrollment exists to remove.
		req = httptest.NewRequest("POST", "/api/admin/2fa/rebind", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("rebind without a password returned %d, want 400: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "2FA code is required") {
			t.Fatal("rebind is behind RequireSensitive2FA")
		}
	})
	t.Run("AnonymousBodyNotBuffered", func(t *testing.T) {
		body := &countingBody{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), 2<<20))}
		req := httptest.NewRequest("POST", "/api/admin/settings/", body)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 || body.count != 0 {
			t.Fatalf("status %d; consumed %d unauthorized bytes", w.Code, body.count)
		}
		req = httptest.NewRequest("POST", "/api/rpc2", bytes.NewReader(bytes.Repeat([]byte("x"), 2<<20)))
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 413 {
			t.Fatalf("oversized body status %d", w.Code)
		}
	})
	t.Run("SessionActivityCoalesced", func(t *testing.T) {
		activityToken := session + "-activity"
		if err := db.Create(&models.Session{UUID: user.UUID, Session: activityToken, Expires: time.Now().Add(time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		updates := 0
		name := "hardening:session-count"
		if err := db.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
			if tx.Statement.Table == "sessions" {
				mu.Lock()
				updates++
				mu.Unlock()
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer db.Callback().Update().Remove(name)
		for i := 0; i < 20; i++ {
			req := httptest.NewRequest("GET", "/asset.js", nil)
			req.AddCookie(&http.Cookie{Name: "session_token", Value: activityToken})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if updates != 1 {
			t.Fatalf("20 static requests caused %d writes", updates)
		}
		t.Logf("20 authenticated static requests: %d session writes", updates)
	})
	t.Run("RevokedWebSocketSession", func(t *testing.T) {
		headers := http.Header{"Origin": {srv.URL}, "Cookie": {"session_token=" + session}}
		ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/rpc2", headers)
		if err != nil {
			t.Fatal(err)
		}
		defer ws.Close()
		request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "admin:listClients", "params": map[string]any{}}
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := ws.WriteJSON(request); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := ws.ReadJSON(&result); err != nil {
			t.Fatal(err)
		}
		if result["error"] != nil {
			t.Fatalf("initial call: %v", result)
		}
		if err := accounts.DeleteSession(session); err != nil {
			t.Fatal(err)
		}
		_ = ws.WriteJSON(request)
		result = nil
		if err := ws.ReadJSON(&result); err == nil && result["error"] == nil {
			t.Fatal("revoked connection retained admin rights")
		}
	})
	t.Run("HiddenNodeRefreshesExistingConnection", func(t *testing.T) {
		id := uuid.NewString()
		if err := db.Create(&models.Client{UUID: id, Name: "test", Token: "test-only-token-" + id}).Error; err != nil {
			t.Fatal(err)
		}
		agentruntime.RecordReport(v2.Report{UUID: id})
		defer agentruntime.DeleteLatestReport(id)
		ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/clients", http.Header{"Origin": {srv.URL}})
		if err != nil {
			t.Fatal(err)
		}
		defer ws.Close()
		read := func() bool {
			t.Helper()
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			if err := ws.WriteMessage(websocket.TextMessage, []byte("get")); err != nil {
				t.Fatal(err)
			}
			var result struct{ Data struct{ Data map[string]any } }
			if err := ws.ReadJSON(&result); err != nil {
				t.Fatal(err)
			}
			_, ok := result.Data.Data[id]
			return ok
		}
		if !read() {
			t.Fatal("initial report missing")
		}
		if err := db.Model(&models.Client{}).Where("uuid = ?", id).Update("hidden", true).Error; err != nil {
			t.Fatal(err)
		}
		if read() {
			t.Fatal("hidden node leaked to existing guest socket")
		}
	})
	t.Run("WarmStatusAvoidsSQL", func(t *testing.T) {
		body := `{"jsonrpc":"2.0","id":1,"method":"common:getNodesLatestStatus","params":{"include_ping":false}}`
		call := func() {
			req := httptest.NewRequest("POST", "/api/rpc2", strings.NewReader(body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 || strings.Contains(w.Body.String(), `"error":`) {
				t.Fatalf("status response %s", w.Body.String())
			}
		}
		call()
		var mu sync.Mutex
		queries := 0
		name := "hardening:query-count"
		if err := db.Callback().Query().After("gorm:query").Register(name, func(*gorm.DB) { mu.Lock(); queries++; mu.Unlock() }); err != nil {
			t.Fatal(err)
		}
		defer db.Callback().Query().Remove(name)
		for i := 0; i < 10; i++ {
			call()
		}
		mu.Lock()
		defer mu.Unlock()
		if queries != 0 {
			t.Fatalf("10 warm status calls made %d SQL queries", queries)
		}
		t.Log("10 warm latest-status requests: 0 SQL queries")
	})
	t.Run("PrivateSiteBlocksIPProviders", func(t *testing.T) {
		if err := config.Set(config.PrivateSiteKey, true); err != nil {
			t.Fatal(err)
		}
		defer config.Set(config.PrivateSiteKey, false)
		req := httptest.NewRequest("GET", "/api/public/ip-info/v1/lookup?ip=8.8.8.8", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("private IP lookup status %d", w.Code)
		}
	})
	t.Run("DeletedAccountInvalidatesCachedSession", func(t *testing.T) {
		token := session + "-deleted"
		if err := db.Create(&models.Session{UUID: user.UUID, Session: token, Expires: time.Now().Add(time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := accounts.GetSession(token); err != nil {
			t.Fatal(err)
		}
		if err := accounts.DeleteAccountByUsername(user.Username); err != nil {
			t.Fatal(err)
		}
		if _, err := accounts.GetSession(token); err == nil {
			t.Fatal("deleted account retained cached access")
		}
	})
}

type countingBody struct {
	*bytes.Reader
	count int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.count += n
	return n, err
}
func (b *countingBody) Close() error { return nil }

var _ io.ReadCloser = (*countingBody)(nil)
