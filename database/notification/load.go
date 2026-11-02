package notification

import (
	"fmt"
	"math"
	"time"

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
		// 基线模式下 Threshold 退化为下限，允许为 0。
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
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&notification).Error; err != nil {
			return err
		}
		// GORM applies the non-zero model default during Create, but zero is a
		// meaningful baseline floor. Never publish the temporary default.
		if mode == models.LoadThresholdModeBaseline && threshold == 0 {
			if err := tx.Model(&notification).Update("threshold", 0).Error; err != nil {
				return err
			}
		}
		return validateLoadSchedule(tx)
	}); err != nil {
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

// LoadNotificationEdit distinguishes omitted fields from explicit values;
// validation still restricts zeroes and retains legacy defaults where needed.
type LoadNotificationEdit struct {
	ID           uint       `json:"id"`
	Name         *string    `json:"name"`
	Clients      *[]string  `json:"clients"`
	Metric       *string    `json:"metric"`
	Threshold    *float32   `json:"threshold"`
	Ratio        *float32   `json:"ratio"`
	Interval     *int       `json:"interval"`
	Mode         *string    `json:"mode"`
	BaselineDays *int       `json:"baseline_days"`
	Multiplier   *float32   `json:"multiplier"`
	Tasks        *[]string  `json:"tasks"`
	LastNotified *time.Time `json:"last_notified"`
}

func EditLoadNotification(notifications []*LoadNotificationEdit) error {
	db := dbcore.GetDBInstance()
	if len(notifications) == 0 {
		return fmt.Errorf("notifications are required")
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		for _, edit := range notifications {
			if edit == nil || edit.ID == 0 {
				return fmt.Errorf("notification id is required")
			}
			var existing models.LoadNotification
			if err := tx.First(&existing, edit.ID).Error; err != nil {
				return err
			}
			// Validate behavior-changing inputs against the merged rule, without
			// preventing unrelated edits or disabling a legacy invalid rule.
			mode, metric, threshold := existing.Mode, existing.Metric, existing.Threshold
			if edit.Mode != nil {
				if *edit.Mode != models.LoadThresholdModeFixed && *edit.Mode != models.LoadThresholdModeBaseline {
					return fmt.Errorf("invalid threshold mode %q", *edit.Mode)
				}
				mode = *edit.Mode
			}
			if edit.Metric != nil {
				if *edit.Metric == "" {
					return fmt.Errorf("metric is required")
				}
				metric = *edit.Metric
			}
			if edit.Threshold != nil {
				threshold = *edit.Threshold
			}
			if (edit.Mode != nil || edit.Threshold != nil) && (math.IsNaN(float64(threshold)) || math.IsInf(float64(threshold), 0) ||
				(mode != models.LoadThresholdModeBaseline && threshold == 0)) {
				return fmt.Errorf("a non-zero fixed threshold is required")
			}
			if edit.Interval != nil && (*edit.Interval < 1 || *edit.Interval > 240) {
				return fmt.Errorf("interval must be between 1 and 240 minutes")
			}
			if edit.Ratio != nil && !notifier.IsPingAlertMetric(metric) && (*edit.Ratio <= 0 || *edit.Ratio > 1 || math.IsNaN(float64(*edit.Ratio))) {
				return fmt.Errorf("ratio must be greater than 0 and at most 1 for host metrics")
			}
			if edit.Metric != nil && !notifier.IsPingAlertMetric(metric) && edit.Ratio == nil && (existing.Ratio <= 0 || existing.Ratio > 1) {
				return fmt.Errorf("a valid ratio is required for host metrics")
			}
			updates := make(map[string]any)
			if edit.Name != nil && *edit.Name != "" {
				updates["name"] = *edit.Name
			}
			if edit.Clients != nil {
				updates["clients"] = models.StringArray(*edit.Clients)
			}
			if edit.Metric != nil {
				updates["metric"] = *edit.Metric
			}
			if edit.Threshold != nil {
				updates["threshold"] = *edit.Threshold
			}
			if edit.Ratio != nil && *edit.Ratio != 0 {
				updates["ratio"] = *edit.Ratio
			}
			if edit.Interval != nil {
				updates["interval"] = *edit.Interval
			}
			if edit.Mode != nil {
				updates["mode"] = *edit.Mode
			}
			if edit.BaselineDays != nil && *edit.BaselineDays != 0 {
				updates["baseline_days"] = *edit.BaselineDays
			}
			if edit.Multiplier != nil && *edit.Multiplier != 0 {
				updates["multiplier"] = *edit.Multiplier
			}
			if edit.Tasks != nil {
				updates["tasks"] = models.StringArray(*edit.Tasks)
			}
			if edit.LastNotified != nil {
				updates["last_notified"] = *edit.LastNotified
			}
			if len(updates) == 0 {
				return fmt.Errorf("notification update is empty")
			}
			result := tx.Model(&models.LoadNotification{}).Where("id = ?", edit.ID).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				// Some databases report changed rows rather than matched rows.
				var count int64
				if err := tx.Model(&models.LoadNotification{}).Where("id = ?", edit.ID).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					return gorm.ErrRecordNotFound
				}
			}
		}
		return validateLoadSchedule(tx)
	}); err != nil {
		return err
	}
	return ReloadLoadNotificationSchedule()
}

func validateLoadSchedule(tx *gorm.DB) error {
	var rules []models.LoadNotification
	if err := tx.Select("id", "interval").Find(&rules).Error; err != nil {
		return err
	}
	return notifier.ValidateLoadNotificationSchedule(rules)
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
