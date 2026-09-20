package accounts

import (
	"fmt"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CheckPassword 检查密码是否正确
//
// 密码正确返回用户 UUID 和 true；密码错误返回空字符串和 false。
// KDF 并发槽位排队超时返回 ErrPasswordBusy，调用方必须把它当作「稍后重试」，
// 不能当作密码错误 —— 否则并发登录时所有人（含管理员）都会看到「密码错误」。
func CheckPassword(username, passwd string) (uuid string, success bool, err error) {
	db := dbcore.GetDBInstance()
	var user models.User
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		// 静默处理错误，不显示日志
		return "", false, nil
	}
	ok, legacy, err := verifyPassword(user.Passwd, passwd)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	if legacy {
		// Best effort: a saturated KDF must not fail an otherwise valid login.
		if hashed, hashErr := hashPassword(passwd); hashErr == nil {
			// Compare-and-swap cannot overwrite a concurrent password reset.
			result := db.Model(&models.User{}).Where("uuid = ? AND passwd = ?", user.UUID, user.Passwd).Update("passwd", hashed)
			if result.Error == nil && result.RowsAffected == 0 {
				return "", false, nil
			}
		}
	}
	return user.UUID, true, nil
}

// ForceResetPassword 强制重置用户密码
func ForceResetPassword(username, passwd string) (err error) {
	db := dbcore.GetDBInstance()
	hashed, err := hashPassword(passwd)
	if err != nil {
		return err
	}
	result := db.Model(&models.User{}).Where("username = ?", username).Update("passwd", hashed)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("无法找到用户名")
	}
	return DeleteAllSessions()
}

func CreateAccount(username, passwd string) (user models.User, err error) {
	return CreateAccountWithDB(dbcore.GetDBInstance(), username, passwd)
}

func CreateAccountWithDB(db *gorm.DB, username, passwd string) (user models.User, err error) {
	hashedPassword, err := hashPassword(passwd)
	if err != nil {
		return models.User{}, err
	}
	user = models.User{
		UUID:     uuid.New().String(),
		Username: username,
		Passwd:   hashedPassword,
	}
	err = db.Create(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

func DeleteAccountByUsername(username string) (err error) {
	return DeleteAccountByUsernameWithDB(dbcore.GetDBInstance(), username)
}

func DeleteAccountByUsernameWithDB(db *gorm.DB, username string) (err error) {
	err = db.Where("username = ?", username).Delete(&models.User{}).Error
	if err != nil {
		return err
	}
	return nil
}

func GetUserByUUID(uuid string) (user models.User, err error) {
	db := dbcore.GetDBInstance()
	err = db.Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

// 通过 SSO 信息获取用户
func GetUserBySSO(ssoID string) (user models.User, err error) {
	db := dbcore.GetDBInstance()

	// 首先尝试查找已存在的用户
	err = db.Where("sso_id = ?", ssoID).First(&user).Error
	if err == nil {
		return user, nil
	}

	// 如果找不到用户，返回明确的错误信息
	return models.User{}, fmt.Errorf("用户不存在：%s", ssoID)
}

func BindingExternalAccount(uuid string, sso_id string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", sso_id).Error
	if err != nil {
		return err
	}
	return nil
}

func UnbindExternalAccount(uuid string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", "").Error
	if err != nil {
		return err
	}
	return nil
}

func UpdateUser(uuid string, name, password, sso_type *string) error {
	db := dbcore.GetDBInstance()
	// Check if user exists
	var existingUser models.User
	result := db.Where("uuid = ?", uuid).First(&existingUser)
	if result.Error != nil {
		return fmt.Errorf("user not found: %s", uuid)
	}
	updates := make(map[string]interface{})
	if name != nil {
		updates["username"] = *name
	}
	if password != nil {
		hashed, err := hashPassword(*password)
		if err != nil {
			return err
		}
		updates["passwd"] = hashed
	}
	if sso_type != nil {
		updates["sso_type"] = *sso_type
	}
	updates["updated_at"] = time.Now().UTC()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Updates(updates).Error
	if err != nil {
		return err
	}
	if password != nil {
		DeleteAllSessions()
	}
	return nil
}
