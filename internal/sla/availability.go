// Package sla computes availability, latency percentiles and outage intervals from
// the metrics the panel already stores.
//
// Why this is its own package with no database in it: every number here has a
// definition that can be wrong in a way nobody notices. "Availability" over a window
// with no data at all, a gap in the series mistaken for downtime, a single one-minute
// blip reported as an incident — each of those is a decision, and a decision buried in
// a handler is a decision nobody can test. The definitions live in this file, they are
// stated in the doc comment of the function that implements them, and each has a test
// that fails if the definition changes silently.
//
// The rule that shapes all of them: **only a reported sample counts as evidence.**
// A bucket with data and a loss value is a measurement. A bucket with no data is
// *unknown*, and unknown is never reported as either 100% or 0% — it is reported as
// coverage, beside the availability figure, so a reader can see how much of the
// window the figure was computed from.
package sla

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Sample is one bucket of a series as the metric store returned it.
//
// Count is how many raw points the bucket aggregated. It matters: a bucket with
// Count == 0 is an *empty* bucket (the store's fill_empty produces these), which is
// not the same as a bucket whose value happens to be zero.
type Sample struct {
	Bucket time.Time
	Value  float64
	Count  int
}

// Presence describes how much of a window had data at all.
//
// Reported separately from Availability on purpose. A node that reported for half a
// window and was up for all of it is not "100% available and 50% covered" in the same
// sense as a node that reported throughout — and collapsing the two into one number
// is how a dead node reads as perfectly available.
type Presence struct {
	// ExpectedBuckets is how many buckets the window should hold at the observed
	// cadence, and ObservedBuckets is how many actually carried data.
	ExpectedBuckets int `json:"expected_buckets"`
	ObservedBuckets int `json:"observed_buckets"`
	// Coverage is ObservedBuckets/ExpectedBuckets. Zero when ExpectedBuckets is 0.
	Coverage float64 `json:"coverage"`
	// FirstData and LastData bound the buckets that carried data. Zero when none did.
	FirstData time.Time `json:"first_data"`
	LastData  time.Time `json:"last_data"`
}

// Availability is a fraction in [0, 1] with the evidence it was computed from.
type Availability struct {
	// Fraction is the availability of the *reported* buckets only. It is meaningless
	// when Buckets is 0, which is why HasData exists rather than leaving the reader to
	// infer it from a zero.
	Fraction float64 `json:"fraction"`
	HasData  bool    `json:"has_data"`
	// Buckets is how many buckets contributed, and Lost is how many of those were
	// failures.
	Buckets int `json:"buckets"`
	Lost    int `json:"lost"`
	// Presence says how much of the window those buckets cover.
	Presence Presence `json:"presence"`
	// Window is the requested window, so a caller can report "over this period".
	Window time.Duration `json:"window"`
}

// dedupe merges samples that share a bucket timestamp into one, and reports whether it
// changed anything.
//
// It has to happen before the cadence is measured, and the reason is a live bug (H1a): a
// task's series are split by their tags, and the tags have grown over time — one task_id
// carries `{task_id}`, then `{protocol, task_id}`, then `{family, protocol, task_id}` —
// over *overlapping* windows. One task therefore arrives with the same bucket repeated
// once per tag generation: on the live store, 327 of 489 adjacent gaps were zero.
//
// `cadence` takes the median of the gaps, so a majority of zeros made it 0, and the
// `step <= 0` branch reported "expected 1, observed 163, coverage 100%". A denominator of
// "unknown" rendered as "one", and 100% hid the gaps coverage exists to expose.
//
// Merging rather than dropping: two series disagreeing about the same bucket is a stronger
// signal, not a duplicate to discard, so the losses combine and the worst is kept — the
// same rule `unionPresence` uses across tasks.
func dedupe(samples []Sample) []Sample {
	if len(samples) < 2 {
		return samples
	}
	merged := make(map[int64]Sample, len(samples))
	order := make([]int64, 0, len(samples))
	for _, sample := range samples {
		key := sample.Bucket.Unix()
		existing, seen := merged[key]
		if !seen {
			merged[key] = sample
			order = append(order, key)
			continue
		}
		// A bucket is measured if any generation measured it.
		combined := existing
		combined.Count = existing.Count + sample.Count
		if sample.Value > combined.Value {
			combined.Value = sample.Value
		}
		merged[key] = combined
	}
	if len(merged) == len(samples) {
		return samples
	}
	out := make([]Sample, 0, len(merged))
	for _, key := range order {
		out = append(out, merged[key])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket.Before(out[j].Bucket) })
	return out
}

// cadence derives the reporting cadence from a series' bucket grid, including the
// buckets that carried no data.
//
// Including the empty ones is the whole point, and getting it wrong is subtle: filter
// them out first and a series whose third minute is missing looks like a series on a
// two-minute cadence, because the only gap between the *observed* points is two
// minutes. Coverage then comes out as 1 for a series that missed a third of its
// buckets. The grid is the evidence for the cadence; the values are the evidence for
// everything else.
//
// Median and not mean: one long gap (a node that was down for six hours) must not
// change the inferred cadence, and with a mean it would. Gaps are exactly the thing
// this is trying to measure, so the measure must not be skewed by them.
func cadence(samples []Sample) time.Duration {
	if len(samples) < 2 {
		return 0
	}
	gaps := make([]time.Duration, 0, len(samples)-1)
	for i := 1; i < len(samples); i++ {
		gap := samples[i].Bucket.Sub(samples[i-1].Bucket)
		if gap > 0 {
			gaps = append(gaps, gap)
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return gaps[len(gaps)/2]
}

// computePresence derives coverage by comparing the buckets that carried data with
// how many the window should hold at the observed cadence.
//
// When there are fewer than two buckets there is no cadence to infer, so coverage is
// reported as 1 if anything arrived and 0 if nothing did — a single bucket is all the
// evidence there is, and pretending to know the cadence from one point would be
// invention.
func computePresence(samples []Sample, window time.Duration) Presence {
	// One slot per distinct timestamp, before anything else: see dedupe. Both the
	// observed count and the cadence have to be computed from the deduplicated grid, or
	// the two disagree and the coverage reads as a figure nobody can check.
	samples = dedupe(samples)

	var presence Presence
	withData := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.Count > 0 {
			withData = append(withData, sample)
		}
	}
	presence.ObservedBuckets = len(withData)
	if len(withData) == 0 {
		return presence
	}
	presence.FirstData = withData[0].Bucket
	presence.LastData = withData[len(withData)-1].Bucket

	// The cadence comes from the whole grid, empty buckets included — see cadence().
	step := cadence(samples)
	if step <= 0 {
		presence.ExpectedBuckets = 1
		presence.Coverage = 1
		return presence
	}
	// Expected slots run from the first observation to the last at the observed
	// cadence, inclusive of both ends, and are then capped by the window itself.
	//
	// Not `window/step + 1`: that counts the window's endpoints as separate slots and
	// reports 11 expected buckets for ten minutes of one-minute data, which reads as
	// 91% coverage on a series that missed nothing. The span is what can be checked
	// against the data; the window is only an upper bound on it.
	span := presence.LastData.Sub(presence.FirstData)
	expected := int(span/step) + 1
	if window > 0 {
		if byWindow := int(window / step); byWindow < expected {
			expected = byWindow
		}
	}
	if expected < 1 {
		expected = 1
	}
	// Observed can never exceed expected: the expected count is derived from the same
	// window the observations came from. If it does, the cadence was misread — most
	// likely because the caller handed us a series whose empty buckets were *absent*
	// rather than zero-counted, so the only gap the cadence can see is the span itself.
	// Reporting "100% (163/1 samples)" is nonsense a reader would rightly distrust, so
	// the expectation is widened to fit the evidence.
	//
	// The order matters and was wrong once: this clamp sat *before* the calculation
	// above, so its value was overwritten by it and the guard did nothing at all. The
	// live page is what surfaced the symptoms.
	if presence.ObservedBuckets > expected {
		expected = presence.ObservedBuckets
	}
	presence.ExpectedBuckets = expected
	presence.Coverage = float64(presence.ObservedBuckets) / float64(expected)
	if presence.Coverage > 1 {
		presence.Coverage = 1
	}
	return presence
}

// AvailabilityFromLoss computes availability from `ping.loss` buckets, where each
// value is the fraction of probes in that bucket that were lost.
//
// The definition: a bucket's availability contribution is 1 - loss, weighted equally
// per bucket. Weighting per bucket rather than per probe is deliberate — the buckets
// are already equal-length windows, and weighting by probe count would let a target
// that was probed more often during an outage dominate its own record.
//
// Empty buckets are excluded from the fraction and counted in the coverage instead.
func AvailabilityFromLoss(samples []Sample, window time.Duration) Availability {
	withData := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.Count > 0 {
			withData = append(withData, sample)
		}
	}
	result := Availability{
		Window:   window,
		Presence: computePresence(samples, window),
	}
	if len(withData) == 0 {
		return result
	}

	total := 0.0
	for _, sample := range withData {
		loss := sample.Value
		if loss < 0 {
			loss = 0
		}
		if loss > 1 {
			loss = 1
		}
		total += 1 - loss
		if loss >= 0.5 {
			result.Lost++
		}
	}
	result.HasData = true
	result.Buckets = len(withData)
	result.Fraction = total / float64(len(withData))
	return result
}

// PresenceFromSamples computes node-level presence from a series the node reports
// continuously (its own resource metrics).
//
// There is deliberately no node-level "availability fraction" here, and that is the
// honest answer rather than a missing one: the panel does not persist an online/offline
// history — `resp.Online` is built from live WebSocket connections and nothing writes
// it down — so the only evidence a node was up in the past is that it reported. A
// fraction computed from that would be a fraction of *coverage* dressed up as
// availability, and a node that was hard down for a week would show 100%, because
// every bucket it did report was a bucket in which it was alive.
//
// So this returns coverage, and the node-level report says "reported N% of the window"
// rather than inventing a percentage of uptime. Per-task availability, where loss is
// actually measured, is AvailabilityFromLoss above.
func PresenceFromSamples(samples []Sample, window time.Duration) Presence {
	return computePresence(samples, window)
}

// Percentiles returns the p50/p95/p99 of the *bucket values* it is given, by linear
// interpolation between the nearest ranks.
//
// It takes values rather than samples because the store already computes percentiles
// over raw points within a bucket (pkg/metric's P50/P95/P99 on Stats); what a caller
// needs here is the percentile across buckets — "the typical bucket" and "the bad
// buckets" — which is a different question and the one a latency report is asked.
//
// Empty buckets must already be excluded by the caller: a missing measurement is not
// a fast one, and including it as zero would drag every percentile down.
func Percentiles(values []float64) (p50, p95, p99 float64, ok bool) {
	clean := make([]float64, 0, len(values))
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		clean = append(clean, value)
	}
	if len(clean) == 0 {
		return 0, 0, 0, false
	}
	sort.Float64s(clean)
	return interpolate(clean, 0.50), interpolate(clean, 0.95), interpolate(clean, 0.99), true
}

// interpolate returns the value at the given quantile of a sorted slice, linearly
// interpolating between the two ranks that bracket it.
func interpolate(sorted []float64, quantile float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := quantile * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower < 0 {
		lower = 0
	}
	if upper >= len(sorted) {
		upper = len(sorted) - 1
	}
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// Incident is a contiguous run of failing buckets.
type Incident struct {
	Start time.Time `json:"Start"`
	End   time.Time `json:"End"`
	// Buckets is how many consecutive failing buckets the run covers, and Duration is
	// End minus Start. Both are reported because they answer different questions: the
	// first is the resolution-limited evidence, the second is what a reader cares about.
	Buckets  int           `json:"buckets"`
	Duration time.Duration `json:"duration"`
	// PeakLoss is the worst bucket value in the run, in the same units as the source.
	PeakLoss float64 `json:"peak_loss"`
}

// IncidentsFromLoss groups buckets whose loss crosses threshold into contiguous runs.
//
// Two rules that the alternatives get wrong:
//
//   - **An empty bucket ends a run rather than extending it.** A gap means no evidence,
//     and merging a failure before a gap with a failure after it would invent an outage
//     that spans a period nobody measured. The two runs are reported separately, which
//     is what the data supports.
//   - **The threshold decides what is an incident at all.** One lost probe in a bucket
//     is not an outage; it is one lost probe. The default a caller should pass is
//     half the bucket, and any bucket at or above it counts, so a partial outage is
//     visible rather than only a total one.
func IncidentsFromLoss(samples []Sample, threshold float64) []Incident {
	if threshold <= 0 {
		threshold = 0.5
	}
	var incidents []Incident
	var current *Incident
	flush := func() {
		if current == nil {
			return
		}
		// End is the last failing bucket, not the bucket after it: the run is over
		// when the last failure was observed, and claiming the extra bucket would
		// overstate every outage by one bucket.
		current.Duration = current.End.Sub(current.Start)
		incidents = append(incidents, *current)
		current = nil
	}

	for _, sample := range samples {
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

// WindowStart clamps a requested window to what the store can answer, and returns the
// start time it settled on with the reason when it clamped.
//
// A report that silently answers a 30-day question with 7 days of data is worse than
// one that says it did: the coverage figure depends on the window being the window
// asked for.
func WindowStart(now time.Time, window, retention time.Duration) (time.Time, string) {
	if window <= 0 {
		return now, "no window requested"
	}
	if retention > 0 && window > retention {
		return now.Add(-retention), fmt.Sprintf(
			"requested %s but only %s is retained", window.Round(time.Hour), retention.Round(time.Hour))
	}
	return now.Add(-window), ""
}
