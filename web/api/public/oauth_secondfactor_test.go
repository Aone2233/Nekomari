package public

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
)

// P1-7: an SSO identity is a proof of *who* the caller is, never a second factor.
// These tests pin the callback to the same TOTP requirement password login
// enforces (login.go:100-111), including its exact refusal messages, and pin that
// the requirement does not change anything for accounts without 2FA.

// bindOAuthTestAccountWith2FA creates an account bound to ssoID and, when secret
// is non-empty, enables TOTP for it. The account is removed with the test.
func bindOAuthTestAccountWith2FA(t *testing.T, username, ssoID, secret string) models.User {
	t.Helper()
	user, err := accounts.CreateAccount(username, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.BindingExternalAccount(user.UUID, ssoID); err != nil {
		t.Fatal(err)
	}
	if secret != "" {
		if err := accounts.Enable2Fa(user.UUID, secret); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = accounts.DeleteAccountByUsername(username)
		_ = accounts.DeleteAllSessions()
	})
	return user
}

// finishOAuthLogin runs a fresh start + callback pair, appending extra query to
// the callback. Every attempt needs its own start: the pending state is
// single-use, which is why a refused callback must be retried from /api/oauth.
func finishOAuthLogin(t *testing.T, router *gin.Engine, ip, extra string) *httptest.ResponseRecorder {
	t.Helper()
	state, _ := startOAuth(t, router, ip)
	query := "state=" + url.QueryEscape(state) + "&code=code"
	if extra != "" {
		query += "&" + extra
	}
	return oauthRequest(router, "/api/oauth_callback", query, ip, map[string]string{"oauth_state": state})
}

// requireNoSessionFor asserts that no session row exists for the account, so a
// refusal is proven to have stopped before CreateSession rather than only to have
// withheld the cookie.
func requireNoSessionFor(t *testing.T, uuid string) {
	t.Helper()
	sessions, err := accounts.GetAllSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.UUID == uuid {
			t.Fatalf("a session was created for %s without a valid second factor", uuid)
		}
	}
}

func TestOAuthCallbackRequiresSecondFactorFor2FAUsers(t *testing.T) {
	upstream := newFakeGenericUpstream(t, "second-factor-user")
	factor, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "second-factor"})
	if err != nil {
		t.Fatal(err)
	}
	user := bindOAuthTestAccountWith2FA(t, "oauth-second-factor-user", "generic_second-factor-user", factor.Secret())
	oauthTestInstance(t, "generic", upstream.config(t))
	isolateLoginLimiter(t)
	router := oauthRouter()

	t.Run("no code is refused", func(t *testing.T) {
		w := finishOAuthLogin(t, router, "203.0.113.7", "")
		if hasCookie(w, "session_token") {
			t.Fatalf("an SSO login created a session for a 2FA account without a second factor: %d %s", w.Code, w.Body.String())
		}
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "2FA code is required") {
			t.Fatalf("message = %s, want the same refusal password login uses", w.Body.String())
		}
		requireNoSessionFor(t, user.UUID)
	})

	t.Run("wrong code is refused", func(t *testing.T) {
		w := finishOAuthLogin(t, router, "203.0.113.7", "2fa_code=not-a-code")
		if hasCookie(w, "session_token") {
			t.Fatalf("a wrong TOTP code still produced a session: %d %s", w.Code, w.Body.String())
		}
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "Invalid 2FA code") {
			t.Fatalf("message = %s, want the same refusal password login uses", w.Body.String())
		}
		requireNoSessionFor(t, user.UUID)
	})

	t.Run("valid code logs in", func(t *testing.T) {
		code, err := totp.GenerateCode(factor.Secret(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		w := finishOAuthLogin(t, router, "203.0.113.7", "2fa_code="+code)
		if w.Code != http.StatusFound {
			t.Fatalf("a correct second factor was refused: %d %s", w.Code, w.Body.String())
		}
		if !hasCookie(w, "session_token") {
			t.Fatal("a correct second factor did not produce a session")
		}
		if w.Header().Get("Location") != "/admin/dashboard" {
			t.Fatalf("location %q", w.Header().Get("Location"))
		}
	})
}

// TestOAuthCallbackWithout2FAIsUnchanged is the control: an account without a
// factor keeps logging in through SSO exactly as before.
func TestOAuthCallbackWithout2FAIsUnchanged(t *testing.T) {
	upstream := newFakeGenericUpstream(t, "no-factor-user")
	bindOAuthTestAccountWith2FA(t, "oauth-no-factor-user", "generic_no-factor-user", "")
	oauthTestInstance(t, "generic", upstream.config(t))
	isolateLoginLimiter(t)
	router := oauthRouter()

	w := finishOAuthLogin(t, router, "203.0.113.7", "")
	if w.Code != http.StatusFound {
		t.Fatalf("callback status %d, body %s", w.Code, w.Body.String())
	}
	if !hasCookie(w, "session_token") {
		t.Fatal("SSO login without a factor created no session")
	}
	if w.Header().Get("Location") != "/admin/dashboard" {
		t.Fatalf("location %q", w.Header().Get("Location"))
	}
}
