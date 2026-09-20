package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/accounts"
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
