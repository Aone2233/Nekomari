package public

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/api"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TwoFa    string `json:"2fa_code"`
}

const sessionCookieMaxAge = 2592000

func setSessionCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "session_token",
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   utils.GetScheme(c) == "https",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// retryAfterSeconds rounds a throttle delay up to whole seconds for Retry-After.
func retryAfterSeconds(delay time.Duration) int {
	seconds := int(math.Ceil(delay.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func Login(c *gin.Context) {
	DisablePasswordLogin, _ := config.GetAs[bool](config.DisablePasswordLoginKey, false)
	if DisablePasswordLogin {
		api.RespondError(c, http.StatusForbidden, "Password login is disabled")
		return
	}

	// 登录请求体只有几个字段；设上限，避免无界读取被用来打内存。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	var data LoginRequest
	err = json.Unmarshal(bodyBytes, &data)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	if data.Username == "" || data.Password == "" {
		api.RespondError(c, http.StatusBadRequest, "Invalid request body: Username and password are required")
		return
	}

	ip := c.ClientIP()
	account := strings.ToLower(strings.TrimSpace(data.Username))
	if allowed, retryAfter := defaultLoginLimiter.Allow(ip, account, time.Now()); !allowed {
		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
		api.RespondError(c, http.StatusTooManyRequests, "Too many login attempts. Try again later.")
		return
	}

	uuid, success, err := accounts.CheckPassword(data.Username, data.Password)
	if err != nil {
		// Saturation is not a credential answer. Reporting it as one both lied to
		// the caller and consumed a login attempt, so a burst of concurrent logins
		// drove every account — the administrator's included — toward lockout with
		// a correct password.
		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(accounts.PasswordRetryAfter())))
		api.RespondError(c, http.StatusServiceUnavailable, "Password verification is temporarily busy. Try again.")
		return
	}
	if !success {
		defaultLoginLimiter.RecordFailure(ip, account, time.Now())
		api.RespondError(c, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	// 2FA
	user, _ := accounts.GetUserByUUID(uuid)
	if user.TwoFactor != "" { // 开启了2FA
		if data.TwoFa == "" {
			defaultLoginLimiter.RecordFailure(ip, account, time.Now())
			api.RespondError(c, http.StatusUnauthorized, "2FA code is required")
			return
		}
		if ok, err := accounts.Verify2Fa(uuid, data.TwoFa); err != nil || !ok {
			defaultLoginLimiter.RecordFailure(ip, account, time.Now())
			api.RespondError(c, http.StatusUnauthorized, "Invalid 2FA code")
			return
		}
	}
	defaultLoginLimiter.Reset(account)
	// Create session
	session, err := accounts.CreateSession(uuid, sessionCookieMaxAge, c.Request.UserAgent(), c.ClientIP(), "password")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to create session: "+err.Error())
		return
	}
	setSessionCookie(c, session, sessionCookieMaxAge)
	auditlog.Log(c.ClientIP(), uuid, "logged in (password)", "login")
	api.RespondSuccess(c, gin.H{"set-cookie": gin.H{"session_token": session}})
}
func Logout(c *gin.Context) {
	session, _ := c.Cookie("session_token")
	accounts.DeleteSession(session)
	setSessionCookie(c, "", -1)
	auditlog.Log(c.ClientIP(), "", "logged out", "logout")
	c.Redirect(302, "/")
}
