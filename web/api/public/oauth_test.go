package public

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/web/oauth"
	"github.com/gin-gonic/gin"
)

func TestOAuthCallbackRejectsDisabledAndMismatchedState(t *testing.T) {
	previous, _ := config.GetAs[bool](config.OAuthEnabledKey, false)
	t.Cleanup(func() { _ = config.Set(config.OAuthEnabledKey, previous); _ = oauth.Shutdown() })
	if err := oauth.LoadProvider("qq", "{}"); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/callback", OAuthCallback)
	for _, tc := range []struct {
		name    string
		enabled bool
		query   string
		want    int
	}{
		{"disabled", false, "cookie-state", http.StatusForbidden},
		{"mismatch", true, "attacker-state", http.StatusBadRequest},
		{"missing", true, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := config.Set(config.OAuthEnabledKey, tc.enabled); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "/callback?state="+tc.query, nil)
			req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "cookie-state"})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, body %s", w.Code, w.Body.String())
			}
		})
	}
}
