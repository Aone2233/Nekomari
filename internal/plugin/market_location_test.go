package plugin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/connection"
	"github.com/gin-gonic/gin"
)

// Run in a network-isolated sandbox: this loads a third-party package.
func TestKomariMarketLocationHooks(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "override-location")
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = old })
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfiguration("override-location", map[string]any{"match_mode": "uuid", "rules_text": "fixture-node=JP"}); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("override-location", true, true); err != nil {
		t.Fatal(err)
	}
	const payload = `{"uuid":"fixture-node","region":"DE"}`
	engine.GET("/api/fixture", func(c *gin.Context) { c.Data(http.StatusOK, "application/json", []byte(payload)) })
	r := httptest.NewRecorder()
	WrapHandler(engine).ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/fixture", nil))
	if !strings.Contains(r.Body.String(), `"region":"JP"`) {
		t.Fatalf("HTTP response was not rewritten: %s; logs: %s", r.Body.String(), GetLogs("override-location"))
	}
	info := &connection.ConnInfo{ID: 1, Path: "/api/rpc2"}
	_, data, drop := global.OnSend(info, 1, []byte(payload))
	if drop || !strings.Contains(string(data), `"region":"JP"`) {
		t.Fatalf("WebSocket frame was not rewritten: %q drop=%v", data, drop)
	}
	if err := SetEnabled("override-location", false, false); err != nil {
		t.Fatal(err)
	}
	_, data, drop = global.OnSend(info, 1, []byte(payload))
	if drop || string(data) != payload {
		t.Fatalf("hook remained after unload: %q drop=%v", data, drop)
	}
}
