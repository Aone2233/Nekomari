package sla

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
)

// Metric names this package reads. They are the ones the agent already reports; the
// point of the feature is to say something with the existing data, not to collect more.
const (
	// MetricLoss carries the fraction of probes lost in a bucket, 0..1.
	MetricLoss = "ping.loss"
	// MetricLatency carries the mean round trip of a bucket in milliseconds, and -1
	// for a bucket in which every probe failed.
	MetricLatency = "ping.latency_ms"
	// MetricPresence is any series a node reports continuously. Availability of a
	// *node* is derived from whether it reported at all; see PresenceFromSamples.
	MetricPresence = "cpu.usage"
)

// SeriesReader is the slice of the metric store this package needs.
//
// An interface rather than *metric.Store so the query layer can be tested without a
// database: the logic that decides what the returned points *mean* is the part worth
// testing, and it is the part that a table-driven test with a fake reader can reach.
// The store satisfies it as it stands.
type SeriesReader interface {
	Series(ctx context.Context, query metric.AggregateQuery, now time.Time) ([]metric.AggregatePoint, error)
}

// BatchSeriesReader is the same, for one query covering many entities.
//
// Worth its own interface rather than being folded into SeriesReader: a report over a
// fleet is one query here and N there, and the difference is not academic — ten nodes
// across three metrics is 30 queries the long way. It stays separate so a reader
// that only implements Series keeps working.
type BatchSeriesReader interface {
	SeriesBatch(ctx context.Context, query metric.BatchSeriesQuery, now time.Time) (metric.BatchSeriesResult, error)
}

// Bucket is one entity's series for one metric, keyed for the report.
type Bucket struct {
	EntityID string
	// Tags identify the series within an entity: task_id, protocol, role, family.
	Tags    map[string]string
	Samples []Sample
}

// ReadSeriesPerEntity fetches one metric for many entities in a single query and
// splits the result by entity, keeping each series' tags.
//
// The split is the point. `PreserveSeries` keeps series apart by their tags, so the
// result arrives as points whose Tags distinguish one task from another; grouping them
// here is what lets the report answer per task instead of averaging a node's five
// targets into one meaningless number.
func ReadSeriesPerEntity(ctx context.Context, reader BatchSeriesReader, query SeriesQuery, entityIDs []string, now time.Time) ([]Bucket, error) {
	if reader == nil {
		return nil, fmt.Errorf("no metric store")
	}
	if query.Interval <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}
	aggregation := query.Aggregation
	if aggregation == "" {
		aggregation = metric.AggAvg
	}
	result, err := reader.SeriesBatch(ctx, metric.BatchSeriesQuery{
		Specs: []metric.BatchSeriesSpec{{
			MetricName:     query.MetricName,
			Aggregations:   []metric.Aggregation{aggregation},
			Interval:       query.Interval,
			PreserveSeries: true,
		}},
		EntityIDs: entityIDs,
		Start:     query.Start,
		End:       query.End,
		Tags:      query.Tags,
	}, now)
	if err != nil {
		return nil, err
	}

	byAggregation, ok := result.Values[query.MetricName]
	if !ok {
		return nil, nil
	}
	points := byAggregation[aggregation]

	// One bucket per (entity, series). The key uses a separator that cannot appear in
	// an entity id or a tag value, so two different series can never collide.
	order := make([]string, 0)
	buckets := make(map[string]*Bucket)
	for _, point := range points {
		key := point.EntityID + "\x00" + seriesTagsKey(point.Tags)
		bucket := buckets[key]
		if bucket == nil {
			bucket = &Bucket{EntityID: point.EntityID, Tags: cloneTags(point.Tags)}
			buckets[key] = bucket
			order = append(order, key)
		}
		bucket.Samples = append(bucket.Samples, Sample{
			Bucket: point.Bucket.UTC(),
			Value:  point.Value,
			Count:  point.Count,
		})
	}

	// Deterministic order: a report that reshuffles between requests cannot be diffed.
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]Bucket, 0, len(order))
	for _, key := range order {
		bucket := buckets[key]
		sort.Slice(bucket.Samples, func(i, j int) bool {
			return bucket.Samples[i].Bucket.Before(bucket.Samples[j].Bucket)
		})
		// Deduplicated here, at the boundary, so every consumer downstream sees the same
		// grid: the cadence, the coverage, the incident grouping and the presence union
		// all assume one entry per bucket, and a caller that forgot would silently get a
		// different answer. See dedupe() for the live bug this fixes.
		bucket.Samples = dedupe(bucket.Samples)
		out = append(out, *bucket)
	}
	return out, nil
}

// seriesTagsKey renders tags into a stable key.
func seriesTagsKey(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder []byte
	for _, key := range keys {
		builder = append(builder, key...)
		builder = append(builder, '=')
		builder = append(builder, tags[key]...)
		builder = append(builder, ';')
	}
	return string(builder)
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

// CompleteGrid fills a series' missing buckets with gaps, given the grid the query
// asked for.
//
// It exists because "absent" and "empty" mean the same thing to a reader and different
// things to the store: with `PreserveSeries`, a series that reported nothing in a
// bucket may come back with no point at all, or with a point whose Count is 0. Both
// are gaps, and a report that treated only one of them as a gap would silently count
// the other as a measurement of zero — which is exactly the failure mode where a dead
// node looks healthy.
//
// The grid is derived from the query's interval and window, so the result has a slot
// for every bucket the window should hold. Trailing buckets that no series reached are
// not invented: the grid stops at the last bucket any series carried.
func CompleteGrid(buckets []Bucket, start, end time.Time, interval time.Duration) []Bucket {
	if interval <= 0 || len(buckets) == 0 {
		return buckets
	}
	// Buckets are aligned the same way the store aligns them, so a filled slot lands
	// on the same boundary as a reported one.
	var latest time.Time
	for _, bucket := range buckets {
		for _, sample := range bucket.Samples {
			if sample.Bucket.After(latest) {
				latest = sample.Bucket
			}
		}
	}
	if latest.IsZero() {
		return buckets
	}

	out := make([]Bucket, 0, len(buckets))
	for _, bucket := range buckets {
		present := make(map[int64]Sample, len(bucket.Samples))
		for _, sample := range bucket.Samples {
			present[sample.Bucket.Unix()] = sample
		}
		filled := make([]Sample, 0, len(bucket.Samples))
		for slot := start.UTC().Truncate(interval); !slot.After(latest); slot = slot.Add(interval) {
			if sample, ok := present[slot.Unix()]; ok {
				filled = append(filled, sample)
				continue
			}
			filled = append(filled, Sample{Bucket: slot, Value: 0, Count: 0})
		}
		bucket.Samples = filled
		out = append(out, bucket)
	}
	return out
}

// ReportRequest is one SLA report to build.
type ReportRequest struct {
	Reader    BatchSeriesReader
	EntityIDs []string
	// TaskNames maps a task id to its display name, so a report says what a task is
	// rather than only which number it is.
	TaskNames map[string]string
	Start     time.Time
	End       time.Time
	Interval  time.Duration
	// WindowLabel is what the caller asked for, carried through so the report can say
	// "last 30 days" without recomputing it from the timestamps.
	WindowLabel string
	// Clamped explains a shortened window, from WindowStart.
	Clamped string
	Now     time.Time
}

// Build reads the series and assembles the whole report.
//
// One query per metric for the entire fleet: the report covers every requested entity
// in two reads, not two per node. The presence grid is derived from the loss series'
// own buckets rather than read separately — a node that reported no ping result also
// reported no resource metrics, so a second series would cost a query and add no
// information.
func Build(ctx context.Context, req ReportRequest) (Report, error) {
	if req.Now.IsZero() {
		req.Now = time.Now().UTC()
	}
	report := Report{
		Start:           req.Start.UTC(),
		End:             req.End.UTC(),
		Window:          req.WindowLabel,
		IntervalSeconds: req.Interval.Seconds(),
		Clamped:         req.Clamped,
		Nodes:           []NodeReport{},
	}
	if req.Reader == nil {
		return report, fmt.Errorf("no metric store")
	}

	buckets, err := ReadSeriesPerEntity(ctx, req.Reader, SeriesQuery{
		MetricName:  MetricLoss,
		Start:       req.Start,
		End:         req.End,
		Interval:    req.Interval,
		Aggregation: metric.AggAvg,
	}, req.EntityIDs, req.Now)
	if err != nil {
		return report, err
	}
	buckets = CompleteGrid(buckets, req.Start, req.End, req.Interval)

	// Latency is a second series for the same (entity, task) pairs. It is read separately
	// rather than derived from the loss series because they carry different values — a
	// bucket can be measurably slow without being lost — and the report would show
	// "no data" for every task without it, which is exactly what it did until this was
	// added: `LatencyFromSamples` existed and nothing called it.
	latencyBuckets, err := ReadSeriesPerEntity(ctx, req.Reader, SeriesQuery{
		MetricName:  MetricLatency,
		Start:       req.Start,
		End:         req.End,
		Interval:    req.Interval,
		Aggregation: metric.AggAvg,
	}, req.EntityIDs, req.Now)
	if err != nil {
		// A missing latency series is not worth failing the whole report for: the
		// availability figures are the reason the report exists, and an instance that
		// never collected ping latency should still get a coverage answer.
		latencyBuckets = nil
	}

	// Group by entity, then by task, so one node's five targets stay five reports.
	byEntity := make(map[string]map[string][]Sample)
	latencyByEntity := make(map[string]map[string][]Sample)
	entityOrder := make([]string, 0, len(req.EntityIDs))
	seen := make(map[string]bool, len(req.EntityIDs))
	for _, entityID := range req.EntityIDs {
		if !seen[entityID] {
			seen[entityID] = true
			entityOrder = append(entityOrder, entityID)
		}
	}
	index := func(byEntity map[string]map[string][]Sample, bucket Bucket) {
		tasks := byEntity[bucket.EntityID]
		if tasks == nil {
			tasks = make(map[string][]Sample)
			byEntity[bucket.EntityID] = tasks
			if !seen[bucket.EntityID] {
				seen[bucket.EntityID] = true
				entityOrder = append(entityOrder, bucket.EntityID)
			}
		}
		tasks[taskIDOf(bucket.Tags)] = append(tasks[taskIDOf(bucket.Tags)], bucket.Samples...)
	}
	for _, bucket := range buckets {
		index(byEntity, bucket)
	}
	for _, bucket := range latencyBuckets {
		index(latencyByEntity, bucket)
	}

	window := req.End.Sub(req.Start)
	for _, entityID := range entityOrder {
		tasks := byEntity[entityID]
		// The node's presence grid is the union of its series' buckets: a node with two
		// tasks reported in a bucket if *either* task has a result there. Built here
		// rather than read as a third series — the same query already says when the node
		// was heard from, and a separate resource-metric query would cost a read for no
		// more information.
		presence := unionPresence(tasks, req.Start, req.End, req.Interval)
		report.Nodes = append(report.Nodes,
			BuildNodeReport(entityID, presence, tasks, latencyByEntity[entityID], window))
	}
	return report, nil
}

// unionPresence merges every task's samples into one grid for the node.
//
// A bucket is "reported" when at least one series carried a measurement there, and a
// total loss (count present, loss 1) still counts: the node was up, the target was
// not, and conflating the two is the misreading this whole report is built to avoid.
// The value carried is the worst loss seen in that bucket across tasks, which is what
// reportingGaps reads for incident grouping — but the grid is what presence uses.
func unionPresence(tasks map[string][]Sample, start, end time.Time, interval time.Duration) []Sample {
	type slot struct {
		count int
		worst float64
	}
	slots := make(map[int64]*slot)
	var latest time.Time
	for _, samples := range tasks {
		for _, sample := range samples {
			key := sample.Bucket.Unix()
			entry := slots[key]
			if entry == nil {
				entry = &slot{}
				slots[key] = entry
			}
			if sample.Count > 0 {
				entry.count += sample.Count
				if sample.Value > entry.worst {
					entry.worst = sample.Value
				}
			}
			if sample.Bucket.After(latest) {
				latest = sample.Bucket
			}
		}
	}
	if len(slots) == 0 {
		return nil
	}

	out := make([]Sample, 0, len(slots))
	if interval <= 0 {
		interval = time.Minute
	}
	for at := start.UTC().Truncate(interval); !at.After(latest); at = at.Add(interval) {
		entry, ok := slots[at.Unix()]
		if !ok || entry.count == 0 {
			out = append(out, Sample{Bucket: at, Value: 0, Count: 0})
			continue
		}
		out = append(out, Sample{Bucket: at, Value: entry.worst, Count: entry.count})
	}
	return out
}

// taskIDOf reads the task id out of a series' tags.
//
// A series with no task_id tag is still reported, under an empty id, rather than
// dropped: it is data that exists, and silently discarding it would make the report
// disagree with the raw series for no stated reason.
func taskIDOf(tags map[string]string) string {
	if tags == nil {
		return ""
	}
	return tags["task_id"]
}

// SeriesQuery describes one series to read and how to bucket it.
type SeriesQuery struct {
	MetricName string
	EntityID   string
	Tags       map[string]string
	Start      time.Time
	End        time.Time
	// Interval is the bucket width. A caller picks it from the window so a 90-day
	// report does not ask for 130 000 minute buckets.
	Interval time.Duration
	// Aggregation is how raw points inside one bucket are combined. Loss wants the
	// mean (the fraction lost); latency wants the mean too, because the store already
	// kept the percentiles it computed over the raw points.
	Aggregation metric.Aggregation
}

// toSamples converts the store's buckets into this package's Sample.
//
// Count is carried through rather than inferred from the value: the store's
// fill_empty produces zero-valued points with Count == 0, and those are gaps, not
// measurements of zero. Treating them as data is how a silent node reads as a
// perfectly healthy one.
func toSamples(points []metric.AggregatePoint) []Sample {
	samples := make([]Sample, 0, len(points))
	for _, point := range points {
		samples = append(samples, Sample{
			Bucket: point.Bucket.UTC(),
			Value:  point.Value,
			Count:  point.Count,
		})
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Bucket.Before(samples[j].Bucket) })
	return samples
}

// ReadSeries fetches one series and converts it, so callers never see a metric type.
func ReadSeries(ctx context.Context, reader SeriesReader, query SeriesQuery, now time.Time) ([]Sample, error) {
	if reader == nil {
		return nil, fmt.Errorf("no metric store")
	}
	if query.Interval <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}
	aggregation := query.Aggregation
	if aggregation == "" {
		aggregation = metric.AggAvg
	}
	points, err := reader.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: query.MetricName,
			EntityID:   query.EntityID,
			Start:      query.Start,
			End:        query.End,
			Tags:       query.Tags,
		},
		Aggregation: aggregation,
		Interval:    query.Interval,
	}, now)
	if err != nil {
		return nil, err
	}
	return toSamples(points), nil
}

// TaskReport is one ping task's availability on one node.
type TaskReport struct {
	TaskID   string            `json:"task_id"`
	Tags     map[string]string `json:"tags,omitempty"`
	Loss     Availability      `json:"loss"`
	Latency  Latency           `json:"latency"`
	Outages  []Incident        `json:"outages"`
	Interval time.Duration     `json:"-"`
}

// Latency is the latency summary across the buckets of a window.
//
// The percentiles are across *buckets*, and the doc comment on Percentiles says why:
// the store's own p50/p95/p99 describe the probes inside one bucket, while a report is
// asked "what was a normal minute, and what was a bad one".
type Latency struct {
	P50     float64 `json:"p50_ms"`
	P95     float64 `json:"p95_ms"`
	P99     float64 `json:"p99_ms"`
	Buckets int     `json:"buckets"`
	HasData bool    `json:"has_data"`
}

// NodeReport is one node's report over a window.
type NodeReport struct {
	EntityID string `json:"entity_id"`
	// Presence is how much of the window the node reported in. It is not called
	// availability, and PresenceFromSamples explains why that distinction is kept.
	Presence Presence `json:"presence"`
	// Tasks is the per-task availability, which *is* measured (from loss).
	Tasks []TaskReport `json:"tasks"`
	// Gaps are the interruptions in the node's own reporting, as incidents.
	// They are excluded from any availability figure by design, and reported here so
	// a reader can see them rather than have them silently dropped.
	Gaps      []Incident `json:"reporting_gaps"`
	HasReport bool       `json:"has_report"`
}

// Report is the whole answer to one SLA request.
type Report struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Window string    `json:"window"`
	// IntervalSeconds is the bucket width the figures were computed from, so a reader
	// can tell a 1-minute report from an hourly one.
	IntervalSeconds float64      `json:"interval_seconds"`
	Clamped         string       `json:"clamped,omitempty"`
	Nodes           []NodeReport `json:"nodes"`
}

// lossThreshold is the bucket loss at or above which a bucket counts as part of an
// outage. Half a bucket: a partial outage must be visible, and one lost probe in a
// bucket of many must not be.
const lossThreshold = 0.5

// BuildNodeReport computes one node's report from series already read.
//
// Kept separate from the reading so the arithmetic can be tested directly, which is
// the whole reason the pure functions in availability.go exist.
func BuildNodeReport(entityID string, presenceSamples []Sample, taskSamples, latencySamples map[string][]Sample, window time.Duration) NodeReport {
	report := NodeReport{
		EntityID: entityID,
		Presence: PresenceFromSamples(presenceSamples, window),
		Tasks:    []TaskReport{},
	}
	report.Gaps = reportingGaps(presenceSamples)

	taskIDs := make([]string, 0, len(taskSamples))
	for taskID := range taskSamples {
		taskIDs = append(taskIDs, taskID)
	}
	// Deterministic order: a report that reshuffles between requests is a report
	// nobody can diff.
	sort.Strings(taskIDs)

	for _, taskID := range taskIDs {
		samples := taskSamples[taskID]
		task := TaskReport{
			TaskID:  taskID,
			Loss:    AvailabilityFromLoss(samples, window),
			Latency: LatencyFromSamples(latencySamples[taskID]),
			Outages: IncidentsFromLoss(samples, lossThreshold),
		}
		report.Tasks = append(report.Tasks, task)
	}
	report.HasReport = report.Presence.ObservedBuckets > 0
	for _, task := range report.Tasks {
		if task.Loss.HasData {
			report.HasReport = true
			break
		}
	}
	return report
}

// reportingGaps turns the empty buckets of a presence series into incidents, so a
// reader sees "the node stopped reporting from 03:00 to 05:00" rather than only a
// coverage percentage.
//
// It reuses the incident grouping by mapping "no data" to a failing value, which keeps
// one definition of what a contiguous run is.
func reportingGaps(samples []Sample) []Incident {
	mapped := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.Count == 0 {
			mapped = append(mapped, Sample{Bucket: sample.Bucket, Value: 1, Count: 1})
			continue
		}
		mapped = append(mapped, Sample{Bucket: sample.Bucket, Value: 0, Count: 1})
	}
	return IncidentsFromLoss(mapped, lossThreshold)
}

// LatencyFromSamples computes the bucket-level latency percentiles for a task.
//
// Buckets whose value is negative are excluded: the agent writes -1 for a bucket in
// which every probe failed, and averaging that in would report an outage as a very
// fast ping. Buckets with no data are excluded for the same reason — see Percentiles.
func LatencyFromSamples(samples []Sample) Latency {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample.Count == 0 || sample.Value < 0 {
			continue
		}
		values = append(values, sample.Value)
	}
	p50, p95, p99, ok := Percentiles(values)
	return Latency{P50: p50, P95: p95, P99: p99, Buckets: len(values), HasData: ok}
}
