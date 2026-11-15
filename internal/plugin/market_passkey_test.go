package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// Uses a virtual account and an empty credential store in a network namespace.
func TestKomariMarketPasskeyAnonymousEndpoints(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "komari-passkey")
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = old })
	if err := rpc.Register("admin:dbQuery", func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		return map[string]any{"rows": [][]string{{"fixture-user", "fixture"}}, "row_count": 1}, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rpc.Unregister("admin:dbQuery") })
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("komari-passkey", true, true); err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		engine.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	status := get("/api/komari-passkey/status?origin=https%3A%2F%2Fpanel.example.test")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"service_available":true`) || !strings.Contains(status.Body.String(), `"enabled":false`) {
		t.Fatalf("status: %d %s; logs: %s", status.Code, status.Body.String(), GetLogs("komari-passkey"))
	}
	credentials := get("/api/komari-passkey/credentials")
	if credentials.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous credentials: %d %s", credentials.Code, credentials.Body.String())
	}
}
