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
func BuildNodeReport(entityID string, presenceSamples []Sample, taskSamples map[string][]Sample, window time.Duration) NodeReport {
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
