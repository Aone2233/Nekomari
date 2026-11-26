package pingrollback

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// splitMetricHistory is an isolated rollback experiment. It deliberately has
// no production entry point or resumable checkpoint. Both stores must be new,
// caller-owned scratch stores with every writer stopped; it is not a substitute
// for the missing two-database rollback coordinator and backup checks.
//
// Only ping series with a canonical, explicitly parked task ID go to archive.
// Other canonical ping series and non-ping metrics stay in the scratch store;
// ambiguous IDs and unknown ping metrics fail closed. Neither source nor archive is
// modified by its later use. A failure leaves partial scratch output, so both
// destinations must be discarded before retrying.
func splitMetricHistory(ctx context.Context, src, live, archive *metric.Store, taskIDs []uint) error {
	if ctx == nil || src == nil || live == nil || archive == nil || src == live || src == archive || live == archive || len(taskIDs) == 0 || len(taskIDs) > maxTasks {
		return fmt.Errorf("invalid isolated metric quarantine")
	}
	if err := metric.RequireDistinctSQLiteFiles(ctx, src, live, archive); err != nil {
		return fmt.Errorf("isolated metric quarantine: %w", err)
	}
	parked := make(map[string]struct{}, len(taskIDs))
	for _, id := range taskIDs {
		if id == 0 {
			return fmt.Errorf("invalid parked task ID")
		}
		key := strconv.FormatUint(uint64(id), 10)
		if _, duplicate := parked[key]; duplicate {
			return fmt.Errorf("duplicate parked task ID")
		}
		parked[key] = struct{}{}
	}
	defs, err := src.ListMetrics(ctx)
	if err != nil {
		return err
	}
	for _, def := range defs {
		if strings.HasPrefix(def.Name, "ping.") && !isPingMetric(def.Name) {
			return fmt.Errorf("unsupported ping metric in isolated quarantine")
		}
		for _, dst := range []*metric.Store{live, archive} {
			if err := dst.UpsertMetric(ctx, def); err != nil {
				return err
			}
		}
		_, err := src.ExportRollups(ctx, def.Name, 500, func(batch []metric.PersistedRollup) error {
			oldRows := make([]metric.PersistedRollup, 0, len(batch))
			parkedRows := make([]metric.PersistedRollup, 0, len(batch))
			for _, row := range batch {
				taskID := row.Tags["task_id"]
				_, isolated := parked[taskID]
				if isPingMetric(row.MetricName) && !canonicalTaskID(taskID) {
					return fmt.Errorf("ping history has an ambiguous task ID")
				}
				if isolated && isPingMetric(row.MetricName) {
					parkedRows = append(parkedRows, row)
				} else {
					oldRows = append(oldRows, row)
				}
			}
			if err := live.ImportRollups(ctx, oldRows); err != nil {
				return err
			}
			return archive.ImportRollups(ctx, parkedRows)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func isPingMetric(name string) bool {
	switch name {
	case metricstore.MetricPingLatency, metricstore.MetricPingSuccessLatency, metricstore.MetricPingLoss:
		return true
	default:
		return false
	}
}

// mergeMetricHistory is also scratch-only. The caller must pass the old
// panel's current store and a separately retained archive, both already
// closed to external writers. Partial destination output must be discarded.
// A ping metric disabled in the current store is not restored from archive.
func mergeMetricHistory(ctx context.Context, old, archive, merged *metric.Store, taskIDs []uint) error {
	if ctx == nil || old == nil || archive == nil || merged == nil || old == archive || old == merged || archive == merged || len(taskIDs) == 0 || len(taskIDs) > maxTasks {
		return fmt.Errorf("invalid isolated metric merge")
	}
	if err := metric.RequireDistinctSQLiteFiles(ctx, old, archive, merged); err != nil {
		return fmt.Errorf("isolated metric merge: %w", err)
	}
	parked := make(map[string]struct{}, len(taskIDs))
	for _, id := range taskIDs {
		key := strconv.FormatUint(uint64(id), 10)
		if id == 0 {
			return fmt.Errorf("invalid parked task ID")
		}
		if _, duplicate := parked[key]; duplicate {
			return fmt.Errorf("duplicate parked task ID")
		}
		parked[key] = struct{}{}
	}
	defs, err := old.ListMetrics(ctx)
	if err != nil {
		return err
	}
	current := make(map[string]metric.Definition, len(defs))
	for _, def := range defs {
		if strings.HasPrefix(def.Name, "ping.") && !isPingMetric(def.Name) {
			return fmt.Errorf("unsupported ping metric in isolated merge")
		}
		current[def.Name] = def
		if err := merged.UpsertMetric(ctx, def); err != nil {
			return err
		}
		_, err := old.ExportRollups(ctx, def.Name, 500, func(batch []metric.PersistedRollup) error {
			rows := make([]metric.PersistedRollup, 0, len(batch))
			for _, row := range batch {
				if isPingMetric(row.MetricName) {
					id := row.Tags["task_id"]
					if !canonicalTaskID(id) {
						return fmt.Errorf("ping history has an ambiguous task ID")
					}
					if _, isolated := parked[id]; isolated {
						continue
					}
				}
				rows = append(rows, row)
			}
			return merged.ImportRollups(ctx, rows)
		})
		if err != nil {
			return err
		}
	}
	archivedDefs, err := archive.ListMetrics(ctx)
	if err != nil {
		return err
	}
	for _, def := range archivedDefs {
		if strings.HasPrefix(def.Name, "ping.") && !isPingMetric(def.Name) {
			return fmt.Errorf("unsupported archived ping metric")
		}
		_, err := archive.ExportRollups(ctx, def.Name, 500, func(batch []metric.PersistedRollup) error {
			active, found := current[def.Name]
			for _, row := range batch {
				if !isPingMetric(row.MetricName) || !canonicalTaskID(row.Tags["task_id"]) {
					return fmt.Errorf("archive contains unrelated or ambiguous history")
				}
				if _, isolated := parked[row.Tags["task_id"]]; !isolated {
					return fmt.Errorf("archive contains unparked history")
				}
			}
			if !found {
				return errors.New("current store is missing an archived metric definition")
			}
			if active.Type != def.Type || active.Unit != def.Unit || (active.RetentionDays != 0 && active.RetentionDays != def.RetentionDays) {
				return errors.New("archived metric type, unit or retention changed")
			}
			if active.RetentionDays == 0 {
				return nil
			}
			return merged.ImportRollups(ctx, batch)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func canonicalTaskID(value string) bool {
	id, err := strconv.ParseUint(value, 10, strconv.IntSize)
	return err == nil && id != 0 && strconv.FormatUint(id, 10) == value
}
