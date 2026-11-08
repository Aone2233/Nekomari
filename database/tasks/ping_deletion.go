package tasks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"gorm.io/gorm"
)

const pingDeletionBatchSize = 64

type flushPingBatch func(context.Context) error
type deletePingMetrics func(context.Context, []uint) error

// The main DB and metric store cannot share a transaction. First reserve the
// ID and remove the task atomically; only then delete history. The tombstone
// survives cleanup to prevent supported creation paths from reusing the ID.
func deletePingTasksWith(ctx context.Context, db *gorm.DB, ids []uint, flush flushPingBatch, remove deletePingMetrics, reload func() error) error {
	if ctx == nil || db == nil || len(ids) == 0 || len(ids) > pingDeletionBatchSize || flush == nil || remove == nil || reload == nil {
		return errors.New("invalid ping task deletion")
	}
	ids = append([]uint(nil), ids...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if id == 0 || (i != 0 && ids[i-1] == id) {
			return errors.New("invalid or duplicate ping task ID")
		}
	}
	return metricstore.WithPingIdentityOperation(ctx, func() error {
		return pingguard.WithMany(ctx, ids, func() error {
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var tasks []models.PingTask
				if err := tx.Select("id", "config_uid").Where("id IN ?", ids).Find(&tasks).Error; err != nil {
					return err
				}
				if len(tasks) != len(ids) {
					return gorm.ErrRecordNotFound
				}
				rows := make([]models.PingTaskDeletion, len(tasks))
				for i, task := range tasks {
					rows[i] = models.PingTaskDeletion{TaskID: task.Id, ConfigUID: task.ConfigUID}
				}
				if err := tx.Create(&rows).Error; err != nil {
					return err
				}
				result := tx.Where("id IN ?", ids).Delete(&models.PingTask{})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != int64(len(ids)) {
					return gorm.ErrRecordNotFound
				}
				return nil
			}); err != nil {
				return err
			}
			// The request may be canceled immediately after commit. Finish the
			// bounded cleanup with an independent context and report its outcome.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			reloadErr := reload()
			cleanupErr := finishPingDeletionsLocked(cleanupCtx, db, ids, flush, remove)
			if err := errors.Join(cleanupErr, reloadErr); err != nil {
				return &PingDeletionPendingError{Err: err}
			}
			return nil
		})
	})
}

// Task guards prevent new result admission. Flushing before deleting metrics
// drains reports accepted before the SQL commit and before a previous crash.
func finishPingDeletionsLocked(ctx context.Context, db *gorm.DB, ids []uint, flush flushPingBatch, remove deletePingMetrics) error {
	var live int64
	if err := db.WithContext(ctx).Model(&models.PingTask{}).Where("id IN ?", ids).Count(&live).Error; err != nil {
		return err
	}
	if live != 0 {
		return errors.New("refusing metric cleanup because a reserved task ID is live")
	}
	if err := flush(ctx); err != nil {
		return err
	}
	if err := remove(ctx, ids); err != nil {
		return err
	}
	now := time.Now().UTC()
	return db.WithContext(ctx).Model(&models.PingTaskDeletion{}).Where("task_id IN ? AND cleaned_at IS NULL", ids).
		Updates(map[string]any{"cleaned_at": now, "last_attempt_at": now}).Error
}

// ReconcilePingDeletions retries a bounded batch after a crash or failed
// metric cleanup. All supported task creation paths must reject reserved IDs.
func ReconcilePingDeletions(ctx context.Context) (int, error) {
	var cleaned int
	err := metricstore.WithPingIdentityOperation(ctx, func() error {
		var err error
		cleaned, err = reconcilePingDeletionsWith(ctx, dbcore.GetDBInstance(), metricstore.FlushReportBatch, metricstore.DeletePingRecordsByTask)
		return err
	})
	return cleaned, err
}

func reconcilePingDeletionsWith(ctx context.Context, db *gorm.DB, flush flushPingBatch, remove deletePingMetrics) (int, error) {
	if ctx == nil || db == nil || flush == nil || remove == nil {
		return 0, errors.New("invalid ping cleanup context")
	}
	var pending []models.PingTaskDeletion
	// Persist attempts before cleanup so a damaged or repeatedly failing row
	// cannot permanently starve later rows, even after process restarts.
	if err := db.WithContext(ctx).Where("cleaned_at IS NULL").
		Order("CASE WHEN last_attempt_at IS NULL THEN 0 ELSE 1 END").
		Order("last_attempt_at ASC").Order("task_id ASC").Limit(pingDeletionBatchSize).Find(&pending).Error; err != nil {
		return 0, err
	}
	var cleaned int
	var failures []error
	for _, row := range pending {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		id := row.TaskID
		finished := false
		err := pingguard.With(ctx, id, func() error {
			// Another reconciliation can have completed it since the query.
			result := db.WithContext(ctx).Model(&models.PingTaskDeletion{}).
				Where("task_id = ? AND cleaned_at IS NULL", id).
				Update("last_attempt_at", time.Now().UTC())
			if result.Error != nil || result.RowsAffected == 0 {
				return result.Error
			}
			if err := finishPingDeletionsLocked(ctx, db, []uint{id}, flush, remove); err != nil {
				return err
			}
			finished = true
			return nil
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("task %d: %w", id, err))
		} else if finished {
			cleaned++
		}
	}
	if len(failures) != 0 {
		return cleaned, fmt.Errorf("reconcile ping deletion: %w", errors.Join(failures...))
	}
	return cleaned, nil
}

// Called after an auto-ID insert but before commit. Returning an error rolls
// the insert back instead of attaching an ID used by a deleted task.
func assertPingTaskIDAvailable(tx *gorm.DB, id uint) error {
	var count int64
	if err := tx.Model(&models.PingTaskDeletion{}).Where("task_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errors.New("generated ping task ID is reserved by a deleted task")
	}
	return nil
}
