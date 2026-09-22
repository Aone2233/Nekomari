package public

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/oauth"
	"github.com/gin-gonic/gin"
)

// OAuth start admission.
//
// The shared state store caps *all* pending logins at 4096 entries. That bounds
// memory but not fairness: one caller looping /api/oauth can fill every slot,
// and every other start then gets the store's "retry later" until the 5-minute
// TTL drains. A per-IP token bucket in front of the store keeps that ceiling out
// of one caller's reach while leaving the global cap and its expiry untouched.
//
// The bucket reuses the login limiter's machinery — loginBucket.refill,
// retryAfter, the bounded map with least-recently-seen eviction and the TTL
// sweep — instead of adding a second rate-limiting idiom. Only the question
// differs: login throttling is a penalty recorded on failure, so it splits Allow
// from RecordFailure; admission control must charge every attempt, so admitIP
// consumes as it admits.
//
// 10 start attempts per IP, one token back every 30s: enough for a user who
// restarts after an expiry, a provider reload or a back button, and at most ~20
// states per 5-minute window per IP, so filling all 4096 slots needs on the
// order of 200 distinct sources instead of one.
const (
	oauthStartIPBurst         = 10.0
	oauthStartIPRefillSeconds = 30.0
)

// defaultOAuthStartLimiter is deliberately a separate instance from
// defaultLoginLimiter: draining the OAuth bucket must not throttle password
// logins, and a password flood must not block logins through a provider.
var defaultOAuthStartLimiter = newLoginLimiter()

// admitIP consumes one token for key from the IP dimension and reports how long
// until the next one is available.
//
// It is declared here, next to its only caller, rather than in login_limiter.go
// so the OAuth bucket shares that file's type, bounds and sweep rather than
// reimplementing them. peekLocked never creates a bucket, so a denied caller
// cannot grow the map; consumeLocked then creates one for an admitted caller
// through bucketLocked, which evicts the least recently seen entry when the map
// is full instead of failing closed — the failure mode that once turned a
// bounded login map into a permanent lockout.
func (l *loginLimiter) admitIP(key string, now time.Time, burst, refillSeconds float64) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)
	if retry := l.peekLocked(l.ips, key, now, burst, refillSeconds).retryAfter(refillSeconds); retry > 0 {
		return false, retry
	}
	l.consumeLocked(l.ips, key, now, burst, refillSeconds)
	return true, 0
}

// /api/oauth
func OAuth(c *gin.Context) {
	OAuthEnabled, _ := config.GetAs[bool](config.OAuthEnabledKey, false)
	if !OAuthEnabled {
		c.JSON(403, gin.H{"status": "error", "error": "OAuth is not enabled"})
		return
	}

	provider := oauth.CurrentProvider()
	if provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "OAuth provider unavailable"})
		return
	}

	// Admission runs before the provider call: GetAuthorizationURL allocates a
	// pending state and, for an aggregator provider such as QQ, makes an upstream
	// request, so a caller over budget must be turned away before either happens.
	// A denied start is not refunded when the store is full — the caller is the
	// one being bounded, and the login limiter refunds nothing either.
	if allowed, retryAfter := defaultOAuthStartLimiter.admitIP(c.ClientIP(), time.Now(), oauthStartIPBurst, oauthStartIPRefillSeconds); !allowed {
		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
		api.RespondError(c, http.StatusTooManyRequests, "Too many OAuth login attempts. Try again later.")
		return
	}

	authURL, state := provider.GetAuthorizationURL(utils.GetCallbackURL(c))
	if authURL == "" || state == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "OAuth authorization unavailable; retry later"})
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("oauth_state", state, 300, "/", "", utils.GetScheme(c) == "https", true)

	c.Redirect(302, authURL)
}

// /api/oauth_callback
func OAuthCallback(c *gin.Context) {
	enabled, err := config.GetAs[bool](config.OAuthEnabledKey, false)
	if err != nil || !enabled {
		c.JSON(http.StatusForbidden, gin.H{"status": "error", "error": "OAuth is not enabled"})
		return
	}
	provider := oauth.CurrentProvider()
	if provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "error": "OAuth provider unavailable"})
		return
	}

	// 验证state防止CSRF攻击
	state, _ := c.Cookie("oauth_state")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("oauth_state", "", -1, "/", "", utils.GetScheme(c) == "https", true)

	// 获取当前OAuth提供商名称
	if state == "" || state != c.Query("state") {
		c.JSON(400, gin.H{"status": "error", "error": "Invalid state"})
		return
	}

	queries := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			queries[key] = values[0]
		}
	}
	oidcUser, err := provider.OnCallback(c, state, queries, utils.GetCallbackURL(c))
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "error": "Failed to get user info: " + err.Error()})
		return
	}

	// ID作为SSO ID
	sso_id := fmt.Sprintf("%s_%s", provider.GetName(), oidcUser.UserId)

	// 如果cookie中有binding_external_account，说明是绑定外部账号
	// 否则是登录
	uuid, _ := c.Cookie("binding_external_account")
	c.SetCookie("binding_external_account", "", -1, "/", "", false, true)
	if uuid != "" {
		// 绑定外部账号
		session, _ := c.Cookie("session_token")
		user, err := accounts.GetUserBySession(session)
		if err != nil || user.UUID != uuid {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		err = accounts.BindingExternalAccount(user.UUID, sso_id)
		if err != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		auditlog.Log(c.ClientIP(), user.UUID, "bound external account (OAuth)"+fmt.Sprintf(",sso_id: %s", sso_id), "login")
		c.Redirect(302, "/admin/dashboard")
		return
	}

	// 尝试获取用户
	user, err := accounts.GetUserBySSO(sso_id)
	if err != nil {
		c.JSON(401, gin.H{
			"status":  "error",
			"message": "please log in and bind your external account first.",
		})
		return
	}

	// 创建会话
	session, err := accounts.CreateSession(user.UUID, sessionCookieMaxAge, c.Request.UserAgent(), c.ClientIP(), "oauth")
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "message": err.Error()})
		return
	}

	// 设置cookie并返回
	setSessionCookie(c, session, sessionCookieMaxAge)
	auditlog.Log(c.ClientIP(), user.UUID, "logged in (OAuth)", "login")
	c.Redirect(302, "/admin/dashboard")
}
