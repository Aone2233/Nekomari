package notifier

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/maintenance"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"gorm.io/gorm"
)

// PreviewLoadLimit prevents a saved rule from turning an interactive preview into
// an unbounded fleet-wide or year-long historical query.
const PreviewLoadLimit = 50
const PreviewBaselineDays = 31

var ErrPreviewLimit = errors.New("rule exceeds preview limits")

type LoadPreviewTarget struct {
	Client      string   `json:"client,omitempty"`
	Task        string   `json:"task,omitempty"`
	Status      string   `json:"status"`
	Samples     int      `json:"samples,omitempty"`
	Threshold   *float32 `json:"threshold,omitempty"`
	Value       *float32 `json:"value,omitempty"`
	Exceeding   *int     `json:"exceeding,omitempty"`
	Required    *int     `json:"required,omitempty"`
	Maintenance bool     `json:"maintenance"`
}

type LoadPreview struct {
	EvaluatedAt          time.Time           `json:"evaluated_at"`
	WindowStart          time.Time           `json:"window_start"`
	LastNotified         *time.Time          `json:"last_notified"`
	StoredCooldownActive bool                `json:"stored_cooldown_active"`
	Targets              []LoadPreviewTarget `json:"targets"`
}

type loadPreviewSource struct {
	host     func(context.Context, string, string, time.Time, time.Time) ([]models.Record, error)
	ping     func(context.Context, string, int, time.Time, time.Time) ([]models.PingRecord, error)
	capacity func(context.Context, string, string) (int64, error)
	index    func(context.Context, []uint) (map[uint]models.PingTask, error)
	window   func(context.Context) ([]maintenance.Window, error)
}

// PreviewLoadNotification evaluates the saved rule without sending or changing
// any notification state. Data and configuration may change during evaluation.
func PreviewLoadNotification(ctx context.Context, rule models.LoadNotification, now time.Time) (LoadPreview, error) {
	db := dbcore.GetDBInstance()
	if db == nil {
		return LoadPreview{}, errors.New("database unavailable")
	}
	source := loadPreviewSource{
		host: metricstore.GetRecordMetricMaxByClientAndTime,
		ping: metricstore.GetPingRecords,
		capacity: func(ctx context.Context, uuid, metric string) (int64, error) {
			var client models.Client
			if err := db.WithContext(ctx).Select("mem_total", "swap_total", "disk_total").Where("uuid = ?", uuid).First(&client).Error; err != nil {
				return 0, err
			}
			switch metric {
			case "ram":
				return client.MemTotal, nil
			case "swap":
				return client.SwapTotal, nil
			default:
				return client.DiskTotal, nil
			}
		},
		index: func(ctx context.Context, ids []uint) (map[uint]models.PingTask, error) {
			var rows []models.PingTask
			if err := db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
				return nil, err
			}
			index := make(map[uint]models.PingTask, len(rows))
			for _, row := range rows {
				index[row.Id] = row
			}
			return index, nil
		},
		window: func(ctx context.Context) ([]maintenance.Window, error) {
			var rows []models.MaintenanceWindow
			if err := db.WithContext(ctx).Order("start asc, id asc").Find(&rows).Error; err != nil {
				return nil, err
			}
			windows := make([]maintenance.Window, 0, len(rows))
			for _, row := range rows {
				windows = append(windows, maintenance.Window{ID: row.ID, Name: row.Name, Start: row.Start, End: row.End, Clients: []string(row.Clients), Reason: row.Reason})
			}
			return windows, nil
		},
	}
	return previewLoadNotification(ctx, rule, now, source)
}

func previewLoadNotification(ctx context.Context, rule models.LoadNotification, now time.Time, source loadPreviewSource) (LoadPreview, error) {
	if err := ctx.Err(); err != nil {
		return LoadPreview{}, err
	}
	if rule.Interval < 1 || rule.Interval > 240 || math.IsNaN(float64(rule.Threshold)) || math.IsInf(float64(rule.Threshold), 0) ||
		math.IsNaN(float64(rule.Ratio)) || math.IsInf(float64(rule.Ratio), 0) || math.IsNaN(float64(rule.Multiplier)) || math.IsInf(float64(rule.Multiplier), 0) ||
		(rule.Mode != "" && rule.Mode != models.LoadThresholdModeFixed && rule.Mode != models.LoadThresholdModeBaseline) ||
		(!IsPingAlertMetric(rule.Metric) && (rule.Ratio <= 0 || rule.Ratio > 1)) {
		return LoadPreview{}, fmt.Errorf("invalid saved rule")
	}
	if len(rule.Clients) > PreviewLoadLimit || len(rule.Tasks) > PreviewLoadLimit || (rule.UsesBaseline() && rule.BaselineWindow() > PreviewBaselineDays*24*time.Hour) {
		return LoadPreview{}, ErrPreviewLimit
	}
	now = now.UTC()
	start := now.Add(-time.Duration(rule.Interval) * time.Minute)
	result := LoadPreview{EvaluatedAt: now, WindowStart: start, LastNotified: rule.LastNotified, Targets: []LoadPreviewTarget{}}
	if rule.LastNotified != nil && !rule.LastNotified.IsZero() {
		result.StoredCooldownActive = now.Sub(*rule.LastNotified) < time.Duration(rule.Interval)*time.Minute
	}

	// Expand and bound all targets before making any per-target history queries.
	if IsPingAlertMetric(rule.Metric) {
		ids := make([]uint, 0, len(rule.Tasks))
		for _, raw := range rule.Tasks {
			if id, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && id > 0 {
				ids = append(ids, uint(id))
			}
		}
		index, err := source.index(ctx, ids)
		if err != nil {
			return LoadPreview{}, err
		}
		allowed := make(map[string]bool, len(rule.Clients))
		for _, uuid := range rule.Clients {
			allowed[uuid] = true
		}
		for _, raw := range rule.Tasks {
			id, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || id <= 0 {
				result.Targets = append(result.Targets, LoadPreviewTarget{Task: raw, Status: "invalid_task"})
				continue
			}
			pingTask, found := index[uint(id)]
			if !found {
				result.Targets = append(result.Targets, LoadPreviewTarget{Task: raw, Status: "task_missing"})
				continue
			}
			count := 0
			for _, uuid := range pingTask.Clients {
				if len(allowed) == 0 || allowed[uuid] {
					target := LoadPreviewTarget{Client: uuid, Task: raw}
					if uuid == "" {
						target.Status = "invalid_target"
					}
					result.Targets = append(result.Targets, target)
					count++
					if len(result.Targets) > PreviewLoadLimit {
						return LoadPreview{}, ErrPreviewLimit
					}
				}
			}
			if count == 0 {
				result.Targets = append(result.Targets, LoadPreviewTarget{Task: raw, Status: "no_targets"})
			}
		}
	} else {
		for _, uuid := range rule.Clients {
			target := LoadPreviewTarget{Client: uuid}
			if uuid == "" {
				target.Status = "invalid_target"
			}
			result.Targets = append(result.Targets, target)
		}
	}
	if len(result.Targets) > PreviewLoadLimit {
		return LoadPreview{}, ErrPreviewLimit
	}
	windows, err := source.window(ctx)
	if err != nil {
		return LoadPreview{}, err
	}
	for i := range result.Targets {
		if err := ctx.Err(); err != nil {
			return LoadPreview{}, err
		}
		target := &result.Targets[i]
		if target.Status != "" {
			continue
		}
		target.Maintenance = maintenance.Decide(now, target.Client, windows).Suppress
		if IsPingAlertMetric(rule.Metric) {
			previewPingTarget(ctx, rule, start, now, target, source)
		} else {
			previewHostTarget(ctx, rule, start, now, target, source)
		}
	}
	if err := ctx.Err(); err != nil {
		return LoadPreview{}, err
	}
	return result, nil
}

func previewPingTarget(ctx context.Context, rule models.LoadNotification, start, now time.Time, target *LoadPreviewTarget, source loadPreviewSource) {
	id, _ := strconv.Atoi(strings.TrimSpace(target.Task))
	recs, err := source.ping(ctx, target.Client, id, start, now)
	if err != nil {
		target.Status = "query_error"
		return
	}
	value, ok := pingMetricValue(recs, rule.Metric)
	if !ok {
		target.Status = "no_data"
		return
	}
	series, _ := pingMetricSeries(recs, rule.Metric)
	target.Samples = len(series)
	target.Value = &value
	threshold := rule.Threshold
	if rule.UsesBaseline() {
		history, err := source.ping(ctx, target.Client, id, start.Add(-rule.BaselineWindow()), start)
		if err != nil {
			target.Status = "query_error"
			return
		}
		values, _ := pingMetricSeries(history, rule.Metric)
		threshold, ok = computeBaselineThreshold(values, rule.EffectiveMultiplier(), rule.Threshold)
		if !ok {
			target.Status = "baseline_unavailable"
			return
		}
	}
	target.Threshold = &threshold
	target.Status = "not_matched"
	if value >= threshold {
		target.Status = "matched"
	}
}

func previewHostTarget(ctx context.Context, rule models.LoadNotification, start, now time.Time, target *LoadPreviewTarget, source loadPreviewSource) {
	// Only metrics whose scheduler value conversion is understood are previewed.
	switch rule.Metric {
	case "cpu", "gpu", "ram", "swap", "disk", "load", "net_in", "netin", "net_out", "netout":
	default:
		target.Status = "unsupported_metric"
		return
	}
	recs, err := source.host(ctx, target.Client, rule.Metric, start, now)
	if err != nil {
		target.Status = "query_error"
		return
	}
	if len(recs) == 0 {
		target.Status = "no_data"
		return
	}
	capacity := int64(0)
	if rule.Metric == "ram" || rule.Metric == "swap" || rule.Metric == "disk" {
		capacity, err = source.capacity(ctx, target.Client, rule.Metric)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			target.Status = "query_error"
			return
		}
		if capacity <= 0 {
			target.Status = "capacity_unavailable"
			return
		}
	}
	values := previewHostValues(recs, rule.Metric, capacity)
	target.Samples = len(values)
	threshold := rule.Threshold
	if rule.UsesBaseline() {
		history, err := source.host(ctx, target.Client, rule.Metric, start.Add(-rule.BaselineWindow()), start)
		if err != nil {
			target.Status = "query_error"
			return
		}
		var ok bool
		threshold, ok = computeBaselineThreshold(previewHostValues(history, rule.Metric, capacity), rule.EffectiveMultiplier(), rule.Threshold)
		if !ok {
			target.Status = "baseline_unavailable"
			return
		}
	}
	required := int(float32(len(values)) * rule.Ratio)
	if required == 0 {
		required = 1
	}
	exceeding := 0
	for _, value := range values {
		if value >= threshold {
			exceeding++
		}
	}
	target.Threshold, target.Exceeding, target.Required = &threshold, &exceeding, &required
	target.Status = "not_matched"
	if exceeding >= required {
		target.Status = "matched"
	}
}

func previewHostValues(records []models.Record, metric string, capacity int64) []float32 {
	values := make([]float32, 0, len(records))
	for _, record := range records {
		switch metric {
		case "ram":
			values = append(values, float32(record.Ram)/float32(capacity)*100)
		case "swap":
			values = append(values, float32(record.Swap)/float32(capacity)*100)
		case "disk":
			values = append(values, float32(record.Disk)/float32(capacity)*100)
		default:
			values = append(values, getMetricValue(record, metric))
		}
	}
	return values
}
