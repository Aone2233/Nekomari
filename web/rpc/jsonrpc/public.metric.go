package jsonrpc

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

const defaultMetricQueryPoints = 500

func init() {
	regPublic("listMetricDefinitions", publicListMetricDefinitions, "List public metric definitions")
	regPublic("queryMetrics", publicQueryMetrics, "Query metric points")
	regPublic("getPingMetricStats", publicGetPingMetricStats, "Get ping metric statistics")
}

type publicMetricQueryParams struct {
	MetricKey  string   `json:"metric_key"`
	MetricKeys []string `json:"metric_keys"`
	Metrics    []string `json:"metrics"`

	EntityID  string   `json:"entity_id"`
	EntityIDs []string `json:"entity_ids"`

	Start     *time.Time `json:"start"`
	StartTime *time.Time `json:"start_time"`
	End       *time.Time `json:"end"`
	EndTime   *time.Time `json:"end_time"`
	Hours     float64    `json:"hours"`

	Tags map[string]string `json:"tags"`

	FillEmpty *bool `json:"fill_empty"`

	MaxPoints         int            `json:"max_points"`
	MaxPointsByMetric map[string]int `json:"max_points_by_metric"`
	PointsByMetric    map[string]int `json:"points_by_metric"`

	Aggregation         string            `json:"aggregation"`
	Algorithm           string            `json:"algorithm"`
	AggregationByMetric map[string]string `json:"aggregation_by_metric"`
	AlgorithmByMetric   map[string]string `json:"algorithm_by_metric"`
}

type publicMetricPoint struct {
	entityID string
	Time     time.Time         `json:"time"`
	Value    *float64          `json:"value"`
	Count    int               `json:"count,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type publicMetricSeries struct {
	Semantics              string              `json:"semantics,omitempty"`
	Quality                string              `json:"quality,omitempty"`
	WindowSemantics        string              `json:"window_semantics,omitempty"`
	CoverageStart          *time.Time          `json:"coverage_start,omitempty"`
	CoverageEndExclusive   *time.Time          `json:"coverage_end_exclusive,omitempty"`
	BackingIntervalSeconds float64             `json:"backing_interval_seconds,omitempty"`
	MetricKey              string              `json:"metric_key"`
	EntityID               string              `json:"entity_id"`
	Type                   string              `json:"type,omitempty"`
	Unit                   string              `json:"unit,omitempty"`
	RetentionDays          int                 `json:"retention_days,omitempty"`
	Tags                   map[string]string   `json:"tags,omitempty"`
	Downsampled            bool                `json:"downsampled"`
	DownsampleAlgorithm    string              `json:"downsample_algorithm,omitempty"`
	FillEmpty              bool                `json:"fill_empty,omitempty"`
	MaxPoints              int                 `json:"max_points,omitempty"`
	IntervalSeconds        float64             `json:"interval_seconds,omitempty"`
	Count                  int                 `json:"count"`
	Points                 []publicMetricPoint `json:"points"`
}

type publicPingMetricStatsParams struct {
	UUID      string   `json:"uuid"`
	EntityID  string   `json:"entity_id"`
	EntityIDs []string `json:"entity_ids"`

	TaskID  any   `json:"task_id"`
	TaskIDs []any `json:"task_ids"`

	Start     *time.Time `json:"start"`
	StartTime *time.Time `json:"start_time"`
	End       *time.Time `json:"end"`
	EndTime   *time.Time `json:"end_time"`
	Hours     float64    `json:"hours"`

	MaxPoints int `json:"max_points"`
}

type publicPingMetricTaskStats struct {
	EntityID string `json:"entity_id"`
	TaskID   string `json:"task_id"`
	// Family 是这一组统计实际使用的地址族（"ipv4"/"ipv6"）。
	// 目标是域名时由各节点自行解析，双栈域名可能落到不同族；不区分就会把
	// 两条不同路径的延迟与丢包算成一个数字（实测过 HK04 读 12.6% 丢包、
	// 两个 v4-only 节点读 0.0%，而它们描述的不是同一条路）。
	// 空值表示该组没有族信息（旧 agent 或无法判断），此时与改动前一致。
	Family                 string            `json:"family,omitempty"`
	Protocol               string            `json:"protocol,omitempty"`
	Role                   string            `json:"role,omitempty"`
	Semantics              string            `json:"semantics"`
	Quality                string            `json:"quality"`
	PercentilesApproximate bool              `json:"percentiles_approximate,omitempty"`
	Name                   string            `json:"name,omitempty"`
	Type                   string            `json:"type,omitempty"`
	Interval               int               `json:"interval,omitempty"`
	Tags                   map[string]string `json:"tags,omitempty"`
	Total                  int               `json:"total"`
	Valid                  int               `json:"valid"`
	ValidKnown             bool              `json:"valid_known"`
	Loss                   *float64          `json:"loss"`
	LossApproximate        bool              `json:"loss_approximate,omitempty"`
	Min                    *float64          `json:"min,omitempty"`
	Max                    *float64          `json:"max,omitempty"`
	Avg                    *float64          `json:"avg,omitempty"`
	Latest                 *float64          `json:"latest,omitempty"`
	P50                    *float64          `json:"p50,omitempty"`
	P95                    *float64          `json:"p95,omitempty"`
	P99                    *float64          `json:"p99,omitempty"`
	StdDev                 *float64          `json:"stddev,omitempty"`
	P99P50Ratio            *float64          `json:"p99_p50_ratio"`
	WindowSemantics        string            `json:"window_semantics"`
	CoverageStart          *time.Time        `json:"coverage_start,omitempty"`
	CoverageEndExclusive   *time.Time        `json:"coverage_end_exclusive,omitempty"`
	BackingIntervalSeconds float64           `json:"backing_interval_seconds,omitempty"`
}

type publicPingMetricStatsResponse struct {
	Start           time.Time                   `json:"start"`
	End             time.Time                   `json:"end"`
	IntervalSeconds float64                     `json:"interval_seconds,omitempty"`
	Stats           []publicPingMetricTaskStats `json:"stats"`
	Count           int                         `json:"count"`
}

func publicListMetricDefinitions(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}
	defs, err := store.ListMetrics(ctx)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list metric definitions: "+err.Error(), nil)
	}
	out := make([]metricDefinitionResponse, 0, len(defs))
	for _, def := range defs {
		out = append(out, metricDefinitionResponse{
			Name:          def.Name,
			Description:   metricDescriptionValue(def.Description),
			Type:          string(def.Type),
			Unit:          def.Unit,
			RetentionDays: def.RetentionDays,
			Metadata:      def.Metadata,
			CreatedAt:     def.CreatedAt,
			UpdatedAt:     def.UpdatedAt,
		})
	}
	return out, nil
}

func publicQueryMetrics(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params publicMetricQueryParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	metricKeys := normalizeStringList(params.MetricKeys, params.Metrics, []string{params.MetricKey})
	if len(metricKeys) > maxMetricKeys {
		return nil, rpc.MakeError(rpc.InvalidParams, "too many metric keys", nil)
	}
	if len(metricKeys) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "metric_keys is required", nil)
	}

	queryNow := time.Now().UTC()
	end := metricQueryTimeOrDefault(firstMetricQueryTime(params.End, params.EndTime), queryNow)
	startFallback := end.Add(-metricQueryHours(params.Hours))
	start := metricQueryTimeOrDefault(firstMetricQueryTime(params.Start, params.StartTime), startFallback)
	if err := validateMetricWindow(start, end); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	if !end.After(start) {
		return nil, rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
	}

	requestedEntityIDs := normalizeStringList(params.EntityIDs, []string{params.EntityID})
	entityIDs, rpcErr := publicMetricEntityIDs(ctx, requestedEntityIDs)
	if rpcErr != nil {
		return nil, rpcErr
	}

	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}

	type metricLoadSpec struct {
		metricKey  string
		storageKey string
		algorithm  metric.Aggregation
		maxPoints  int
		interval   time.Duration
	}
	loadSpecs := make([]metricLoadSpec, 0, len(metricKeys))
	storageKeys := make([]string, 0, len(metricKeys))
	pointBudget := 0
	for _, metricKey := range metricKeys {
		maxPoints, err := resolveMetricMaxPoints(metricKey, params)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
		pointBudget += maxPoints * maxInt(1, len(entityIDs))
		if pointBudget > maxMetricTotalPoints {
			return nil, rpc.MakeError(rpc.InvalidParams, "query point budget exceeded; request fewer nodes, metrics or points", nil)
		}
		algorithm := resolveMetricAggregation(metricKey, params)
		if _, cumulative := trafficCumulativeMetrics[metricKey]; cumulative && (algorithm == metric.AggRate || algorithm == metric.AggDelta) {
			return nil, rpc.MakeError(rpc.InvalidParams, "rate/delta on billing-cycle counters is not continuity-safe; use sum for validated interval amounts", nil)
		}
		storageKey := publicMetricStorageKey(metricKey, algorithm)
		storageKeys = append(storageKeys, storageKey)
		loadSpecs = append(loadSpecs, metricLoadSpec{
			metricKey:  metricKey,
			storageKey: storageKey,
			algorithm:  algorithm,
			maxPoints:  maxPoints,
		})
	}

	metricFillEmpty := resolveMetricFillEmpty(params)
	useRaw := publicMetricUsesRawWindow(start, end, queryNow)
	definitions := make(map[string]metric.Definition, len(metricKeys))
	rawValues := make(map[string][]metric.Point)
	rollupValues := make(map[string]map[metric.Aggregation][]metric.AggregatePoint)
	backingResolutions := make(map[string]time.Duration)
	if len(entityIDs) > 0 && useRaw {
		var err error
		definitions, err = store.GetMetrics(ctx, storageKeys)
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to query metric definitions: "+err.Error(), nil)
		}
		for _, spec := range loadSpecs {
			if _, ok := definitions[spec.storageKey]; !ok {
				return nil, rpc.MakeError(rpc.InvalidParams, "unknown metric key: "+spec.metricKey, nil)
			}
		}
		rawValues, err = store.QueryBatch(ctx, metric.BatchQuery{
			MetricNames: storageKeys,
			EntityIDs:   entityIDs,
			Start:       start,
			End:         end,
			Tags:        params.Tags,
			Order:       metric.OrderAsc,
		})
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Failed to query metrics: "+err.Error(), nil)
		}
	}
	if len(entityIDs) > 0 {
		batchSpecs := make([]metric.BatchSeriesSpec, 0, len(loadSpecs))
		for i := range loadSpecs {
			if useRaw && !isIntervalTrafficMetric(loadSpecs[i].storageKey) {
				continue
			}
			loadSpecs[i].interval = metricDownsampleInterval(end.Sub(start), loadSpecs[i].maxPoints)
			if isIntervalTrafficMetric(loadSpecs[i].storageKey) {
				loadSpecs[i].interval = time.Minute
			}
			loadSpecs[i].interval = store.CompatibleSeriesInterval(start, queryNow, loadSpecs[i].interval)
			batchSpecs = append(batchSpecs, metric.BatchSeriesSpec{
				MetricName:       loadSpecs[i].storageKey,
				Aggregations:     []metric.Aggregation{loadSpecs[i].algorithm},
				Interval:         loadSpecs[i].interval,
				PreserveSeries:   true,
				FinestResolution: isIntervalTrafficMetric(loadSpecs[i].storageKey),
			})
		}
		if len(batchSpecs) > 0 {
			loaded, err := store.SeriesBatch(ctx, metric.BatchSeriesQuery{
				Specs:     batchSpecs,
				EntityIDs: entityIDs,
				Start:     start,
				End:       end,
				Tags:      params.Tags,
				Order:     metric.OrderAsc,
			}, queryNow)
			if err != nil {
				return nil, rpc.MakeError(rpc.InvalidParams, "Failed to query metrics: "+err.Error(), nil)
			}
			for key, def := range loaded.Definitions {
				definitions[key] = def
			}
			rollupValues = loaded.Values
			backingResolutions = loaded.Resolutions
		}
		for _, spec := range loadSpecs {
			if _, ok := definitions[spec.storageKey]; !ok {
				return nil, rpc.MakeError(rpc.InvalidParams, "unknown metric key: "+spec.metricKey, nil)
			}
		}
	} else {
		var err error
		definitions, err = store.GetMetrics(ctx, storageKeys)
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to query metric definitions: "+err.Error(), nil)
		}
		for _, spec := range loadSpecs {
			if _, ok := definitions[spec.storageKey]; !ok {
				return nil, rpc.MakeError(rpc.InvalidParams, "unknown metric key: "+spec.metricKey, nil)
			}
		}
	}

	series := make([]publicMetricSeries, 0, len(metricKeys)*maxInt(1, len(entityIDs)))
	responsePoints := 0
	for _, spec := range loadSpecs {
		specRaw := useRaw
		if specRaw && isIntervalTrafficMetric(spec.storageKey) {
			specRaw = rawTrafficMatchesRollups(rawValues[spec.storageKey], rollupValues[spec.storageKey][spec.algorithm])
		}
		def := definitions[spec.storageKey]
		item := publicMetricSeries{
			MetricKey:       spec.metricKey,
			Type:            string(def.Type),
			Unit:            def.Unit,
			RetentionDays:   def.RetentionDays,
			Tags:            params.Tags,
			FillEmpty:       metricFillEmpty,
			MaxPoints:       spec.maxPoints,
			Downsampled:     !specRaw,
			WindowSemantics: "exact_samples",
		}
		if spec.storageKey != spec.metricKey {
			item.Semantics = "interval_delta_v2"
			item.Quality = "validated_intervals_only;legacy_history_unknown"
		} else if _, ok := trafficCumulativeMetrics[spec.metricKey]; ok {
			item.Semantics = "billing_cycle_cumulative"
		}
		if specRaw {
			points := rawValues[spec.storageKey]
			item.Points = make([]publicMetricPoint, 0, len(points))
			for _, point := range points {
				item.Points = append(item.Points, publicMetricPoint{
					entityID: point.EntityID,
					Time:     point.Timestamp.UTC(),
					Value:    publicRawMetricValue(point.MetricName, point.Value, metricFillEmpty),
					Count:    1,
					Tags:     point.Tags,
					Labels:   point.Labels,
				})
			}
		} else {
			item.WindowSemantics = "bucket_coverage"
			resolution := backingResolutions[spec.storageKey]
			item.CoverageStart, item.CoverageEndExclusive = metricBucketCoverage(start, end, resolution)
			item.BackingIntervalSeconds = resolution.Seconds()
			item.DownsampleAlgorithm = string(spec.algorithm)
			item.IntervalSeconds = spec.interval.Seconds()
			points := rollupValues[spec.storageKey][spec.algorithm]
			item.Points = make([]publicMetricPoint, 0, len(points))
			for _, point := range points {
				item.Points = append(item.Points, publicMetricPoint{
					entityID: point.EntityID,
					Time:     point.Bucket.UTC(),
					Value:    publicRawMetricValue(point.MetricName, point.Value, metricFillEmpty),
					Count:    point.Count,
					Tags:     point.Tags,
				})
			}
		}

		byEntity := make(map[string][]publicMetricSeries, len(entityIDs))
		for _, split := range splitPublicMetricSeries(item) {
			if split.EntityID != "" {
				byEntity[split.EntityID] = append(byEntity[split.EntityID], split)
			}
		}
		for _, entityID := range entityIDs {
			entitySeries := byEntity[entityID]
			if len(entitySeries) == 0 {
				empty := item
				empty.EntityID = entityID
				empty.Count = 0
				empty.Points = nil
				entitySeries = []publicMetricSeries{empty}
			}
			for _, split := range entitySeries {
				if isIntervalTrafficMetric(spec.storageKey) && len(split.Points) > spec.maxPoints {
					split.Points = sumPublicTrafficBins(split.Points, start, end, spec.maxPoints)
					split.Count = len(split.Points)
					split.Downsampled = true
					split.DownsampleAlgorithm = string(metric.AggSum)
					split.IntervalSeconds = float64(end.Sub(start)) / float64(time.Second) / float64(spec.maxPoints)
				}
				if metricFillEmpty {
					split = adaptiveFillPublicMetricSeries(split, start, end)
				}
				responsePoints += len(split.Points)
				if responsePoints > maxMetricTotalPoints {
					return nil, rpc.MakeError(rpc.InvalidParams, "response point budget exceeded; narrow the query", nil)
				}
				series = append(series, split)
			}
		}
	}

	return map[string]any{
		"start":                     start.UTC(),
		"end":                       end.UTC(),
		"server_downsample_default": true,
		"default_points":            defaultMetricQueryPoints,
		"series":                    series,
		"count":                     len(series),
	}, nil
}

type publicMetricPointResult struct {
	points      []publicMetricPoint
	downsampled bool
	interval    time.Duration
}

func loadPublicMetricPoints(
	ctx context.Context,
	store *metric.Store,
	query metric.Query,
	algorithm metric.Aggregation,
	maxPoints int,
	fillEmpty bool,
	now time.Time,
) (publicMetricPointResult, error) {
	query.MetricName = publicMetricStorageKey(query.MetricName, algorithm)
	if publicMetricUsesRawWindow(query.Start, query.End, now) {
		points, err := store.Query(ctx, query)
		if err != nil {
			return publicMetricPointResult{}, err
		}
		rawComplete := true
		if isIntervalTrafficMetric(query.MetricName) {
			rollups, err := store.Series(ctx, metric.AggregateQuery{Query: query, Aggregation: algorithm, Interval: time.Minute, PreserveSeries: true}, now)
			if err != nil {
				return publicMetricPointResult{}, err
			}
			rawComplete = rawTrafficMatchesRollups(points, rollups)
		}
		if rawComplete {
			result := publicMetricPointResult{points: make([]publicMetricPoint, 0, len(points))}
			for _, point := range points {
				result.points = append(result.points, publicMetricPoint{
					entityID: point.EntityID,
					Time:     point.Timestamp.UTC(),
					Value:    publicRawMetricValue(point.MetricName, point.Value, fillEmpty),
					Count:    1,
					Tags:     point.Tags,
					Labels:   point.Labels,
				})
			}
			if isIntervalTrafficMetric(query.MetricName) && len(result.points) > maxPoints && maxPoints > 0 {
				result.points = sumPublicTrafficBins(result.points, query.Start, query.End, maxPoints)
				result.downsampled = true
				result.interval = query.End.Sub(query.Start) / time.Duration(maxPoints)
			}
			return result, nil
		}
	}

	interval := metricDownsampleInterval(query.End.Sub(query.Start), maxPoints)
	if isIntervalTrafficMetric(query.MetricName) {
		interval = time.Minute
	}
	interval = store.CompatibleSeriesInterval(query.Start, now, interval)
	points, err := store.Series(ctx, metric.AggregateQuery{
		Query:          query,
		Aggregation:    algorithm,
		Interval:       interval,
		PreserveSeries: true,
	}, now)
	if err != nil {
		return publicMetricPointResult{}, err
	}
	result := publicMetricPointResult{
		points:      make([]publicMetricPoint, 0, len(points)),
		downsampled: true,
		interval:    interval,
	}
	for _, point := range points {
		result.points = append(result.points, publicMetricPoint{
			entityID: point.EntityID,
			Time:     point.Bucket.UTC(),
			Value:    publicRawMetricValue(point.MetricName, point.Value, fillEmpty),
			Count:    point.Count,
			Tags:     point.Tags,
		})
	}
	if isIntervalTrafficMetric(query.MetricName) && len(result.points) > maxPoints && maxPoints > 0 {
		result.points = sumPublicTrafficBins(result.points, query.Start, query.End, maxPoints)
		result.interval = query.End.Sub(query.Start) / time.Duration(maxPoints)
	}
	return result, nil
}

// Queries include both endpoints; retained buckets cover a half-open superset.
func metricBucketCoverage(start, end time.Time, resolution time.Duration) (*time.Time, *time.Time) {
	if resolution <= 0 {
		return nil, nil
	}
	lower := start.UTC().Truncate(resolution)
	upper := end.UTC().Truncate(resolution).Add(resolution)
	return &lower, &upper
}

func publicMetricUsesRawWindow(start, end, now time.Time) bool {
	retention := metricstore.DefaultRollupRawRetention
	return !start.Before(now.UTC().Add(-retention)) && end.After(start)
}

func isIntervalTrafficMetric(name string) bool {
	return name == metricstore.MetricTrafficIntervalUp || name == metricstore.MetricTrafficIntervalDown
}

// Exact samples are volatile; compare every series against the durable view so
// a restart cannot silently turn a full-window traffic query into a partial sum.
func rawTrafficMatchesRollups(raw []metric.Point, rollups []metric.AggregatePoint) bool {
	type summary struct {
		count int
		sum   float64
	}
	rawSummary, rollupSummary := map[string]summary{}, map[string]summary{}
	for _, p := range raw {
		key := p.EntityID + "\x00" + publicMetricTagsKey(p.Tags)
		v := rawSummary[key]
		v.count++
		v.sum += p.Value
		rawSummary[key] = v
	}
	for _, p := range rollups {
		key := p.EntityID + "\x00" + publicMetricTagsKey(p.Tags)
		v := rollupSummary[key]
		v.count += p.Count
		v.sum += p.Value
		rollupSummary[key] = v
	}
	if len(rawSummary) != len(rollupSummary) {
		return false
	}
	for key, v := range rawSummary {
		other := rollupSummary[key]
		if v.count != other.count || math.Abs(v.sum-other.sum) > 1e-9*math.Max(1, math.Abs(other.sum)) {
			return false
		}
	}
	return true
}

// Binning an additive metric must sum, never sample or average. Relative window
// bins keep both closed endpoints and guarantee at most maxPoints per series.
func sumPublicTrafficBins(points []publicMetricPoint, start, end time.Time, maxPoints int) []publicMetricPoint {
	if maxPoints <= 0 || len(points) <= maxPoints || !end.After(start) {
		return points
	}
	type binKey struct {
		series string
		index  int
	}
	bins := make(map[binKey]publicMetricPoint, maxPoints)
	span := float64(end.Sub(start))
	for _, p := range points {
		index := int(float64(p.Time.Sub(start)) / span * float64(maxPoints))
		if index < 0 {
			index = 0
		}
		if index >= maxPoints {
			index = maxPoints - 1
		}
		key := binKey{p.entityID + "\x00" + publicMetricTagsKey(p.Tags), index}
		bin, ok := bins[key]
		if !ok {
			bin = p
			bin.Time = start.Add(time.Duration(float64(index) * span / float64(maxPoints)))
			bin.Value = nil
			bin.Count = 0
		}
		if p.Value != nil {
			value := *p.Value
			if bin.Value != nil {
				value += *bin.Value
			}
			bin.Value = &value
		}
		bin.Count += p.Count
		bins[key] = bin
	}
	keys := make([]binKey, 0, len(bins))
	for key := range bins {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].series != keys[j].series {
			return keys[i].series < keys[j].series
		}
		return keys[i].index < keys[j].index
	})
	result := make([]publicMetricPoint, 0, len(keys))
	for _, key := range keys {
		result = append(result, bins[key])
	}
	return result
}

func publicGetPingMetricStats(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params publicPingMetricStatsParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	end := metricQueryTimeOrDefault(firstMetricQueryTime(params.End, params.EndTime), time.Now().UTC())
	startFallback := end.Add(-metricQueryHours(params.Hours))
	start := metricQueryTimeOrDefault(firstMetricQueryTime(params.Start, params.StartTime), startFallback)
	if err := validateMetricWindow(start, end); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	if params.MaxPoints > maxMetricPoints || params.MaxPoints < 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "max_points must be between 1 and 4096", nil)
	}
	if !end.After(start) {
		return nil, rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
	}

	requestedEntities := normalizeStringList(params.EntityIDs, []string{firstNonEmpty(params.EntityID, params.UUID)})
	entityIDs, rpcErr := publicMetricEntityIDs(ctx, requestedEntities)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if len(entityIDs) == 0 {
		return publicPingMetricStatsResponse{
			Start: start.UTC(),
			End:   end.UTC(),
			Stats: []publicPingMetricTaskStats{},
			Count: 0,
		}, nil
	}

	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}

	taskList, err := tasks.GetAllPingTasks()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping tasks: "+err.Error(), nil)
	}
	taskMap := make(map[string]models.PingTask, len(taskList))
	for _, task := range taskList {
		taskMap[strconv.FormatUint(uint64(task.Id), 10)] = task
	}
	taskFilter := normalizePingMetricTaskIDs(params.TaskID, params.TaskIDs)

	maxPoints := params.MaxPoints
	if maxPoints <= 0 {
		maxPoints = defaultMetricQueryPoints
	}
	now := time.Now().UTC()
	interval := metricDownsampleInterval(end.Sub(start), maxPoints)
	interval = store.CompatibleSeriesInterval(start, now, interval)

	groupsByEntity, err := loadPublicPingMetricAggregateGroups(ctx, store, entityIDs, start, end, interval, now)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to query ping stats: "+err.Error(), nil)
	}
	stats := make([]publicPingMetricTaskStats, 0)
	for _, entityID := range entityIDs {
		stats = append(stats, publicPingStatsFromAggregateGroups(entityID, groupsByEntity[entityID], taskMap, taskFilter)...)
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].EntityID != stats[j].EntityID {
			return stats[i].EntityID < stats[j].EntityID
		}
		return stats[i].TaskID < stats[j].TaskID
	})

	return publicPingMetricStatsResponse{
		Start:           start.UTC(),
		End:             end.UTC(),
		IntervalSeconds: interval.Seconds(),
		Stats:           stats,
		Count:           len(stats),
	}, nil
}

type publicMetricSeriesGroup struct {
	entityID string
	tagsKey  string
	tags     map[string]string
	points   []publicMetricPoint
}

func splitPublicMetricSeries(base publicMetricSeries) []publicMetricSeries {
	if len(base.Points) == 0 {
		base.Count = 0
		return []publicMetricSeries{base}
	}

	groups := make(map[string]*publicMetricSeriesGroup)
	order := make([]string, 0)
	for _, point := range base.Points {
		entityID := base.EntityID
		if point.entityID != "" {
			entityID = point.entityID
		}
		tags := point.Tags
		point.Tags = tags
		tagsKey := publicMetricTagsKey(tags)
		key := entityID + "\x00" + tagsKey
		group := groups[key]
		if group == nil {
			group = &publicMetricSeriesGroup{
				entityID: entityID,
				tagsKey:  tagsKey,
				tags:     clonePublicMetricTags(tags),
			}
			groups[key] = group
			order = append(order, key)
		}
		group.points = append(group.points, point)
	}

	sort.SliceStable(order, func(i, j int) bool {
		a := groups[order[i]]
		b := groups[order[j]]
		if a.entityID != b.entityID {
			return a.entityID < b.entityID
		}
		return a.tagsKey < b.tagsKey
	})

	out := make([]publicMetricSeries, 0, len(order))
	for _, key := range order {
		group := groups[key]
		item := base
		item.EntityID = group.entityID
		item.Tags = group.tags
		item.Points = group.points
		item.Count = len(group.points)
		out = append(out, item)
	}
	return out
}

// adaptiveFillPublicMetricSeries inserts only the null points needed to mark
// chart boundaries and real collection gaps. The typical collection interval
// is inferred per metric/entity/tag series, so sparse periodic data does not
// expand into hundreds of artificial empty buckets.
func adaptiveFillPublicMetricSeries(series publicMetricSeries, start, end time.Time) publicMetricSeries {
	pointTimes := make([]time.Time, len(series.Points))
	deltas := make([]time.Duration, 0, len(series.Points))
	for i, point := range series.Points {
		pointTimes[i] = point.Time
		if i > 0 {
			delta := point.Time.Sub(pointTimes[i-1])
			if delta > 0 {
				deltas = append(deltas, delta)
			}
		}
	}

	expectedInterval := time.Duration(series.IntervalSeconds * float64(time.Second))
	// Two deltas are the minimum needed to distinguish a regular cadence from
	// one isolated long gap. A lower quartile keeps outages from inflating the
	// inferred cadence when the rest of the series is regular.
	if len(deltas) >= 2 {
		sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
		observedInterval := deltas[(len(deltas)-1)/4]
		if observedInterval > expectedInterval {
			expectedInterval = observedInterval
		}
	}
	if expectedInterval > 0 {
		series.IntervalSeconds = expectedInterval.Seconds()
	}

	nullPoint := func(at time.Time) publicMetricPoint {
		return publicMetricPoint{
			Time:  at.UTC(),
			Value: nil,
			Tags:  series.Tags,
		}
	}
	filled := make([]publicMetricPoint, 0, len(series.Points)+2)
	if len(pointTimes) == 0 || start.Before(pointTimes[0]) {
		filled = append(filled, nullPoint(start))
	}
	for i, point := range series.Points {
		if i > 0 && expectedInterval > 0 && series.Points[i-1].Value != nil && point.Value != nil {
			delta := pointTimes[i].Sub(pointTimes[i-1])
			if delta > expectedInterval+expectedInterval/2 {
				filled = append(filled, nullPoint(pointTimes[i-1].Add(expectedInterval)))
			}
		}
		filled = append(filled, point)
	}
	// Do not append a trailing null after real data. It would make the final chart
	// bucket blank regardless of whether the tail is a collection delay or a gap.
	if len(pointTimes) == 0 {
		filled = append(filled, nullPoint(end))
	}
	series.Points = filled
	series.Count = len(filled)
	return series
}

func publicMetricTagsKey(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(tags[key])
		b.WriteByte('\x00')
	}
	return b.String()
}

func clonePublicMetricTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func publicMetricEntityIDs(ctx context.Context, requested []string) ([]string, *rpc.JsonRpcError) {
	if len(requested) > maxMetricEntities {
		return nil, rpc.MakeError(rpc.InvalidParams, "too many entities", nil)
	}
	hidden, err := clients.HiddenClients()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client information: "+err.Error(), nil)
	}
	isLogin := isLoginFromCtx(ctx)
	// Existence is the map key, not "the value is false": the metric store keeps a
	// node's retained history after its client row is deleted, so an absent UUID is
	// a deleted node, not a visible one. Rejecting it here is what stops an
	// anonymous caller from reading the history of a node the panel no longer lists.
	allVisible := make([]string, 0, len(hidden))
	for uuid := range hidden {
		if clientHistoryReadable(hidden, isLogin, uuid) {
			allVisible = append(allVisible, uuid)
		}
	}
	sort.Strings(allVisible)
	if len(requested) == 0 {
		if len(allVisible) > maxMetricEntities {
			return nil, rpc.MakeError(rpc.InvalidParams, "too many entities; select a subset", nil)
		}
		return allVisible, nil
	}
	out := make([]string, 0, len(requested))
	for _, entityID := range requested {
		if clientHistoryReadable(hidden, isLogin, entityID) {
			out = append(out, entityID)
		}
	}
	return out, nil
}

func normalizeStringList(groups ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, group := range groups {
		for _, item := range group {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

func firstMetricQueryTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func metricQueryTimeOrDefault(value *time.Time, fallback time.Time) time.Time {
	if value == nil {
		return fallback.UTC()
	}
	return value.UTC()
}

func metricQueryHours(hours float64) time.Duration {
	if hours <= 0 {
		return 4 * time.Hour
	}
	if math.IsNaN(hours) || math.IsInf(hours, 0) || hours > 366*24 {
		return 367 * 24 * time.Hour
	}
	return time.Duration(hours * float64(time.Hour))
}

func resolveMetricMaxPoints(metricKey string, params publicMetricQueryParams) (int, error) {
	maxPoints := params.MaxPoints
	if maxPoints == 0 {
		maxPoints = defaultMetricQueryPoints
	}
	if v, ok := params.PointsByMetric[metricKey]; ok {
		maxPoints = v
	}
	if v, ok := params.MaxPointsByMetric[metricKey]; ok {
		maxPoints = v
	}
	if maxPoints <= 0 || maxPoints > maxMetricPoints {
		return 0, fmt.Errorf("max points for %s must be between 1 and %d", metricKey, maxMetricPoints)
	}
	return maxPoints, nil
}

// trafficQuantityAtCycleStart 说明 `traffic.*` 是**周期累计**而不是每区间的量。
//
// 面板存的是探针上报的周期累计值（自计费周期重置日以来的字节数），存储层忠实保存它。于是"对窗口内
// 的取值求和"没有任何合理语义：那是把一个持续增长的总量按采样次数反复累加。
//
// 现场实测：全队 10 个节点、当日窗口，`sum` 得到 **3.87 PB**，而同一份数据取差值不到 **1 TB**。
// 主题里那句"今日流量"就是这么算出来的，所以它显示的每个数字都大得离谱。
//
// Sum reads the separately stored, continuity-validated interval amounts.
// Old cumulative history cannot be reconstructed safely and remains unknown.
//
// 想要累计本身的调用方请求 `last`，那是另一个问题（"本周期至今用了多少"），且不受影响。
var trafficCumulativeMetrics = map[string]struct{}{
	"traffic.up":   {},
	"traffic.down": {},
}

// resolveMetricAggregation 决定某个指标用哪种算法取值。
func resolveMetricAggregation(metricKey string, params publicMetricQueryParams) metric.Aggregation {
	raw := firstNonEmpty(params.Aggregation, params.Algorithm)
	if v := firstNonEmpty(
		params.AggregationByMetric[metricKey],
		params.AlgorithmByMetric[metricKey],
	); v != "" {
		raw = v
	}
	if raw == "" {
		if _, ok := trafficCumulativeMetrics[metricKey]; ok {
			return metric.AggSum
		}
		raw = string(metric.AggAvg)
	}
	normalized := normalizeMetricAggregation(raw)

	return metric.Aggregation(normalized)
}

// Sum reads already validated interval amounts; never difference bucket endpoints.
func publicMetricStorageKey(key string, aggregation metric.Aggregation) string {
	if aggregation == metric.AggSum {
		switch key {
		case metricstore.MetricTrafficUp:
			return metricstore.MetricTrafficIntervalUp
		case metricstore.MetricTrafficDown:
			return metricstore.MetricTrafficIntervalDown
		}
	}
	return key
}

func resolveMetricFillEmpty(params publicMetricQueryParams) bool {
	return params.FillEmpty != nil && *params.FillEmpty
}

func publicMetricValue(value float64) *float64 {
	return &value
}

func publicRawMetricValue(metricName string, value float64, fillEmpty bool) *float64 {
	if isNullPingMetricValue(metricName, value, fillEmpty) {
		return nil
	}
	return publicMetricValue(value)
}

func isNullPingMetricValue(metricName string, value float64, fillEmpty bool) bool {
	if !fillEmpty || value != -1 {
		return false
	}
	return metricName == metricstore.MetricPingLatency || metricName == metricstore.MetricPingLoss
}

type publicPingMetricAggregateGroups struct {
	Avg                  map[string][]metric.AggregatePoint
	Min                  map[string][]metric.AggregatePoint
	Max                  map[string][]metric.AggregatePoint
	Last                 map[string][]metric.AggregatePoint
	P50                  map[string][]metric.AggregatePoint
	P95                  map[string][]metric.AggregatePoint
	P99                  map[string][]metric.AggregatePoint
	StdDev               map[string][]metric.AggregatePoint
	Loss                 map[string][]metric.AggregatePoint
	LossAvailable        bool
	Success              *publicPingMetricAggregateGroups
	CoverageStart        *time.Time
	CoverageEndExclusive *time.Time
	Resolution           time.Duration
}

func loadPublicPingMetricAggregateGroups(ctx context.Context, store *metric.Store, entityIDs []string, start, end time.Time, interval time.Duration, now time.Time) (map[string]publicPingMetricAggregateGroups, error) {
	latencyAggregations := []metric.Aggregation{
		metric.AggAvg,
		metric.AggMin,
		metric.AggMax,
		metric.AggLast,
		metric.AggP50,
		metric.AggP95,
		metric.AggP99,
		metric.AggStdDev,
	}
	loaded, err := store.SeriesBatch(ctx, metric.BatchSeriesQuery{
		Specs: []metric.BatchSeriesSpec{
			{MetricName: metricstore.MetricPingLatency, Aggregations: latencyAggregations, Interval: interval, PreserveSeries: true, WholeWindow: true},
			{MetricName: metricstore.MetricPingSuccessLatency, Aggregations: latencyAggregations, Interval: interval, PreserveSeries: true, WholeWindow: true},
			{MetricName: metricstore.MetricPingLoss, Aggregations: []metric.Aggregation{metric.AggAvg}, Interval: interval, PreserveSeries: true, WholeWindow: true},
		},
		EntityIDs: entityIDs,
		Start:     start,
		End:       end,
		Order:     metric.OrderAsc,
	}, now)
	if err != nil {
		return nil, err
	}
	latency := loaded.Values[metricstore.MetricPingLatency]
	lossPoints := loaded.Values[metricstore.MetricPingLoss][metric.AggAvg]

	avg := groupPingMetricAggregatePointsByEntity(latency[metric.AggAvg])
	minimum := groupPingMetricAggregatePointsByEntity(latency[metric.AggMin])
	maximum := groupPingMetricAggregatePointsByEntity(latency[metric.AggMax])
	last := groupPingMetricAggregatePointsByEntity(latency[metric.AggLast])
	p50 := groupPingMetricAggregatePointsByEntity(latency[metric.AggP50])
	p95 := groupPingMetricAggregatePointsByEntity(latency[metric.AggP95])
	p99 := groupPingMetricAggregatePointsByEntity(latency[metric.AggP99])
	stddev := groupPingMetricAggregatePointsByEntity(latency[metric.AggStdDev])
	loss := groupPingMetricAggregatePointsByEntity(lossPoints)
	success := loaded.Values[metricstore.MetricPingSuccessLatency]
	successAvg := groupPingMetricAggregatePointsByEntity(success[metric.AggAvg])
	successMin := groupPingMetricAggregatePointsByEntity(success[metric.AggMin])
	successMax := groupPingMetricAggregatePointsByEntity(success[metric.AggMax])
	successLast := groupPingMetricAggregatePointsByEntity(success[metric.AggLast])
	successP50 := groupPingMetricAggregatePointsByEntity(success[metric.AggP50])
	successP95 := groupPingMetricAggregatePointsByEntity(success[metric.AggP95])
	successP99 := groupPingMetricAggregatePointsByEntity(success[metric.AggP99])
	successStd := groupPingMetricAggregatePointsByEntity(success[metric.AggStdDev])

	entitySet := make(map[string]struct{})
	for _, groups := range []map[string]map[string][]metric.AggregatePoint{avg, minimum, maximum, last, p50, p99, stddev, loss} {
		for currentEntityID := range groups {
			entitySet[currentEntityID] = struct{}{}
		}
	}
	result := make(map[string]publicPingMetricAggregateGroups, len(entitySet))
	resolution := loaded.Resolutions[metricstore.MetricPingLatency]
	coverageStart, coverageEnd := metricBucketCoverage(start, end, resolution)
	for currentEntityID := range entitySet {
		entityLoss := loss[currentEntityID]
		result[currentEntityID] = publicPingMetricAggregateGroups{
			Avg:           avg[currentEntityID],
			Min:           minimum[currentEntityID],
			Max:           maximum[currentEntityID],
			Last:          last[currentEntityID],
			P50:           p50[currentEntityID],
			P95:           p95[currentEntityID],
			P99:           p99[currentEntityID],
			StdDev:        stddev[currentEntityID],
			Loss:          entityLoss,
			LossAvailable: pingMetricGroupsHaveData(entityLoss),
			CoverageStart: coverageStart, CoverageEndExclusive: coverageEnd, Resolution: resolution,
		}
		entry := result[currentEntityID]
		entry.Success = &publicPingMetricAggregateGroups{
			Avg: successAvg[currentEntityID], Min: successMin[currentEntityID], Max: successMax[currentEntityID],
			Last: successLast[currentEntityID], P50: successP50[currentEntityID], P95: successP95[currentEntityID], P99: successP99[currentEntityID], StdDev: successStd[currentEntityID],
		}
		result[currentEntityID] = entry
	}
	return result, nil
}

// pingStatGroupSep 分隔「任务」与「实际地址族」两个分组维度。
// 用控制字符是因为 task_id 与 family 都不会包含它，拼接不会产生歧义。
const pingStatGroupSep = "\x1f"

// pingStatGroupKey 把任务与实际地址族组成一个分组键。
//
// family 为空时【原样返回 task_id】—— 这是关键：旧 agent 不上报族，历史数据也
// 没有 family 标签，此时分组键与改动前逐字一致，统计结果不会发生变化。
func pingStatGroupKey(taskID, family string, dimensions ...string) string {
	if len(dimensions) == 2 && (dimensions[0] != "" || dimensions[1] != "") {
		return strings.Join([]string{taskID, family, dimensions[0], dimensions[1]}, pingStatGroupSep)
	}
	if family == "" {
		return taskID
	}
	return taskID + pingStatGroupSep + family
}

// splitPingStatGroupKey 还原分组键。没有分隔符时族为空。
func splitPingStatGroupKey(key string) (taskID, family string) {
	if index := strings.Index(key, pingStatGroupSep); index >= 0 {
		return key[:index], strings.Split(key[index+len(pingStatGroupSep):], pingStatGroupSep)[0]
	}
	return key, ""
}

func groupPingMetricAggregatePointsByEntity(points []metric.AggregatePoint) map[string]map[string][]metric.AggregatePoint {
	out := make(map[string]map[string][]metric.AggregatePoint)
	for _, point := range points {
		taskID := strings.TrimSpace(point.Tags["task_id"])
		if taskID == "" {
			continue
		}
		// 同一个任务下，实际走 v4 与走 v6 的点必须落在不同的组里，
		// 否则两条路径会被算成一个延迟和一个丢包率。
		key := pingStatGroupKey(taskID, strings.TrimSpace(point.Tags["family"]), strings.TrimSpace(point.Tags["protocol"]), strings.TrimSpace(point.Tags["role"]))
		byTask := out[point.EntityID]
		if byTask == nil {
			byTask = make(map[string][]metric.AggregatePoint)
			out[point.EntityID] = byTask
		}
		byTask[key] = append(byTask[key], point)
	}
	return out
}

func pingMetricGroupsHaveData(groups map[string][]metric.AggregatePoint) bool {
	for _, points := range groups {
		for _, point := range points {
			if point.Count > 0 {
				return true
			}
		}
	}
	return false
}

func publicPingStatsFromAggregateGroups(entityID string, groups publicPingMetricAggregateGroups, taskMap map[string]models.PingTask, taskFilter map[string]bool) []publicPingMetricTaskStats {
	groupKeys := make(map[string]struct{})
	for _, group := range []map[string][]metric.AggregatePoint{
		groups.Avg, groups.Min, groups.Max, groups.Last, groups.P50, groups.P99, groups.StdDev, groups.Loss,
	} {
		for key := range group {
			groupKeys[key] = struct{}{}
		}
	}

	out := make([]publicPingMetricTaskStats, 0, len(groupKeys))
	for key := range groupKeys {
		// 分组键是「任务 + 实际地址族」；族为空时它就是纯 task_id。
		taskID, family := splitPingStatGroupKey(key)
		if len(taskFilter) > 0 && !taskFilter[taskID] {
			continue
		}

		total := aggregatePointCount(groups.Avg[key])
		if total == 0 {
			total = aggregatePointCount(groups.Loss[key])
		}
		if total == 0 {
			continue
		}

		lossKnown := aggregatePointCount(groups.Loss[key]) == total
		lossRate, valid, approximate := publicPingLossRate(groups.Avg[key], groups.Loss[key], total, lossKnown)
		avg, stddev := pingSuccessfulMoments(groups.Avg[key], groups.StdDev[key], total-valid, valid)
		p50 := singleAggregateValue(groups.P50[key])
		p95 := singleAggregateValue(groups.P95[key])
		p99 := singleAggregateValue(groups.P99[key])
		minimum := positiveAggregateMin(groups.Min[key])
		maximum := positiveAggregateMax(groups.Max[key])
		latest := latestPositiveAggregate(groups.Last[key])
		quality := "legacy_success_only"
		if valid < total || approximate {
			minimum, p50, p95, p99 = nil, nil, nil, nil
			quality = "legacy_quantiles_unknown"
		}
		if approximate {
			avg, stddev = nil, nil
		}
		if groups.Success != nil && valid > 0 && aggregatePointCount(groups.Success.Avg[key]) == valid {
			success := groups.Success
			avg, stddev = pingSuccessfulMoments(success.Avg[key], success.StdDev[key], 0, valid)
			minimum, maximum = positiveAggregateMin(success.Min[key]), positiveAggregateMax(success.Max[key])
			p50, p99 = singleAggregateValue(success.P50[key]), singleAggregateValue(success.P99[key])
			p95 = singleAggregateValue(success.P95[key])
			quality = "success_only_v2"
		}
		if valid == 0 {
			avg, stddev, minimum, maximum, p50, p95, p99 = nil, nil, nil, nil, nil, nil, nil
		}

		tags := map[string]string{"task_id": taskID}
		if family != "" {
			// 与 metricstore 写入 ping 序列时用的是同一个标签名，
			// 前端因此能直接按它把统计与曲线对上。
			tags["family"] = family
		}
		parts := strings.Split(key, pingStatGroupSep)
		protocol, role := "", ""
		if len(parts) == 4 {
			protocol, role = parts[2], parts[3]
		}
		if protocol != "" {
			tags["protocol"] = protocol
		}
		if role != "" {
			tags["role"] = role
		}

		stat := publicPingMetricTaskStats{
			EntityID: entityID,
			TaskID:   taskID,
			Family:   family,
			Protocol: protocol, Role: role,
			Semantics: "successful_latency_all_attempts_loss_v2", Quality: quality,
			PercentilesApproximate: p50 != nil || p99 != nil,
			Tags:                   tags,
			Total:                  total,
			Valid:                  valid,
			ValidKnown:             lossKnown,
			LossApproximate:        approximate,
			Min:                    minimum,
			Max:                    maximum,
			Avg:                    avg,
			Latest:                 latest,
			P50:                    p50,
			P95:                    p95,
			P99:                    p99,
			StdDev:                 stddev,
			WindowSemantics:        "bucket_coverage", CoverageStart: groups.CoverageStart, CoverageEndExclusive: groups.CoverageEndExclusive, BackingIntervalSeconds: groups.Resolution.Seconds(),
		}
		if lossKnown {
			stat.Loss = publicMetricValue(lossRate)
		} else {
			stat.Quality = "legacy_loss_unknown"
		}
		if task, ok := taskMap[taskID]; ok {
			stat.Name = task.Name
			stat.Type = task.Type
			stat.Interval = task.Interval
		}
		if p50 != nil && p99 != nil && *p50 > 0 && *p99 >= *p50 {
			adjustedBase := math.Max(math.Min(*p50, 50.0), 10.0)
			stat.P99P50Ratio = publicMetricValue((*p99 - *p50) / adjustedBase)
		}
		out = append(out, stat)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].TaskID != out[j].TaskID {
			return out[i].TaskID < out[j].TaskID
		}
		// 同一任务内按族排序，保证输出顺序稳定（Go 的 map 遍历是随机的）。
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		if out[i].Protocol != out[j].Protocol {
			return out[i].Protocol < out[j].Protocol
		}
		return out[i].Role < out[j].Role
	})
	return out
}

func normalizePingMetricTaskIDs(taskID any, taskIDs []any) map[string]bool {
	out := make(map[string]bool)
	add := func(value any) {
		switch v := value.(type) {
		case nil:
			return
		case string:
			if raw := strings.TrimSpace(v); raw != "" {
				out[raw] = true
			}
		case float64:
			out[strconv.FormatInt(int64(v), 10)] = true
		case int:
			out[strconv.Itoa(v)] = true
		case int64:
			out[strconv.FormatInt(v, 10)] = true
		case jsonNumber:
			if raw := strings.TrimSpace(v.String()); raw != "" {
				out[raw] = true
			}
		default:
			raw := strings.TrimSpace(fmt.Sprint(v))
			if raw != "" {
				out[raw] = true
			}
		}
	}
	add(taskID)
	for _, value := range taskIDs {
		add(value)
	}
	return out
}

type jsonNumber interface {
	String() string
}

func aggregatePointCount(points []metric.AggregatePoint) int {
	total := 0
	for _, point := range points {
		total += point.Count
	}
	return total
}

func publicPingLossRate(latencyPoints, lossPoints []metric.AggregatePoint, total int, lossAvailable bool) (float64, int, bool) {
	if total <= 0 {
		return 0, 0, !lossAvailable
	}
	if lossAvailable {
		lossCount := 0.0
		for _, point := range lossPoints {
			if point.Count <= 0 {
				continue
			}
			lossCount += math.Max(0, math.Min(1, point.Value)) * float64(point.Count)
		}
		lost := int(math.Round(lossCount))
		if lost > total {
			lost = total
		}
		return lossCount / float64(total) * 100, total - lost, false
	}

	lost := 0
	valid := 0
	for _, point := range latencyPoints {
		if point.Count <= 0 {
			continue
		}
		if point.Value < 0 {
			lost += point.Count
			continue
		}
		valid += point.Count
	}
	return float64(lost) / float64(total) * 100, valid, true
}

func weightedAggregateValue(points []metric.AggregatePoint, skipNegative bool) (*float64, int) {
	sum := 0.0
	count := 0
	for _, point := range points {
		if point.Count <= 0 {
			continue
		}
		if skipNegative && point.Value < 0 {
			continue
		}
		sum += point.Value * float64(point.Count)
		count += point.Count
	}
	if count == 0 {
		return nil, 0
	}
	value := sum / float64(count)
	return &value, count
}

// Quantiles must come from one globally merged digest, never averaged buckets.
func singleAggregateValue(points []metric.AggregatePoint) *float64 {
	if len(points) != 1 || points[0].Count <= 0 || points[0].Value < 0 {
		return nil
	}
	return publicMetricValue(points[0].Value)
}

// Legacy failed attempts have value -1. Their contribution can be removed
// from moments, but a mixed digest cannot reconstruct successful quantiles.
func pingSuccessfulMoments(means, deviations []metric.AggregatePoint, lost, valid int) (*float64, *float64) {
	if valid <= 0 {
		return nil, nil
	}
	sum, sumsq := float64(lost), -float64(lost)
	momentsKnown := len(means) > 0
	for _, p := range means {
		sum += p.Value * float64(p.Count)
		found := false
		for _, d := range deviations {
			if d.Bucket.Equal(p.Bucket) && d.Count == p.Count {
				sumsq += (d.Value*d.Value + p.Value*p.Value) * float64(p.Count)
				found = true
				break
			}
		}
		momentsKnown = momentsKnown && found
	}
	mean := sum / float64(valid)
	if mean < 0 {
		return nil, nil
	}
	if !momentsKnown {
		return &mean, nil
	}
	variance := sumsq/float64(valid) - mean*mean
	stddev := math.Sqrt(math.Max(0, variance))
	return &mean, &stddev
}

func positiveAggregateMin(points []metric.AggregatePoint) *float64 {
	var out *float64
	for _, point := range points {
		if point.Count <= 0 || point.Value < 0 {
			continue
		}
		value := point.Value
		if out == nil || value < *out {
			out = &value
		}
	}
	return out
}

func positiveAggregateMax(points []metric.AggregatePoint) *float64 {
	var out *float64
	for _, point := range points {
		if point.Count <= 0 || point.Value < 0 {
			continue
		}
		value := point.Value
		if out == nil || value > *out {
			out = &value
		}
	}
	return out
}

func latestPositiveAggregate(points []metric.AggregatePoint) *float64 {
	var out *float64
	var latest time.Time
	for _, point := range points {
		if point.Count <= 0 {
			continue
		}
		if out == nil || point.Bucket.After(latest) {
			value := point.Value
			out = &value
			latest = point.Bucket
		}
	}
	if out != nil && *out < 0 {
		return nil
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeMetricAggregation(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "average", "mean":
		return string(metric.AggAvg)
	case "std_dev", "stddev_pop", "std_dev_pop":
		return string(metric.AggStdDev)
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func metricDownsampleInterval(rangeDuration time.Duration, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = defaultMetricQueryPoints
	}
	nanos := rangeDuration.Nanoseconds()
	if nanos <= 0 {
		return time.Second
	}
	interval := time.Duration((nanos + int64(maxPoints) - 1) / int64(maxPoints))
	if interval < time.Second {
		return time.Second
	}
	return metric.CeilStandardInterval(interval)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
