package sla

import (
	"math"
	"testing"
	"time"
)

// base is a fixed instant so every expected timestamp in these tests is readable.
var base = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// at returns the bucket `n` minutes after base.
func at(n int) time.Time { return base.Add(time.Duration(n) * time.Minute) }

// series builds samples at one-minute intervals, with count 1 unless the entry is a
// gap (a negative value marks a gap, which is how the store's fill_empty produces it).
func series(values ...float64) []Sample {
	return slots(values)
}

// slots builds a series from explicit per-minute slots starting at base: a value is a
// measurement, and `gap` is a minute in which nothing was reported.
//
// Explicit rather than offset arithmetic. An earlier version of these tests built a
// gap by concatenating two offset series, and the arithmetic was wrong in a way that
// made the test assert the wrong minute — the failure looked like a bug in the code
// under test. A test whose intent cannot be read off the line is a test that cannot
// be trusted when it fails.
func slots(values []float64) []Sample {
	samples := make([]Sample, 0, len(values))
	for i, value := range values {
		if value == gap {
			samples = append(samples, Sample{Bucket: at(i), Value: 0, Count: 0})
			continue
		}
		samples = append(samples, Sample{Bucket: at(i), Value: value, Count: 1})
	}
	return samples
}

// gap marks a minute with no reported data.
const gap = -1

// atOffset is series() compressed into one call: a value is a measurement, gap is a
// missing minute, and the whole slice starts at minute 0.
func atOffset(values ...float64) []Sample { return slots(values) }

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// The acceptance criterion that matters most: a window with no data must not be
// reported as 0% and must not be reported as 100%.
func TestNoDataIsNeitherZeroNorFull(t *testing.T) {
	window := 10 * time.Minute

	result := AvailabilityFromLoss(nil, window)
	if result.HasData {
		t.Fatal("no samples must not produce a figure")
	}
	if result.Fraction != 0 {
		t.Fatalf("Fraction = %v, want 0 for an unset figure", result.Fraction)
	}
	if result.Presence.Coverage != 0 {
		t.Fatalf("Coverage = %v, want 0", result.Presence.Coverage)
	}

	// A series of gaps is the same answer as no series at all.
	gaps := AvailabilityFromLoss(slots([]float64{gap, gap, gap}), window)
	if gaps.HasData {
		t.Fatal("a series of empty buckets must not produce a figure either")
	}
	if gaps.Buckets != 0 {
		t.Fatalf("Buckets = %d, want 0", gaps.Buckets)
	}
}

// A quiet window is 100%, and that is a real figure rather than a missing one, so
// HasData has to distinguish them.
func TestFullAvailabilityIsReportedAsSuch(t *testing.T) {
	result := AvailabilityFromLoss(series(0, 0, 0, 0), 4*time.Minute)
	if !result.HasData {
		t.Fatal("four measured buckets are data")
	}
	if !almostEqual(result.Fraction, 1) {
		t.Fatalf("Fraction = %v, want 1", result.Fraction)
	}
	if result.Lost != 0 {
		t.Fatalf("Lost = %d, want 0", result.Lost)
	}
}

// Partial loss averages per bucket, not per probe.
func TestPartialLossAveragesPerBucket(t *testing.T) {
	// Half the buckets fully lost, half clean = 50%.
	result := AvailabilityFromLoss(series(1, 0, 1, 0), 4*time.Minute)
	if !almostEqual(result.Fraction, 0.5) {
		t.Fatalf("Fraction = %v, want 0.5", result.Fraction)
	}
	if result.Lost != 2 {
		t.Fatalf("Lost = %d, want 2", result.Lost)
	}

	// And a bucket that is a quarter lost contributes 0.75, so a partial outage is
	// visible rather than rounded to "up".
	quarter := AvailabilityFromLoss(series(0.25), time.Minute)
	if !almostEqual(quarter.Fraction, 0.75) {
		t.Fatalf("Fraction = %v, want 0.75", quarter.Fraction)
	}
}

// A gap must lower coverage without changing availability: the buckets that were
// measured were all fine.
func TestAGapLowersCoverageNotAvailability(t *testing.T) {
	// Ten minutes, the third and fourth missing, the other eight clean.
	samples := slots([]float64{0, 0, gap, gap, 0, 0, 0, 0, 0, 0})

	result := AvailabilityFromLoss(samples, 10*time.Minute)
	if !almostEqual(result.Fraction, 1) {
		t.Fatalf("Fraction = %v, want 1: every measured bucket was clean", result.Fraction)
	}
	if result.Buckets != 8 {
		t.Fatalf("Buckets = %d, want 8", result.Buckets)
	}
	// Eight of the ten minutes the series spans, which is what the data can be
	// checked against.
	if !almostEqual(result.Presence.Coverage, 0.8) {
		t.Fatalf("Coverage = %v, want 0.8", result.Presence.Coverage)
	}
	// And the two missing minutes are reported as one gap, not silently dropped.
	gaps := IncidentsFromLoss(slots([]float64{0, 0, 1, 1, 0, 0, 0, 0, 0, 0}), 0.5)
	if len(gaps) != 1 || gaps[0].Buckets != 2 {
		t.Fatalf("gaps = %+v, want one two-bucket run", gaps)
	}
}

// One long gap must not be read as a change of cadence, or a node that was down for
// hours would have its own outage reinterpreted as a slower reporting interval.
func TestOneLongGapDoesNotChangeTheCadence(t *testing.T) {
	samples := []Sample{
		{Bucket: at(0), Value: 0, Count: 1},
		{Bucket: at(1), Value: 0, Count: 1},
		{Bucket: at(2), Value: 0, Count: 1},
		// Six hours of silence.
		{Bucket: at(2 + 360), Value: 0, Count: 1},
		{Bucket: at(2 + 361), Value: 0, Count: 1},
		{Bucket: at(2 + 362), Value: 0, Count: 1},
	}
	if got := cadence(samples); got != time.Minute {
		t.Fatalf("cadence = %s, want 1m", got)
	}
}

// Fewer than two buckets carries no cadence evidence, so coverage must not invent one.
func TestASingleBucketClaimsNoCadence(t *testing.T) {
	presence := computePresence(series(0), time.Hour)
	if presence.ExpectedBuckets != 1 || !almostEqual(presence.Coverage, 1) {
		t.Fatalf("presence = %+v, want one expected bucket at full coverage", presence)
	}
}

// Percentiles come from the values, and a gap must be excluded before calling this:
// the test asserts the exclusion is the caller's job by showing what including a zero
// would do.
func TestPercentiles(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p50, p95, p99, ok := Percentiles(values)
	if !ok {
		t.Fatal("expected a result")
	}
	if !almostEqual(p50, 5.5) {
		t.Fatalf("p50 = %v, want 5.5", p50)
	}
	if !almostEqual(p95, 9.55) {
		t.Fatalf("p95 = %v, want 9.55", p95)
	}
	if !almostEqual(p99, 9.91) {
		t.Fatalf("p99 = %v, want 9.91", p99)
	}

	if _, _, _, ok := Percentiles(nil); ok {
		t.Fatal("no values must not produce percentiles")
	}
	// NaN and infinities are dropped rather than poisoning the result.
	single, _, _, ok := Percentiles([]float64{math.NaN(), 5, math.Inf(1)})
	if !ok || !almostEqual(single, 5) {
		t.Fatalf("p50 = %v (ok=%v), want 5", single, ok)
	}
}

// An incident is a contiguous run, its duration is measured to the last failing
// bucket, and its peak is the worst bucket in it.
func TestIncidentsFromLoss(t *testing.T) {
	incidents := IncidentsFromLoss(series(0, 1, 1, 1, 0, 0, 1, 0), 0.5)
	if len(incidents) != 2 {
		t.Fatalf("got %d incidents, want 2: %+v", len(incidents), incidents)
	}
	first := incidents[0]
	if !first.Start.Equal(at(1)) || !first.End.Equal(at(3)) {
		t.Fatalf("first incident = %s..%s, want %s..%s", first.Start, first.End, at(1), at(3))
	}
	if first.Buckets != 3 {
		t.Fatalf("first incident buckets = %d, want 3", first.Buckets)
	}
	// Three buckets from 1 to 3 minutes is two minutes of wall time.
	if first.Duration != 2*time.Minute {
		t.Fatalf("first incident duration = %s, want 2m", first.Duration)
	}
	if incidents[1].Buckets != 1 {
		t.Fatalf("second incident buckets = %d, want 1", incidents[1].Buckets)
	}
}

// A gap ends a run instead of joining the failures on either side of it: nobody
// measured the gap, so an outage spanning it would be invented.
func TestAGapEndsAnIncidentRatherThanJoiningIt(t *testing.T) {
	samples := slots([]float64{1, 1, gap, gap, 1, 1})

	incidents := IncidentsFromLoss(samples, 0.5)
	if len(incidents) != 2 {
		t.Fatalf("got %d incidents, want 2: %+v", len(incidents), incidents)
	}
	if !incidents[0].End.Equal(at(1)) {
		t.Fatalf("first incident ends at %s, want %s", incidents[0].End, at(1))
	}
	if !incidents[1].Start.Equal(at(4)) {
		t.Fatalf("second incident starts at %s, want %s", incidents[1].Start, at(4))
	}
}
// A single lost probe is not an incident at the default threshold; a total loss is.
// This is the "one blip counted as an outage" failure the plan calls out.
func TestOneLostProbeIsNotAnOutage(t *testing.T) {
	// One probe in a bucket is a loss of 1/n; at the default threshold of half a
	// bucket, a bucket reporting 0.1 is below it.
	if got := IncidentsFromLoss(series(0.1, 0.1, 0.1), 0.5); len(got) != 0 {
		t.Fatalf("got %d incidents, want 0: %+v", len(got), got)
	}
	if got := IncidentsFromLoss(series(1), 0.5); len(got) != 1 {
		t.Fatalf("a total loss must be an incident, got %d", len(got))
	}
	// A threshold of zero would make every bucket an incident, so it is replaced
	// rather than honoured.
	if got := IncidentsFromLoss(series(0, 0), 0); len(got) != 0 {
		t.Fatalf("a zero threshold must fall back to the default, got %d incidents", len(got))
	}
}

// The synthetic-outage reconstruction the plan asks for: a known-length outage comes
// back to within one bucket of the source resolution.
func TestAKnownOutageIsReconstructed(t *testing.T) {
	const outageBuckets = 7
	values := make([]float64, 0, 20)
	for i := 0; i < 6; i++ {
		values = append(values, 0) // healthy before
	}
	for i := 0; i < outageBuckets; i++ {
		values = append(values, 1)
	}
	for i := 0; i < 5; i++ {
		values = append(values, 0) // healthy after
	}

	result := AvailabilityFromLoss(series(values...), time.Duration(len(values))*time.Minute)
	incidents := IncidentsFromLoss(series(values...), 0.5)
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want 1", len(incidents))
	}
	// The run starts at the first failing bucket and ends at the last.
	wantStart := at(6)
	wantEnd := at(6 + outageBuckets - 1)
	if !incidents[0].Start.Equal(wantStart) || !incidents[0].End.Equal(wantEnd) {
		t.Fatalf("incident = %s..%s, want %s..%s",
			incidents[0].Start, incidents[0].End, wantStart, wantEnd)
	}
	if incidents[0].Buckets != outageBuckets {
		t.Fatalf("buckets = %d, want %d", incidents[0].Buckets, outageBuckets)
	}
	// Availability is the healthy share of the measured window.
	want := float64(len(values)-outageBuckets) / float64(len(values))
	if !almostEqual(result.Fraction, want) {
		t.Fatalf("Fraction = %v, want %v", result.Fraction, want)
	}
}

// Asking for more than is retained reports the window it used and why, rather than
// silently answering a shorter question.
func TestWindowStartClampsToRetention(t *testing.T) {
	now := base
	start, reason := WindowStart(now, 90*24*time.Hour, 30*24*time.Hour)
	if reason == "" {
		t.Fatal("clamping must say why")
	}
	if !start.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("start = %s, want the retention boundary", start)
	}
	if _, reason := WindowStart(now, 7*24*time.Hour, 30*24*time.Hour); reason != "" {
		t.Fatalf("no clamp expected, got %q", reason)
	}
	if _, reason := WindowStart(now, 0, time.Hour); reason == "" {
		t.Fatal("a window of zero must be reported as no window")
	}
}

// A window shorter than the inferred cadence still expects one bucket rather than zero,
// which would divide by zero in the coverage.
func TestCoverageNeverDividesByZero(t *testing.T) {
	presence := computePresence(series(0, 0), time.Second)
	if presence.ExpectedBuckets < 1 {
		t.Fatalf("ExpectedBuckets = %d, want at least 1", presence.ExpectedBuckets)
	}
	if presence.Coverage > 1 || presence.Coverage <= 0 {
		t.Fatalf("Coverage = %v, want within (0,1]", presence.Coverage)
	}
}

// The same shape as the live page's seven-day report: hourly buckets, no gaps, and the
// counts must agree.
//
// Honest about what this pins. It would **not** have caught the live page's
// "100% (163/1 samples)": for 163 contiguous hourly buckets the calculation already yields
// 163, and removing the `observed <= expected` guard afterwards leaves this test green.
// The guard in `computePresence` is therefore a defensive invariant — it makes a
// self-contradictory pair of numbers impossible to render — and **not** the explanation
// for what the page showed. That cause is still open; see the H1 note in
// `docs/ROADMAP.md`.
//
// What this test does pin is the ordinary case, which had no coverage at all before: a
// full window reports full coverage with counts that match. That is worth having on its
// own, because the reader-facing invariant is exactly "these two numbers agree".
func TestEveryBucketReportedIsFullCoverage(t *testing.T) {
	samples := make([]Sample, 0, 163)
	for i := 0; i < 163; i++ {
		samples = append(samples, Sample{
			Bucket: base.Add(time.Duration(i) * time.Hour),
			Value:  0,
			Count:  3,
		})
	}

	result := AvailabilityFromLoss(samples, 7*24*time.Hour)
	if result.Presence.ObservedBuckets != 163 {
		t.Fatalf("ObservedBuckets = %d, want 163", result.Presence.ObservedBuckets)
	}
	if result.Presence.ExpectedBuckets != 163 {
		t.Fatalf("ExpectedBuckets = %d, want 163", result.Presence.ExpectedBuckets)
	}
	if !almostEqual(result.Presence.Coverage, 1) {
		t.Fatalf("Coverage = %v, want 1", result.Presence.Coverage)
	}
}

// The invariant behind the case above, asserted across shapes rather than one example:
// whatever the input, the two counts stay ordered and coverage stays within [0,1]. A
// reader cannot check a report's arithmetic, so it has to hold by construction.
func TestObservedNeverExceedsExpected(t *testing.T) {
	cases := [][]float64{
		{0, 0, 0},
		{0, gap, 0},
		{gap, gap, gap, 0},
		{0},
		{1, 1, 1, 1, 1},
		{0, gap, gap, gap, 0},
	}
	for index, values := range cases {
		result := AvailabilityFromLoss(slots(values), time.Duration(len(values))*time.Minute)
		presence := result.Presence
		if presence.ObservedBuckets > presence.ExpectedBuckets {
			t.Errorf("case %d: observed %d > expected %d (inputs %v)",
				index, presence.ObservedBuckets, presence.ExpectedBuckets, values)
		}
		if presence.Coverage < 0 || presence.Coverage > 1 {
			t.Errorf("case %d: coverage %v outside [0,1]", index, presence.Coverage)
		}
	}
}
