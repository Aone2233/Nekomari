package pingguard

import (
	"context"
	"errors"

	"github.com/Aone2233/nekomari/database/models"
	"gorm.io/gorm"
)

var ErrTaskReserved = errors.New("ping task ID is reserved after deletion")

// CheckNotReserved fails closed on database errors and on conflicting live and
// tombstone rows. Callers hold the task guard to order this with deletion.
func CheckNotReserved(ctx context.Context, db *gorm.DB, id uint) error {
	if ctx == nil || db == nil || id == 0 {
		return errors.New("invalid ping task reservation check")
	}
	var count int64
	if err := db.WithContext(ctx).Model(&models.PingTaskDeletion{}).Where("task_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrTaskReserved
	}
	return nil
}
