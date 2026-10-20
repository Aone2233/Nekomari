package sla

import (
	"sort"
	"time"
)

// This file is the maintenance-window half of the report: time that must not count as
// evidence. A window means "this node was supposed to be unavailable", so a lost probe,
// a slow bucket or a reporting gap inside one is not an outage — it is the thing the
// window was scheduled for.
//
// The rule that shapes everything here: **excluded time is removed from both sides of
// the ratio.** Dropping the losses alone would report 100% for a reboot; dropping only
// the denominator would leave the failures in the numerator while claiming they never
// happened. So an excluded bucket leaves the observed count, the expected count, the
// availability fraction, the latency percentiles and the incident list together.
//
// A window that covers the whole report window therefore has no evidence left, and the
// report says so through HasData=false rather than by inventing 100%.

// Interval is a half-open time range [Start, End) excluded from a report. It is a
// whole maintenance window, or the part of one that falls inside the report window:
// callers clip at the boundary and Build merges overlaps (see normalizeIntervals).
type Interval struct {
	Start time.Time
	End   time.Time
}

// bucketIntersects reports whether the bucket [bucket, bucket+width) intersects one of
// the intervals.
//
// Half-open on both sides: a bucket that starts exactly at the end of a window is
// evidence (the window is over), and a bucket that starts exactly at the start of one
// is excluded (it is the first bucket of the window). A width of zero or less means
// the caller has no bucket width, and the bucket is then treated as the single instant
// it starts at — the conservative choice, since it can only exclude less.
func bucketIntersects(bucket time.Time, width time.Duration, intervals []Interval) bool {
	bucket = bucket.UTC()
	for _, interval := range intervals {
		start, end := interval.Start.UTC(), interval.End.UTC()
		if !start.Before(end) {
			continue // an empty interval excludes nothing
		}
		if width <= 0 {
			if !bucket.Before(start) && bucket.Before(end) {
				return true
			}
			continue
		}
		if bucket.Before(end) && bucket.Add(width).After(start) {
			return true
		}
	}
	return false
}

// normalizeIntervals clips intervals to [start, end), drops the empty ones, and merges
// overlaps and touching ranges.
//
// Clipping is what makes an exclusion describe the report rather than the whole
// history: the same window covers several requests, and only the part inside the
// requested window may subtract from that window's coverage. Merging is what stops two
// overlapping operator windows from counting the shared time twice, both in the
// denominator and in the maintenance_excluded_seconds figure.
func normalizeIntervals(start, end time.Time, intervals []Interval) []Interval {
	start, end = start.UTC(), end.UTC()
	clipped := make([]Interval, 0, len(intervals))
	for _, interval := range intervals {
		windowStart, windowEnd := interval.Start.UTC(), interval.End.UTC()
		if windowStart.Before(start) {
			windowStart = start
		}
		if windowEnd.After(end) {
			windowEnd = end
		}
		if !windowStart.Before(windowEnd) {
			continue
		}
		clipped = append(clipped, Interval{Start: windowStart, End: windowEnd})
	}
	if len(clipped) == 0 {
		return nil
	}
	sort.Slice(clipped, func(i, j int) bool { return clipped[i].Start.Before(clipped[j].Start) })
	merged := make([]Interval, 0, len(clipped))
	for _, interval := range clipped {
		last := len(merged) - 1
		if last >= 0 && !interval.Start.After(merged[last].End) {
			if interval.End.After(merged[last].End) {
				merged[last].End = interval.End
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}

// excludedSeconds is how much of the report window the (already normalized) intervals
// cover, in whole seconds. Zero means "no maintenance in this window", which is what
// lets the field be omitted from the wire response.
func excludedSeconds(intervals []Interval) int64 {
	var total time.Duration
	for _, interval := range intervals {
		if interval.End.After(interval.Start) {
			total += interval.End.Sub(interval.Start)
		}
	}
	if total <= 0 {
		return 0
	}
	return int64(total / time.Second)
}

// presenceWithExclusions applies the windows to a Presence figure.
//
// The cadence is still inferred from the whole grid, empty and excluded buckets
// included: an excluded stretch is evidence about the schedule, not about the
// reporting cadence, and inferring the cadence from the remaining buckets would read a
// window as a longer reporting interval. Only the expected and observed counts (and
// the first/last data bounds) are adjusted afterwards, so a node that reported normally
// up to a reboot and normally after it keeps its real cadence and loses exactly the
// window from both sides of the ratio.
func presenceWithExclusions(samples []Sample, window time.Duration, exclusions []Interval, width time.Duration) Presence {
	presence := computePresence(samples, window)
	if len(exclusions) == 0 {
		return presence
	}
	deduped := dedupe(samples)
	step := cadence(deduped)
	observedAt := make(map[int64]bool, len(deduped))
	for _, sample := range deduped {
		if sample.Count > 0 {
			observedAt[sample.Bucket.Unix()] = true
		}
	}

	excludedExpected, excludedObserved := 0, 0
	switch {
	case !presence.FirstData.IsZero() && step > 0:
		for slot := presence.FirstData; !slot.After(presence.LastData); slot = slot.Add(step) {
			if !bucketIntersects(slot, width, exclusions) {
				continue
			}
			excludedExpected++
			if observedAt[slot.Unix()] {
				excludedObserved++
			}
		}
	case !presence.FirstData.IsZero() && bucketIntersects(presence.FirstData, width, exclusions):
		// A single bucket has no cadence to walk; that one slot is the whole grid.
		excludedExpected, excludedObserved = 1, presence.ObservedBuckets
	}

	expected := presence.ExpectedBuckets - excludedExpected
	if expected < 0 {
		expected = 0
	}
	observed := presence.ObservedBuckets - excludedObserved
	if observed < 0 {
		observed = 0
	}
	first, last := time.Time{}, time.Time{}
	for _, sample := range deduped {
		if sample.Count == 0 || bucketIntersects(sample.Bucket, width, exclusions) {
			continue
		}
		if first.IsZero() {
			first = sample.Bucket
		}
		last = sample.Bucket
	}
	presence.FirstData, presence.LastData = first, last
	if expected == 0 && observed == 0 {
		// Everything the window had was maintenance: no evidence, not a perfect score.
		return Presence{}
	}
	if observed > expected {
		// Same guard computePresence applies: a denominator below the counted evidence
		// would report >100% coverage for a series whose cadence was misread.
		expected = observed
	}
	presence.ExpectedBuckets = expected
	presence.ObservedBuckets = observed
	presence.Coverage = float64(observed) / float64(expected)
	if presence.Coverage > 1 {
		presence.Coverage = 1
	}
	return presence
}

// PresenceFromSamplesExcluding is PresenceFromSamples with maintenance windows removed
// from both the observed and expected counts.
//
// With no exclusions it is exactly PresenceFromSamples, which is the regression
// guarantee: a deployment that never schedules a window sees the same numbers it saw
// before this existed.
func PresenceFromSamplesExcluding(samples []Sample, window time.Duration, exclusions []Interval, width time.Duration) Presence {
	if len(exclusions) == 0 {
		return PresenceFromSamples(samples, window)
	}
	return presenceWithExclusions(samples, window, exclusions, width)
}

// AvailabilityFromLossExcluding is AvailabilityFromLoss with maintenance buckets
// removed. A loss bucket that intersects a window does not count as availability
// evidence at all: it is neither a success nor a failure, and it leaves the coverage
// denominator with the expected slots.
func AvailabilityFromLossExcluding(samples []Sample, window time.Duration, exclusions []Interval, width time.Duration) Availability {
	if len(exclusions) == 0 {
		return AvailabilityFromLoss(samples, window)
	}
	result := Availability{
		Window:   window,
		Presence: presenceWithExclusions(samples, window, exclusions, width),
	}
	total := 0.0
	for _, sample := range dedupe(samples) {
		if sample.Count == 0 || bucketIntersects(sample.Bucket, width, exclusions) {
			continue
		}
		loss := sample.Value
		if loss < 0 {
			loss = 0
		}
		if loss > 1 {
			loss = 1
		}
		total += 1 - loss
		result.Buckets++
		if loss >= 0.5 {
			result.Lost++
		}
	}
	if result.Buckets == 0 {
		return result // HasData stays false: a fully excluded window is unknown, not 100%
	}
	result.HasData = true
	result.Fraction = total / float64(result.Buckets)
	return result
}

// IncidentsFromLossExcluding is IncidentsFromLoss with maintenance buckets removed and
// treated as run terminators.
//
// A window is not a gap either: it is a period whose evidence is deliberately absent.
// Two failures on either side of a reboot are two incidents, not one outage that spans
// the reboot — merging them would report the maintenance itself as downtime, which is
// the figure this whole file exists to keep out.
func IncidentsFromLossExcluding(samples []Sample, threshold float64, exclusions []Interval, width time.Duration) []Incident {
	if len(exclusions) == 0 {
		return IncidentsFromLoss(samples, threshold)
	}
	if threshold <= 0 {
		threshold = 0.5
	}
	var incidents []Incident
	var current *Incident
	flush := func() {
		if current == nil {
			return
		}
		current.Duration = current.End.Sub(current.Start)
		incidents = append(incidents, *current)
		current = nil
	}
	for _, sample := range samples {
		if bucketIntersects(sample.Bucket, width, exclusions) {
			flush()
			continue
		}
		if sample.Count == 0 {
			flush()
			continue
		}
		if sample.Value >= threshold {
			if current == nil {
				current = &Incident{Start: sample.Bucket, End: sample.Bucket, PeakLoss: sample.Value}
			}
			current.End = sample.Bucket
			current.Buckets++
			if sample.Value > current.PeakLoss {
				current.PeakLoss = sample.Value
			}
			continue
		}
		flush()
	}
	flush()
	return incidents
}

// LatencyFromSamplesExcluding is LatencyFromSamples with maintenance buckets removed.
//
// A slow bucket inside a window is not a latency observation — an upgrade is allowed to
// be slow — and averaging it in would blame the target for the operator's reboot.
func LatencyFromSamplesExcluding(samples []Sample, exclusions []Interval, width time.Duration) Latency {
	if len(exclusions) == 0 {
		return LatencyFromSamples(samples)
	}
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if sample.Count == 0 || sample.Value < 0 || bucketIntersects(sample.Bucket, width, exclusions) {
			continue
		}
		values = append(values, sample.Value)
	}
	p50, p95, p99, ok := Percentiles(values)
	return Latency{P50: p50, P95: p95, P99: p99, Buckets: len(values), HasData: ok}
}

// reportingGapsExcluding is reportingGaps with maintenance windows treated as neither a
// gap nor a report.
//
// This is the node-level half of "a reboot during a window is not an outage": the gaps
// list exists to show when the panel stopped hearing from a node, and a scheduled
// reboot is the one time it is expected to stop.
func reportingGapsExcluding(samples []Sample, exclusions []Interval, width time.Duration) []Incident {
	if len(exclusions) == 0 {
		return reportingGaps(samples)
	}
	mapped := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.Count == 0 {
			mapped = append(mapped, Sample{Bucket: sample.Bucket, Value: 1, Count: 1})
			continue
		}
		mapped = append(mapped, Sample{Bucket: sample.Bucket, Value: 0, Count: 1})
	}
	return IncidentsFromLossExcluding(mapped, lossThreshold, exclusions, width)
}

// BuildNodeReportExcluding is BuildNodeReport with maintenance windows excluded.
//
// With no exclusions it delegates to BuildNodeReport unchanged; a report that never
// mentions a window therefore has the identical definition it had before.
func BuildNodeReportExcluding(entityID string, presenceSamples []Sample, taskSamples, latencySamples map[string][]Sample, window, interval time.Duration, exclusions []Interval) NodeReport {
	if len(exclusions) == 0 {
		return BuildNodeReport(entityID, presenceSamples, taskSamples, latencySamples, window)
	}
	report := NodeReport{
		EntityID:                   entityID,
		Presence:                   PresenceFromSamplesExcluding(presenceSamples, window, exclusions, interval),
		Tasks:                      []TaskReport{},
		MaintenanceExcludedSeconds: excludedSeconds(exclusions),
	}
	report.Gaps = reportingGapsExcluding(presenceSamples, exclusions, interval)

	taskIDs := make([]string, 0, len(taskSamples))
	for taskID := range taskSamples {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)

	for _, taskID := range taskIDs {
		samples := taskSamples[taskID]
		report.Tasks = append(report.Tasks, TaskReport{
			TaskID:  taskID,
			Loss:    AvailabilityFromLossExcluding(samples, window, exclusions, interval),
			Latency: LatencyFromSamplesExcluding(latencySamples[taskID], exclusions, interval),
			Outages: IncidentsFromLossExcluding(samples, lossThreshold, exclusions, interval),
		})
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
