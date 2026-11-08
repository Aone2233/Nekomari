package metricstore

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/metric"
	"gorm.io/gorm"
)

// A deleted ID remains reserved even after its current store has been cleaned.
// An older store can still contain its history, so every replacement and
// migration must consult the main database before exposing or copying it.
func reservedPingTaskIDs(ctx context.Context, db *gorm.DB) (map[string]struct{}, error) {
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("main database unavailable for ping history reconciliation")
	}
	var conflict int64
	if err := db.WithContext(ctx).Table("ping_task_deletions AS d").
		Joins("JOIN ping_tasks AS t ON t.id = d.task_id").Count(&conflict).Error; err != nil {
		return nil, fmt.Errorf("inspect reserved ping task IDs: %w", err)
	}
	if conflict != 0 {
		return nil, fmt.Errorf("reserved ping task ID is live; refusing to change metric stores")
	}
	var rows []models.PingTaskDeletion
	if err := db.WithContext(ctx).Select("task_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read reserved ping task IDs: %w", err)
	}
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.TaskID == 0 {
			return nil, fmt.Errorf("invalid reserved ping task ID")
		}
		ids[strconv.FormatUint(uint64(row.TaskID), 10)] = struct{}{}
	}
	return ids, nil
}

// The caller holds the store operation gate exclusively, or has not yet
// published the store. A failed cleanup must leave the replacement inactive.
func cleanReservedPingHistory(ctx context.Context, s *metric.Store, ids map[string]struct{}) error {
	if s == nil {
		return fmt.Errorf("metric store unavailable for ping history reconciliation")
	}
	for id := range ids {
		for _, name := range pingMetricNames {
			if _, err := s.DeleteSeries(ctx, metric.Query{MetricName: name, Tags: map[string]string{"task_id": id}}); err != nil {
				return fmt.Errorf("clean reserved ping task %s: %w", id, err)
			}
		}
	}
	return nil
}

// ReconcileReservedPingHistory removes deleted task histories from a store
// before it is exposed. No other process or writer may use the store while it
// runs; callers using the global store must hold its exclusive operation gate.
func ReconcileReservedPingHistory(ctx context.Context, s *metric.Store, db *gorm.DB) error {
	ids, err := reservedPingTaskIDs(ctx, db)
	if err != nil {
		return err
	}
	return cleanReservedPingHistory(ctx, s, ids)
}

func isReservedPingRollup(row metric.PersistedRollup, ids map[string]struct{}) bool {
	for _, name := range pingMetricNames {
		if row.MetricName == name {
			_, reserved := ids[row.Tags["task_id"]]
			return reserved
		}
	}
	return false
}
