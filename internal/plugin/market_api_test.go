package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// Run with no external network access: this loads an actual market package.
func TestKomariMarketAPIVisibility(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "api")
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Key") == "1" {
			c.Set(principalContextKey, rpc.NewAPIKeyPrincipal())
		}
		c.Next()
	})
	Init(engine)
	oldVersion := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = oldVersion })
	fixtures := map[string]any{
		"admin:listClients": []map[string]any{
			{"uuid": "visible", "name": "Visible", "token": "agent-secret", "ipv4": "192.0.2.1"},
			{"uuid": "hidden", "name": "Hidden", "hidden": true, "token": "hidden-secret"},
		},
		"common:getNodesLatestStatus": map[string]any{"visible": map[string]any{"online": true, "cpu": 27}},
	}
	for method, fixture := range fixtures {
		value := fixture
		if err := rpc.Register(method, func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) { return value, nil }); err != nil {
			t.Fatal(err)
		}
		name := method
		t.Cleanup(func() { rpc.Unregister(name) })
	}
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("api", true, true); err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		engine.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	list := request("/api/v1/nodes")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	var result struct {
		Count int `json:"count"`
		Nodes []struct {
			UUID   string `json:"uuid"`
			Online bool   `json:"online"`
			Live   struct {
				CPU float64 `json:"cpu"`
			} `json:"live"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || len(result.Nodes) != 1 || result.Nodes[0].UUID != "visible" || !result.Nodes[0].Online || result.Nodes[0].Live.CPU != 27 {
		t.Fatalf("unexpected public nodes: %s", list.Body.String())
	}
	if strings.Contains(list.Body.String(), "secret") || strings.Contains(list.Body.String(), "192.0.2.1") {
		t.Fatalf("private data leaked: %s", list.Body.String())
	}
	if r := request("/api/v1/nodes/hidden"); r.Code != http.StatusNotFound {
		t.Fatalf("hidden node: %d %s", r.Code, r.Body.String())
	}
	if r := request("/api/v1/nodes/visible"); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"uuid":"visible"`) {
		t.Fatalf("visible node: %d %s", r.Code, r.Body.String())
	}
	keyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	keyRequest.Header.Set("X-Test-Key", "1")
	keyResponse := httptest.NewRecorder()
	engine.ServeHTTP(keyResponse, keyRequest)
	if keyResponse.Code != http.StatusForbidden {
		t.Fatalf("unscoped API key access: %d %s", keyResponse.Code, keyResponse.Body.String())
	}
	if err := SetEnabled("api", false, false); err != nil {
		t.Fatal(err)
	}
	if r := request("/api/v1/nodes"); r.Code != http.StatusNotFound {
		t.Fatalf("route after unload: %d", r.Code)
	}
}
