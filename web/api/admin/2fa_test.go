package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
)

func TestFactorEnrollmentIsServerIssuedBoundAndSingleUse(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:factor-enrollment?mode=memory&cache=shared"
	user, err := accounts.CreateAccount("enrollment", "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	contextFor := func(w *httptest.ResponseRecorder, req *http.Request) *gin.Context {
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Set("uuid", user.UUID)
		return c
	}
	w := httptest.NewRecorder()
	Generate2FA(contextFor(w, httptest.NewRequest("GET", "/generate", nil)))
	if w.Code != 200 {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("missing opaque enrollment cookie")
	}
	token := cookies[0].Value
	pendingFactors.Lock()
	pending := pendingFactors.entries[token]
	pendingFactors.Unlock()
	if pending.secret == "" || pending.secret == token {
		t.Fatal("cookie must not hold the factor secret")
	}
	code, err := totp.GenerateCode(pending.secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	attempt := func(uuid string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/enable?code="+code, nil)
		req.AddCookie(cookies[0])
		c := contextFor(w, req)
		c.Set("uuid", uuid)
		Enable2FA(c)
		return w
	}
	if attempt("other-user").Code == 200 {
		t.Fatal("cross-user token accepted")
	}
	if w := attempt(user.UUID); w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	if attempt(user.UUID).Code == 200 {
		t.Fatal("enrollment replay accepted")
	}
	enabled, err := accounts.GetUserByUUID(user.UUID)
	if err != nil || enabled.TwoFactor != pending.secret {
		t.Fatal("factor not persisted")
	}
}

// TestRebindReplacesFactorOnlyAfterPasswordAndCode is the recovery path for a lost
// authenticator: /disable needs a code the account cannot produce any more, so
// without this the only way back in was the disable2FA command on the server.
func TestRebindReplacesFactorOnlyAfterPasswordAndCode(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:factor-rebind?mode=memory&cache=shared"
	user, err := accounts.CreateAccount("rebind", "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	old, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Enable2Fa(user.UUID, old.Secret()); err != nil {
		t.Fatal(err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	rebind := func(password string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/rebind", strings.NewReader(`{"password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Set("uuid", user.UUID)
		Rebind2FA(c)
		return w
	}

	if w := rebind("wrong-password"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d %s", w.Code, w.Body.String())
	}
	if current, err := accounts.GetUserByUUID(user.UUID); err != nil || current.TwoFactor != old.Secret() {
		t.Fatal("a wrong password replaced the factor")
	}

	w := rebind("test-only-password")
	if w.Code != http.StatusOK {
		t.Fatalf("rebind: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("rebind issued %d cookies, want 1 enrollment token", len(cookies))
	}
	pendingFactors.Lock()
	pending, ok := pendingFactors.entries[cookies[0].Value]
	pendingFactors.Unlock()
	if !ok || !pending.replace {
		t.Fatal("re-enrollment was not recorded as a replacement")
	}
	if pending.secret == old.Secret() {
		t.Fatal("re-enrollment reused the stored factor")
	}
	// The account still has only its old factor: replacement happens on the code.
	if current, err := accounts.GetUserByUUID(user.UUID); err != nil || current.TwoFactor != old.Secret() {
		t.Fatal("rebind changed the factor before the new code was verified")
	}
	oldCode, err := totp.GenerateCode(old.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := accounts.Verify2Fa(user.UUID, oldCode); err != nil || !valid {
		t.Fatal("the existing factor stopped working before the replacement completed")
	}

	code, err := totp.GenerateCode(pending.secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/enable?code="+code, nil)
	req.AddCookie(cookies[0])
	req.AddCookie(&http.Cookie{Name: "session_token", Value: session})
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("uuid", user.UUID)
	Enable2FA(c)
	if w.Code != http.StatusOK {
		t.Fatalf("enable after rebind: %d %s", w.Code, w.Body.String())
	}
	current, err := accounts.GetUserByUUID(user.UUID)
	if err != nil || current.TwoFactor != pending.secret {
		t.Fatal("factor not replaced")
	}
	db := dbcore.GetDBInstance()
	if err := db.Where("session = ?", other).First(&models.Session{}).Error; err == nil {
		t.Fatal("another session survived the factor replacement")
	}
	if err := db.Where("session = ?", session).First(&models.Session{}).Error; err != nil {
		t.Fatal("the operator's own session was revoked")
	}
}
