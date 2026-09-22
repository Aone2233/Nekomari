package public

import (
	"fmt"
	"net/http"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/oauth"
	"github.com/gin-gonic/gin"
)

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
