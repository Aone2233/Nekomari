package metricstore

import (
	"context"
	"errors"
	"fmt"

	logger "github.com/Aone2233/nekomari/utils/log"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// DeleteAllRecords 删除所有负载/系统类记录（保留指标定义，不含 ping）。
func DeleteAllRecords(ctx context.Context) error {
	if err := acquirePingHistoryTransition(ctx, "clearing records"); err != nil {
		return err
	}
	defer releasePingHistoryTransition()
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	err := deleteMetricRecords(ctx, s, recordMetricNames)
	clearReportTrafficStates()
	return err
}

// DeleteAllPingRecords 删除全部 ping 记录（保留指标定义）。
func DeleteAllPingRecords(ctx context.Context) error {
	if err := acquirePingHistoryTransition(ctx, "clearing ping records"); err != nil {
		return err
	}
	defer releasePingHistoryTransition()
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	return deleteMetricRecords(ctx, s, pingMetricNames)
}

func deleteMetricRecords(ctx context.Context, s *metric.Store, names []string) error {
	var failures []error
	for _, name := range names {
		// DeleteSeries also removes pending coarse rollups. DeleteBefore alone
		// can let those in-memory buckets reappear on a later compaction.
		if _, err := s.DeleteSeries(ctx, metric.Query{MetricName: name}); err != nil {
			failures = append(failures, fmt.Errorf("delete metric %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

// DeletePingRecordsByTask 删除指定任务（task_id）的全部 ping 记录。
func DeletePingRecordsByTask(ctx context.Context, taskIDs []uint) error {
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return err
	}
	defer storeOperations.ReleaseShared()
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	for _, id := range taskIDs {
		for _, metricName := range pingMetricNames {
			if _, err := s.DeleteSeries(ctx, metric.Query{
				MetricName: metricName,
				Tags:       map[string]string{"task_id": fmt.Sprintf("%d", id)},
			}); err != nil {
				return fmt.Errorf("failed to delete ping records for task %d: %w", id, err)
			}
		}
	}
	return nil
}

// DeleteEntity 删除指定 agent 在所有指标下的历史数据。
func DeleteEntity(ctx context.Context, entityID string) error {
	if err := acquirePingHistoryTransition(ctx, "clearing node history"); err != nil {
		return err
	}
	defer releasePingHistoryTransition()
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	if _, err := s.DeleteEntity(ctx, entityID); err != nil {
		return fmt.Errorf("failed to delete metric records for entity %s: %w", entityID, err)
	}
	deleteReportTrafficState(entityID)
	return nil
}

// DeleteEntityAsync clears one agent's metric history without delaying the
// client deletion response.
func DeleteEntityAsync(entityID string) {
	if !pendingCleanup.start(func() error {
		err := DeleteEntity(context.Background(), entityID)
		if err != nil {
			logger.Errorf("metricstore", "Failed to delete metric records for entity %s: %v", entityID, err)
		}
		return err
	}) {
		logger.Errorf("metricstore", "Rejected entity cleanup while shutting down: %s", entityID)
	}
}

// DeleteMetricDataAsync clears disabled metric history without delaying an
// admin retention-policy update response.
func DeleteMetricDataAsync(metricName string) {
	if !pendingCleanup.start(func() error {
		s := GetStore()
		if s == nil {
			logger.Errorf("metricstore", "Failed to delete disabled metric %s: metric store not enabled", metricName)
			return errors.New("metric store not enabled")
		}
		_, err := s.DeleteMetricDataIfDisabled(context.Background(), metricName)
		if err != nil {
			logger.Errorf("metricstore", "Failed to delete disabled metric %s: %v", metricName, err)
		}
		return err
	}) {
		logger.Errorf("metricstore", "Rejected disabled metric cleanup while shutting down: %s", metricName)
	}
}
