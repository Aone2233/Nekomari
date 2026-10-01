package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
)

// P1-7: binding an SSO identity to the account is a credential change, so it has
// to sit behind the same sensitive-operation gate as /2fa/disable and /task/exec.
// Before the fix both routes were reachable with a session cookie alone.
func TestOAuth2BindingRoutesRequireSensitive2FA(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:hardening?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)

	username := "oauth2bind" + strings.ReplaceAll(uuid.NewString(), "-", "")
	user, err := accounts.CreateAccount(username, "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounts.DeleteAccountByUsername(username) })

	factor, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "oauth2-bind"})
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Enable2Fa(user.UUID, factor.Secret()); err != nil {
		t.Fatal(err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware())
	Register(r)

	request := func(method, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("bind without a code is refused", func(t *testing.T) {
		w := request(http.MethodGet, "/api/admin/oauth2/bind")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("bind status %d, want 401: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "2FA code is required") {
			t.Fatalf("bind body = %s, want the sensitive-2FA refusal", w.Body.String())
		}
	})

	t.Run("unbind without a code is refused", func(t *testing.T) {
		w := request(http.MethodPost, "/api/admin/oauth2/unbind")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unbind status %d, want 401: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "2FA code is required") {
			t.Fatalf("unbind body = %s, want the sensitive-2FA refusal", w.Body.String())
		}
	})

	t.Run("bind with a code is allowed", func(t *testing.T) {
		code, err := totp.GenerateCode(factor.Secret(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		w := request(http.MethodGet, "/api/admin/oauth2/bind?2fa_code="+code)
		if w.Code != http.StatusFound {
			t.Fatalf("bind with a code status %d, want 302: %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Location") != "/api/oauth" {
			t.Fatalf("location %q", w.Header().Get("Location"))
		}
	})
}
