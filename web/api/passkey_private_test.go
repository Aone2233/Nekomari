package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/gin-gonic/gin"
)

func TestPrivateSitePasskeyBootstrapExactPaths(t *testing.T) {
	initWSOriginTestConfig(t)
	old, _ := config.GetAs[bool](config.PrivateSiteKey, false)
	if err := config.Set(config.PrivateSiteKey, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { config.Set(config.PrivateSiteKey, old) })
	r := gin.New()
	r.Use(PrivateSiteMiddleware())
	r.NoRoute(func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/komari-passkey/status", true},
		{"POST", "/api/komari-passkey/authentication/options", true},
		{"POST", "/api/komari-passkey/authentication/verify", true},
		{"POST", "/api/komari-passkey/status", false},
		{"GET", "/api/komari-passkey/authentication/options", false},
		{"GET", "/api/komari-passkey/credentials", false},
		{"POST", "/api/komari-passkey/registration/options", false},
		{"POST", "/api/komari-passkey/authentication/verify/extra", false},
		{"GET", "/api/komari-passkey/status-extra", false},
		{"POST", "/api/rpc2", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			want := http.StatusUnauthorized
			if tc.allowed {
				want = http.StatusNoContent
			}
			if w.Code != want {
				t.Fatalf("status %d, want %d", w.Code, want)
			}
		})
	}
}
