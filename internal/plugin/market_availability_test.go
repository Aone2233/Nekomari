package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gin-gonic/gin"
)

// Run in a network-isolated sandbox: this loads a third-party package.
func TestKomariMarketAvailabilityTracking(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "availability-history")
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
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = old })
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("availability-history", true, true); err != nil {
		t.Fatal(err)
	}
	info := &connection.ConnInfo{ID: 123, Path: "/api/clients/v2/rpc", ClientUUID: "fixture-node"}
	if denied, reason := global.OnConnect(info); denied {
		t.Fatalf("connection denied: %s", reason)
	}
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		engine.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	r := request("/api/plugin/availability-history/v1/summary?uuids=fixture-node")
	if r.Code != http.StatusOK {
		t.Fatalf("summary: %d %s; logs: %s", r.Code, r.Body.String(), GetLogs("availability-history"))
	}
	var summary struct {
		Nodes []struct {
			UUID         string `json:"uuid"`
			CurrentState string `json:"currentState"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Nodes) != 1 || summary.Nodes[0].UUID != "fixture-node" || summary.Nodes[0].CurrentState != "online" {
		t.Fatalf("summary missing observed online node: %s", r.Body.String())
	}
	if r := request("/api/plugin/availability-history/v1/events"); r.Code != http.StatusForbidden {
		t.Fatalf("anonymous event access: %d %s", r.Code, r.Body.String())
	}
	keyRequest := httptest.NewRequest(http.MethodGet, "/api/plugin/availability-history/v1/events", nil)
	keyRequest.Header.Set("X-Test-Key", "1")
	keyResponse := httptest.NewRecorder()
	engine.ServeHTTP(keyResponse, keyRequest)
	if keyResponse.Code != http.StatusForbidden {
		t.Fatalf("unscoped API key event access: %d", keyResponse.Code)
	}
	global.OnClose(info)
	if err := SetEnabled("availability-history", false, false); err != nil {
		t.Fatal(err)
	}
	if r := request("/api/plugin/availability-history/v1/summary"); r.Code != http.StatusNotFound {
		t.Fatalf("summary after unload: %d", r.Code)
	}
}
