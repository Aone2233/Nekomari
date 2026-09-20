package accounts

import (
	"fmt"
	"image"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/pquerna/otp/totp"
)

var (
	// TwoFactorIssuer 是认证器 App 里显示的发行方名称。
	// 必须是 Nekomari 而不是上游的 Komari Monitor：这个字符串会被用户永久保存在
	// 认证器里，写错品牌等于让每个用户都看到上游的名字。
	TwoFactorIssuer = "Nekomari"
)

func Generate2Fa() (string, image.Image, error) {
	otp, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TwoFactorIssuer,
		AccountName: "komari",
	})
	if err != nil {
		return "", nil, err
	}
	img, err := otp.Image(250, 250)
	if err != nil {
		return "", nil, err
	}
	return otp.Secret(), img, nil
}

func Enable2Fa(uuid, secret string) error {
	db := dbcore.GetDBInstance()
	result := db.Model(&models.User{}).Where("uuid = ? AND (two_factor = '' OR two_factor IS NULL)", uuid).Update("two_factor", secret)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("2FA is already enabled; verify and disable the existing factor before replacing it")
	}
	return nil
}

// Replace2Fa overwrites an existing factor. It is only reachable from the
// password-authorised re-enrollment path (see Rebind2FA in web/api/admin): a lost
// authenticator cannot produce the code that /2fa/disable requires, so without a
// replacement path the only recovery was the disable2FA CLI command on the host.
// Callers must have established the account's identity first; this function
// deliberately performs no verification of its own.
//
// The update is conditional on the factor still being the one the re-enrollment
// started against, so a pending token cannot clobber a factor that changed while it
// was outstanding.
func Replace2Fa(uuid, secret, expected string) error {
	db := dbcore.GetDBInstance()
	result := db.Model(&models.User{}).Where("uuid = ? AND two_factor = ?", uuid, expected).Update("two_factor", secret)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("the factor changed while re-enrollment was pending; start again")
	}
	return nil
}

func Verify2Fa(uuid, code string) (bool, error) {
	db := dbcore.GetDBInstance()
	var user models.User
	err := db.Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return false, err
	}

	if user.TwoFactor == "" {
		return false, nil // 用户未启用2FA
	}

	valid := totp.Validate(code, user.TwoFactor)
	if !valid {
		return false, nil
	}

	return true, nil
}

func Disable2Fa(uuid string) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.User{}).Where("uuid = ?", uuid).Update("two_factor", "").Error
}
