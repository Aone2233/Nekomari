package plugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// Mock all external effects; never send a real notification or create a real session.
func TestKomariMarketOTPLogin(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "komari-otp-login")
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = old })
	codes := make(chan string, 2)
	var sessions atomic.Int32
	handlers := map[string]rpc.Handler{
		"admin:dbQuery": func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
			return map[string]any{"rows": [][]string{{"fixture-user", "fixture"}}}, nil
		},
		"admin:dbExec": func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
			sessions.Add(1)
			return nil, nil
		},
		"admin:sendNotification": func(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
			var p struct {
				Event struct {
					Message string `json:"message"`
				} `json:"event"`
			}
			_ = req.BindParams(&p)
			if match := regexp.MustCompile(`验证码：([A-Z0-9]{8})`).FindStringSubmatch(p.Event.Message); len(match) == 2 {
				codes <- match[1]
			}
			return nil, nil
		},
	}
	for method, handler := range handlers {
		if err := rpc.Register(method, handler); err != nil {
			t.Fatal(err)
		}
		name := method
		t.Cleanup(func() { rpc.Unregister(name) })
	}
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("komari-otp-login", true, true); err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		engine.ServeHTTP(r, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return r
	}
	if r := post("/api/komari-otp-login/send", `{"username":"fixture"}`); r.Code != http.StatusOK {
		t.Fatalf("send: %d %s; logs %s", r.Code, r.Body.String(), GetLogs("komari-otp-login"))
	}
	var code string
	select {
	case code = <-codes:
	case <-time.After(time.Second):
		t.Fatal("no code delivered to the mock notification handler")
	}
	wrong := "00000000"
	if code == wrong {
		wrong = "11111111"
	}
	if r := post("/api/komari-otp-login/verify", fmt.Sprintf(`{"username":"fixture","code":%q}`, wrong)); r.Code != http.StatusUnauthorized || sessions.Load() != 0 {
		t.Fatalf("wrong code: %d %s", r.Code, r.Body.String())
	}
	r := post("/api/komari-otp-login/verify", fmt.Sprintf(`{"username":"fixture","code":%q}`, code))
	if r.Code != http.StatusOK || sessions.Load() != 1 || !strings.Contains(r.Header().Get("Set-Cookie"), "session_token=") {
		t.Fatalf("verify: %d %s sessions=%d", r.Code, r.Body.String(), sessions.Load())
	}
	if r := post("/api/komari-otp-login/verify", fmt.Sprintf(`{"username":"fixture","code":%q}`, code)); r.Code != http.StatusBadRequest || sessions.Load() != 1 {
		t.Fatalf("reuse: %d %s", r.Code, r.Body.String())
	}
}
