package notification

import (
	"fmt"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/utils/notifier"
	"gorm.io/gorm"
)

func AddLoadNotification(clients []string, name string, metric string, threshold float32, ratio float32, interval int, mode string, baselineDays int, multiplier float32, tasks []string) (uint, error) {
	db := dbcore.GetDBInstance()
	if mode == "" {
		mode = models.LoadThresholdModeFixed
	}
	if mode != models.LoadThresholdModeFixed && mode != models.LoadThresholdModeBaseline {
		return 0, fmt.Errorf("invalid threshold mode %q", mode)
	}
	if mode == models.LoadThresholdModeBaseline {
		// 基线模式下 Threshold 退化为下限，允许为 0（例如「只要超过基线就报」）；
		// 但下限与基线都无效时规则没有意义，提前拦掉。
		if baselineDays <= 0 {
			baselineDays = 7
		}
		if multiplier <= 0 {
			multiplier = 3
		}
	}
	notification := models.LoadNotification{
		Clients:      clients,
		Name:         name,
		Metric:       metric,
		Threshold:    threshold,
		Ratio:        ratio,
		Interval:     interval,
		Mode:         mode,
		BaselineDays: baselineDays,
		Multiplier:   multiplier,
		Tasks:        models.StringArray(tasks),
	}
	if err := db.Create(&notification).Error; err != nil {
		return 0, err
	}

	return notification.Id, ReloadLoadNotificationSchedule()
}
func DeleteLoadNotification(id []uint) error {
	db := dbcore.GetDBInstance()
	result := db.Where("id IN ?", id).Delete(&models.LoadNotification{})
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return ReloadLoadNotificationSchedule()
}

func EditLoadNotification(notifications []*models.LoadNotification) error {
	db := dbcore.GetDBInstance()
	for _, notification := range notifications {
		result := db.Model(&models.LoadNotification{}).Where("id = ?", notification.Id).Updates(notification)
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
	}

	return ReloadLoadNotificationSchedule()
}

func GetAllLoadNotifications() ([]models.LoadNotification, error) {
	db := dbcore.GetDBInstance()
	var notifications []models.LoadNotification
	if err := db.Find(&notifications).Error; err != nil {
		return nil, err
	}
	return notifications, nil
}

func ReloadLoadNotificationSchedule() error {
	db := dbcore.GetDBInstance()
	var loadNotifications []models.LoadNotification
	if err := db.Find(&loadNotifications).Error; err != nil {
		return err
	}
	return notifier.ReloadLoadNotificationSchedule(loadNotifications)
}
