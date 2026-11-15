package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// Run in an isolated network namespace; this loads a market package.
func TestKomariMarketForecastRouteAuthorization(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "net-forecast")
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Admin") == "1" {
			c.Set(principalContextKey, rpc.NewUserPrincipal("fixture-admin"))
		}
		c.Next()
	})
	Init(engine)
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = old })
	var edits atomic.Int32
	if err := rpc.Register("admin:editClient", func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		edits.Add(1)
		return map[string]any{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rpc.Unregister("admin:editClient") })
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("net-forecast", true, true); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, admin bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, "http://panel.test"+path, strings.NewReader(`{"uuid":"fixture","traffic_limit":1}`))
		if admin {
			req.Header.Set("X-Test-Admin", "1")
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		engine.ServeHTTP(r, req)
		return r
	}
	for _, path := range []string{"overview", "export.json", "debug", "debug.json"} {
		if r := request(http.MethodGet, "/api/plugin/net-forecast/"+path, false, ""); r.Code != http.StatusForbidden {
			t.Fatalf("anonymous %s: %d %s", path, r.Code, r.Body.String())
		}
	}
	for _, test := range []struct {
		admin  bool
		origin string
	}{{false, ""}, {true, "https://evil.test"}} {
		if r := request(http.MethodPost, "/api/plugin/net-forecast/update-quota", test.admin, test.origin); r.Code != http.StatusForbidden {
			t.Fatalf("untrusted update: %d %s", r.Code, r.Body.String())
		}
	}
	if edits.Load() != 0 {
		t.Fatal("unauthorized edit reached RPC")
	}
	if r := request(http.MethodPost, "/api/plugin/net-forecast/update-quota", true, "http://panel.test"); r.Code != http.StatusOK || edits.Load() != 1 {
		t.Fatalf("administrator update: %d %s; calls=%d; logs=%s", r.Code, r.Body.String(), edits.Load(), GetLogs("net-forecast"))
	}
}
