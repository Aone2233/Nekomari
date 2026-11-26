package metricstore

import (
	"context"
	"fmt"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// ApplyRetentionPreset updates every built-in node metric to the node window
// and every ping/HTTP metric to the ping window. Panel-only metrics and
// explicitly disabled definitions are left alone. It never deletes data; a
// shorter window is enforced by normal retention.
func ApplyRetentionPreset(ctx context.Context, nodeDays, pingDays int) (int, error) {
	if nodeDays < 1 || nodeDays > 3650 || pingDays < 1 || pingDays > 3650 {
		return 0, fmt.Errorf("retention presets must be between 1 and 3650 days")
	}
	store := GetStore()
	if store == nil {
		return 0, fmt.Errorf("metric store not initialized")
	}
	defs, err := store.ListMetrics(ctx)
	if err != nil {
		return 0, fmt.Errorf("list metric definitions: %w", err)
	}
	updated := 0
	for _, def := range defs {
		if IsPanelMetric(def.Name) || def.RetentionDays == 0 {
			continue
		}
		target := nodeDays
		if IsPingMetric(def.Name) {
			target = pingDays
		}
		if def.RetentionDays == target {
			continue
		}
		if _, err := store.UpdateMetricRetention(ctx, def.Name, target); err != nil {
			if err == metric.ErrNotFound {
				continue
			}
			return updated, fmt.Errorf("update %s: %w", def.Name, err)
		}
		updated++
	}
	return updated, nil
}
