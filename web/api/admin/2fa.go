package admin

import (
	"image/png"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/utils"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/api/public"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
)

type pendingFactor struct {
	secret, user string
	expires      time.Time
	attempts     int
	// replace marks an enrollment started by an account that already holds a
	// factor, after the password step-up in Rebind2FA. Only such an enrollment may
	// overwrite the stored secret, and only while `expected` is still the stored one.
	replace  bool
	expected string
}

var pendingFactors = struct {
	sync.Mutex
	entries map[string]pendingFactor
}{entries: make(map[string]pendingFactor)}

func Generate2FA(c *gin.Context) {
	user, err := accounts.GetUserByUUID(c.GetString("uuid"))
	if err != nil || user.TwoFactor != "" {
		api.RespondError(c, 409, "Disable the existing factor before enrolling a new one")
		return
	}
	startEnrollment(c, user, false)
}

// Rebind2FA starts a replacement enrollment for an account that already has a
// factor. It is the recovery path for a lost authenticator: /2fa/disable requires
// a valid code, so without this the only way back in was the disable2FA command on
// the server. The account password is the step-up, and it is checked against the
// same buckets as login so a stolen session cannot use this endpoint to grind
// passwords for free. The current factor keeps working until the new code is
// verified, so a failed replacement leaves the account as it was.
func Rebind2FA(c *gin.Context) {
	uuid := c.GetString("uuid")
	var body struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Password) == "" {
		api.RespondError(c, http.StatusBadRequest, "Account password is required")
		return
	}
	user, err := accounts.GetUserByUUID(uuid)
	if err != nil {
		// No account behind this credential: an API key carries a placeholder uuid,
		// and a deleted account resolves to nothing. Either way there is no password
		// to check, so this is an authentication failure rather than a server fault.
		api.RespondError(c, http.StatusUnauthorized, "No account is associated with this credential")
		return
	}
	ip := c.ClientIP()
	account := strings.ToLower(strings.TrimSpace(user.Username))
	if allowed, retryAfter := public.AllowSensitivePasswordCheck(ip, account); !allowed {
		c.Header("Retry-After", strconv.Itoa(secondsAtLeastOne(retryAfter)))
		api.RespondError(c, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}
	_, ok, err := accounts.CheckPassword(user.Username, body.Password)
	if err != nil {
		// Saturation is not a credential answer: it must not consume an attempt.
		c.Header("Retry-After", strconv.Itoa(secondsAtLeastOne(accounts.PasswordRetryAfter())))
		api.RespondError(c, http.StatusServiceUnavailable, "Password verification is temporarily busy. Try again.")
		return
	}
	if !ok {
		public.RecordSensitivePasswordFailure(ip, account)
		auditlog.Log(ip, uuid, "2FA re-enrollment rejected: invalid password", "warn")
		api.RespondError(c, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	public.ResetSensitivePasswordFailures(account)
	if user.TwoFactor == "" {
		api.RespondError(c, http.StatusConflict, "2FA is not enabled; enroll a new factor instead")
		return
	}
	auditlog.Log(ip, uuid, "started 2FA re-enrollment", "warn")
	startEnrollment(c, user, true)
}

// startEnrollment issues a pending secret bound to one account and renders its QR
// code. The secret is only stored by Enable2FA, after the caller proves possession
// with a code.
func startEnrollment(c *gin.Context, user models.User, replace bool) {
	secret, img, err := accounts.Generate2Fa()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to generate 2FA: "+err.Error())
		return
	}
	token := utils.GenerateRandomString(32)
	pendingFactors.Lock()
	for key, pending := range pendingFactors.entries {
		if time.Now().After(pending.expires) || pending.user == user.UUID {
			delete(pendingFactors.entries, key)
		}
	}
	if len(pendingFactors.entries) >= 128 {
		pendingFactors.Unlock()
		api.RespondError(c, http.StatusTooManyRequests, "Too many pending enrollments")
		return
	}
	pendingFactors.entries[token] = pendingFactor{
		secret: secret, user: user.UUID, expires: time.Now().Add(10 * time.Minute),
		replace: replace, expected: user.TwoFactor,
	}
	pendingFactors.Unlock()
	c.SetCookie("2fa_secret", token, 600, "/", "", utils.GetScheme(c) == "https", true)
	c.Header("Content-Type", "image/png")
	c.Writer.WriteHeader(http.StatusOK)
	_ = png.Encode(c.Writer, img)
}

func Enable2FA(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	token, _ := c.Cookie("2fa_secret")
	code := c.Query("code")
	if token == "" || uuid == nil || code == "" {
		api.RespondError(c, http.StatusBadRequest, "2FA secret or code not provided")
		return
	}
	pendingFactors.Lock()
	pending, exists := pendingFactors.entries[token]
	if !exists || pending.user != uuid || time.Now().After(pending.expires) || pending.attempts >= 5 {
		pendingFactors.Unlock()
		api.RespondError(c, http.StatusBadRequest, "Expired or invalid 2FA enrollment")
		return
	}
	pending.attempts++
	pendingFactors.entries[token] = pending
	if !totp.Validate(code, pending.secret) {
		pendingFactors.Unlock()
		api.RespondError(c, http.StatusBadRequest, "Invalid 2FA code")
		return
	}
	delete(pendingFactors.entries, token)
	pendingFactors.Unlock()
	if pending.replace {
		if err := accounts.Replace2Fa(uuid.(string), pending.secret, pending.expected); err != nil {
			api.RespondError(c, http.StatusConflict, "Failed to replace 2FA: "+err.Error())
			return
		}
		// The replacement already happened; a failed revocation is reported but must
		// not present the enrollment as failed.
		keep, _ := c.Cookie("session_token")
		if keep == "" {
			// An API-key caller has no session cookie of its own to keep. Revoking
			// everything here would log the operator out of every session they hold,
			// which is not what "replace my factor" means.
			logger.Warnf("admin-api", "2FA replaced for %s without a session cookie; other sessions left alone", uuid)
		} else if err := accounts.DeleteOtherSessions(uuid.(string), keep); err != nil {
			logger.Errorf("admin-api", "Failed to revoke other sessions after 2FA replacement for %s: %v", uuid, err)
		}
		auditlog.Log(c.ClientIP(), uuid.(string), "replaced 2FA authenticator", "warn")
	} else if err := accounts.Enable2Fa(uuid.(string), pending.secret); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to enable 2FA: "+err.Error())
		return
	}
	c.SetCookie("2fa_secret", "", -1, "/", "", false, true)

	api.RespondSuccess(c, "2FA enabled successfully")
}

func Disable2FA(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	err := accounts.Disable2Fa(uuid.(string))
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to disable 2FA: "+err.Error())
		return
	}
	api.RespondSuccess(c, "")
}

// secondsAtLeastOne rounds a throttle delay up to whole seconds, never below one.
func secondsAtLeastOne(delay time.Duration) int {
	seconds := int(math.Ceil(delay.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
