package admin

import (
	"image/png"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
)

type pendingFactor struct {
	secret, user string
	expires      time.Time
	attempts     int
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
	secret, img, err := accounts.Generate2Fa()
	if err != nil {
		api.RespondError(c, 500, "Failed to generate 2FA: "+err.Error())
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
		api.RespondError(c, 429, "Too many pending enrollments")
		return
	}
	pendingFactors.entries[token] = pendingFactor{secret: secret, user: user.UUID, expires: time.Now().Add(10 * time.Minute)}
	pendingFactors.Unlock()
	c.SetCookie("2fa_secret", token, 600, "/", "", utils.GetScheme(c) == "https", true)
	c.Header("Content-Type", "image/png")
	c.Writer.WriteHeader(200)
	png.Encode(c.Writer, img)
}

func Enable2FA(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	token, _ := c.Cookie("2fa_secret")
	code := c.Query("code")
	if token == "" || uuid == nil || code == "" {
		api.RespondError(c, 400, "2FA secret or code not provided")
		return
	}
	pendingFactors.Lock()
	pending, exists := pendingFactors.entries[token]
	if !exists || pending.user != uuid || time.Now().After(pending.expires) || pending.attempts >= 5 {
		pendingFactors.Unlock()
		api.RespondError(c, 400, "Expired or invalid 2FA enrollment")
		return
	}
	pending.attempts++
	pendingFactors.entries[token] = pending
	if !totp.Validate(code, pending.secret) {
		pendingFactors.Unlock()
		api.RespondError(c, 400, "Invalid 2FA code")
		return
	}
	delete(pendingFactors.entries, token)
	pendingFactors.Unlock()
	err := accounts.Enable2Fa(uuid.(string), pending.secret)
	if err != nil {
		api.RespondError(c, 500, "Failed to enable 2FA: "+err.Error())
		return
	}
	c.SetCookie("2fa_secret", "", -1, "/", "", false, true)

	api.RespondSuccess(c, "2FA enabled successfully")
}

func Disable2FA(c *gin.Context) {
	uuid, _ := c.Get("uuid")
	err := accounts.Disable2Fa(uuid.(string))
	if err != nil {
		api.RespondError(c, 500, "Failed to disable 2FA: "+err.Error())
		return
	}
	api.RespondSuccess(c, "")
}
