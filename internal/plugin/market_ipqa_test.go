package plugin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// Execute with external networking disabled; never trigger a report or sync.
func TestKomariMarketIPQARouteAuthorization(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "ipqa-alert-report")
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
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ipqa-alert-report", true, true); err != nil {
		t.Fatal(err)
	}
	request := func(principal, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://panel.test/api/plugin/ipqa-alert-report/sync", strings.NewReader("{}"))
		req.Header.Set("X-Test-Principal", principal)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		engine.ServeHTTP(r, req)
		return r
	}
	for _, tc := range []struct{ principal, origin string }{{"", ""}, {"key", ""}, {"admin", "https://evil.test"}} {
		if r := request(tc.principal, tc.origin); r.Code != http.StatusForbidden {
			t.Fatalf("untrusted sync %q: %d %s", tc.principal, r.Code, r.Body.String())
		}
	}
}
