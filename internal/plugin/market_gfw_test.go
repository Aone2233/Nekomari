package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// Mock task discovery and keep the network namespace isolated.
func TestKomariMarketGFWRouteAuthorization(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "komari-gfw")
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		switch c.GetHeader("X-Test-Principal") {
		case "admin":
			c.Set(principalContextKey, rpc.NewUserPrincipal("fixture"))
		case "key":
			c.Set(principalContextKey, rpc.NewAPIKeyPrincipal())
		}
		c.Next()
	})
	Init(engine)
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = old })
	if err := rpc.Register("admin:getAllPingTasks", func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) { return []any{}, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rpc.Unregister("admin:getAllPingTasks") })
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("komari-gfw", true, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		principal string
		want      int
	}{{"", 403}, {"key", 403}, {"admin", 200}} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/plugins/komari-gfw/overview", nil)
		req.Header.Set("X-Test-Principal", tc.principal)
		r := httptest.NewRecorder()
		engine.ServeHTTP(r, req)
		if r.Code != tc.want {
			t.Fatalf("%s: %d %s; logs: %s", tc.principal, r.Code, r.Body.String(), GetLogs("komari-gfw"))
		}
	}
}
