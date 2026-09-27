package forecast

import (
	"testing"
	"time"
)

const gib = 1024 * 1024 * 1024

var cycleStart = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
var cycleEnd = cycleStart.Add(30 * 24 * time.Hour)

// steady builds samples at a fixed rate: `perStep` bytes added every `step`.
//
// Synthetic because the acceptance criteria ask for a known total to compare against, and a real
// node's traffic is not a number anyone knows in advance.
func steady(perStep int64, step time.Duration, count int) []Sample {
	samples := make([]Sample, 0, count)
	for index := 0; index < count; index++ {
		samples = append(samples, Sample{
			At:    cycleStart.Add(time.Duration(index) * step),
			Total: int64(index) * perStep,
		})
	}
	return samples
}

// A cycle with under a day of data reports "not enough data", not a projection. The criterion this
// feature is safest with: a projection is what an operator acts on, and a wrong one either hides a
// suspension or buys headroom that was never needed.
func TestACycleWithUnderADayOfDataIsNotProjected(t *testing.T) {
	// Twelve hours in, with plenty of samples: the refusal must be about the elapsed fraction, not
	// about how many observations there are.
	now := cycleStart.Add(12 * time.Hour)
	samples := steady(gib, time.Hour, 13)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 500 * gib,
	})

	if result.Method != MethodInsufficient {
		t.Fatalf("Method = %q with 12h of a 30d cycle, want %q", result.Method, MethodInsufficient)
	}
	if result.ProjectedBytes != 0 {
		t.Fatalf("ProjectedBytes = %d on an insufficient forecast, want 0", result.ProjectedBytes)
	}
	if result.Reason == "" {
		t.Fatal("an insufficient forecast must say what was missing")
	}
	// The observed total is still reported: "how much have I used" is answerable even when "where
	// will it end up" is not.
	if result.UsedBytes != 12*gib {
		t.Fatalf("UsedBytes = %d, want %d", result.UsedBytes, 12*gib)
	}
	if result.Warning != nil {
		t.Fatal("an insufficient forecast must not warn: there is nothing to warn about")
	}
}

// The same node a day later is projected, and lands within the stated tolerance of the known total.
func TestASteadyNodeProjectsWithinTheStatedTolerance(t *testing.T) {
	// A node transferring exactly 10 GiB a day, observed for 10 days.
	const perDay = 10 * gib
	step := 6 * time.Hour
	perStep := int64(perDay) / 4
	samples := steady(perStep, step, 41) // 10 days at 6h
	now := cycleStart.Add(10 * 24 * time.Hour)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 1000 * gib,
	})

	if result.Method != MethodLinearRate {
		t.Fatalf("Method = %q (%s), want %q", result.Method, result.Reason, MethodLinearRate)
	}
	// The known answer: 10 GiB a day for 30 days.
	want := int64(300) * gib
	if result.ProjectedBytes != want {
		t.Fatalf("ProjectedBytes = %d, want %d", result.ProjectedBytes, want)
	}
	// A perfectly steady node has no scatter, so the uncertainty is zero and the tolerance is tight.
	if result.Basis.Uncertainty > 0.01 {
		t.Fatalf("Uncertainty = %.4f on a perfectly steady series, want ~0", result.Basis.Uncertainty)
	}
	if result.Basis.Samples != 41 {
		t.Fatalf("Samples = %d, want 41", result.Basis.Samples)
	}
	if result.Basis.BytesPerDay < float64(perDay)*0.99 || result.Basis.BytesPerDay > float64(perDay)*1.01 {
		t.Fatalf("BytesPerDay = %.0f, want ~%d", result.Basis.BytesPerDay, perDay)
	}
	// And the projection states the method, so a reader is not asked to trust it.
	if result.Method != MethodLinearRate {
		t.Fatal("the forecast must name its method")
	}
	if result.Basis.Coverage < 0.32 || result.Basis.Coverage > 0.34 {
		t.Fatalf("Coverage = %.3f, want ~1/3", result.Basis.Coverage)
	}
}

// The limit-crossing date is reported with the basis: rate, window and samples used.
func TestTheCrossingDateIsReportedWithItsBasis(t *testing.T) {
	// 10 GiB a day, observed for 10 days, so 100 GiB used. Against a 150 GiB limit the remaining
	// 50 GiB is five days away — a crossing in the future, which is what the date is for. (A limit
	// equal to what has already been used is the degenerate case, and it is the next test.)
	const perDay = 10 * gib
	samples := steady(int64(perDay)/4, 6*time.Hour, 41)
	now := cycleStart.Add(10 * 24 * time.Hour)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 150 * gib,
	})

	if !result.LimitSet {
		t.Fatal("LimitSet must be true when the caller supplied a limit")
	}
	if !result.WouldExceed {
		t.Fatal("a node using 10 GiB a day against a 150 GiB limit must be projected to exceed it")
	}
	if !result.CrossesInCycle {
		t.Fatal("the crossing falls inside the cycle and must be reported as such")
	}
	if result.CrossesInDays < 4.9 || result.CrossesInDays > 5.1 {
		t.Fatalf("CrossesInDays = %.2f, want ~5", result.CrossesInDays)
	}
	// The basis is complete enough for a reader to redo the arithmetic.
	if result.Basis.BytesPerDay <= 0 {
		t.Fatal("the basis must state the rate")
	}
	if result.Basis.Samples == 0 {
		t.Fatal("the basis must state how many samples it used")
	}
	if result.Basis.Coverage <= 0 || result.Basis.Coverage > 1 {
		t.Fatalf("Coverage = %.3f, want a fraction of the cycle", result.Basis.Coverage)
	}
	if result.Basis.CycleDays < 29.9 || result.Basis.CycleDays > 30.1 {
		t.Fatalf("CycleDays = %.1f, want 30", result.Basis.CycleDays)
	}
}

// A node that has already used exactly its allowance is the marginal case, and it must not be the
// silent one. This is the case that found an off-by-one in the crossing calculation: `>` instead of
// `>=` left this node with no crossing date, no warning and nothing to look at, which is the one
// situation where the plan is exhausted to the byte.
func TestANodeThatHasExactlyUsedItsAllowanceIsReported(t *testing.T) {
	const perDay = 10 * gib
	samples := steady(int64(perDay)/4, 6*time.Hour, 41) // 100 GiB used
	now := cycleStart.Add(10 * 24 * time.Hour)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 100 * gib,
	})

	if !result.LimitSet {
		t.Fatal("LimitSet must be true")
	}
	if result.CrossesAt.IsZero() {
		t.Fatal("a node exactly at its limit must still get a crossing date, or the exhausted plan is the silent case")
	}
	if !result.CrossesInCycle {
		t.Fatal("the crossing is now, which is inside the cycle")
	}
	if result.Warning == nil {
		t.Fatal("a node at 107% of its limit projected must warn")
	}
}

// A crossing after the cycle ends is not a crossing this cycle, and saying so is the difference
// between a warning and noise.
func TestACrossingAfterTheCycleIsNotInThisCycle(t *testing.T) {
	// 1 GiB a day against a 100 GiB limit: day 100, well past the 30-day cycle.
	samples := steady(gib/4, 6*time.Hour, 41)
	now := cycleStart.Add(10 * 24 * time.Hour)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 100 * gib,
	})

	if !result.LimitSet {
		t.Fatal("LimitSet must be true")
	}
	if result.WouldExceed {
		t.Fatalf("projecting %d against a %d limit must not exceed it",
			result.ProjectedBytes, int64(100*gib))
	}
	if result.CrossesInCycle {
		t.Fatal("a crossing on day 100 is not in a 30-day cycle")
	}
	if result.CrossesAt.IsZero() {
		t.Fatal("the date is still worth reporting, flagged as outside the cycle")
	}
	if result.Warning != nil {
		t.Fatal("a node that will not reach its limit this cycle must not be warned about")
	}
}

// A node with no plan limit is not forecast against infinity: it is projected, and says there is
// nothing to project against.
func TestANodeWithNoLimitIsNotForecastAgainstInfinity(t *testing.T) {
	samples := steady(gib/4, 6*time.Hour, 41)
	now := cycleStart.Add(10 * 24 * time.Hour)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 0,
	})

	if result.LimitSet {
		t.Fatal("LimitSet must be false when no limit was supplied")
	}
	if result.Method != MethodLinearRate {
		t.Fatalf("Method = %q (%s), want a projection: the total is still useful", result.Method, result.Reason)
	}
	if result.ProjectedBytes <= 0 {
		t.Fatal("the projected total must still be reported")
	}
	// And nothing that depends on a limit is invented.
	if result.ProjectedFraction != 0 {
		t.Fatalf("ProjectedFraction = %v with no limit, want 0", result.ProjectedFraction)
	}
	if result.WouldExceed {
		t.Fatal("there is no limit to exceed")
	}
	if !result.CrossesAt.IsZero() {
		t.Fatal("there is no limit to cross")
	}
	if result.Warning != nil {
		t.Fatal("there is no limit to warn about")
	}
}

// The warning fires when the projection crosses the threshold, and carries a sentence with the
// numbers in it.
func TestTheWarningCrossesAtTheThreshold(t *testing.T) {
	// 10 GiB a day, 30 days observed ... against a limit the projection nearly fills.
	const perDay = 10 * gib
	samples := steady(int64(perDay)/4, 6*time.Hour, 41)
	now := cycleStart.Add(10 * 24 * time.Hour)

	// Projected 300 GiB. Against a 300 GiB limit that is exactly at it.
	atLimit := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 300 * gib,
	})
	if atLimit.Warning == nil {
		t.Fatal("a projection at 100% of the limit must warn at the default 90% threshold")
	}
	if atLimit.Warning.Threshold != DefaultThreshold {
		t.Fatalf("Threshold = %v, want the default %v", atLimit.Warning.Threshold, DefaultThreshold)
	}
	if atLimit.Warning.Message == "" {
		t.Fatal("the warning must carry a sentence")
	}

	// Well under: no warning, because a warning at 50% would train an operator to ignore them.
	under := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 1000 * gib,
	})
	if under.Warning != nil {
		t.Fatalf("a projection at %.0f%% of the limit must not warn at %v",
			under.ProjectedFraction*100, DefaultThreshold)
	}

	// A caller's own threshold is honoured, and the warning says which one it used.
	custom := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 400 * gib,
		Threshold: 0.7,
	})
	if custom.Warning == nil {
		t.Fatalf("a projection at %.0f%% must warn at a 70%% threshold", custom.ProjectedFraction*100)
	}
	if custom.Warning.Threshold != 0.7 {
		t.Fatalf("Threshold = %v, want the caller's 0.7", custom.Warning.Threshold)
	}
}

// Two points define a line, and that line is whatever the last two minutes were. A handful of
// observations is refused.
func TestTooFewObservationsIsRefused(t *testing.T) {
	// Twenty days elapsed, so coverage is not the reason; four samples is.
	now := cycleStart.Add(20 * 24 * time.Hour)
	samples := steady(gib, 24*time.Hour, 4)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 500 * gib,
	})
	if result.Method != MethodInsufficient {
		t.Fatalf("Method = %q with 4 samples, want %q", result.Method, MethodInsufficient)
	}
	if result.Reason == "" {
		t.Fatal("the refusal must say how many observations there were")
	}

	// Five is enough.
	enough := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: steady(gib, 24*time.Hour, 5),
		LimitBytes: 500 * gib,
	})
	if enough.Method != MethodLinearRate {
		t.Fatalf("Method = %q with 5 samples (%s), want a projection", enough.Method, enough.Reason)
	}
}

// A counter that goes down means the ledger rotated or the node was reinstalled; there is no rate to
// project from, and a negative one would produce a negative total.
func TestACounterResetIsRefused(t *testing.T) {
	now := cycleStart.Add(10 * 24 * time.Hour)
	samples := []Sample{
		{At: cycleStart.Add(1 * time.Hour), Total: 50 * gib},
		{At: cycleStart.Add(2 * time.Hour), Total: 60 * gib},
		{At: cycleStart.Add(3 * time.Hour), Total: 70 * gib},
		{At: cycleStart.Add(4 * time.Hour), Total: 80 * gib},
		{At: cycleStart.Add(5 * time.Hour), Total: 2 * gib}, // reset
	}

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 500 * gib,
	})
	if result.Method != MethodInsufficient {
		t.Fatalf("Method = %q after a counter reset, want %q", result.Method, MethodInsufficient)
	}
	if result.ProjectedBytes != 0 {
		t.Fatalf("ProjectedBytes = %d, want 0", result.ProjectedBytes)
	}
}

// Samples from before the cycle are ignored, because the ledger rotates and a caller reading the
// table will hand them over.
func TestSamplesOutsideTheCycleAreIgnored(t *testing.T) {
	now := cycleStart.Add(10 * 24 * time.Hour)
	samples := append([]Sample{
		{At: cycleStart.Add(-24 * time.Hour), Total: 900 * gib}, // last cycle
		{At: cycleStart.Add(-12 * time.Hour), Total: 950 * gib},
	}, steady(10*gib, 24*time.Hour, 11)...)

	result := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 5000 * gib,
	})
	if result.Basis.Samples != 11 {
		t.Fatalf("Samples = %d, want 11: the two from before the cycle must be dropped",
			result.Basis.Samples)
	}
	// Not 950 GiB, which is what including the pre-cycle rows would have reported.
	if result.UsedBytes != 100*gib {
		t.Fatalf("UsedBytes = %d, want %d", result.UsedBytes, int64(100*gib))
	}
}

// A cycle with no length is not a cycle to project across.
func TestADegenerateCycleIsRefused(t *testing.T) {
	result := Compute(cycleStart, Request{
		CycleStart: cycleStart, CycleEnd: cycleStart, Samples: steady(gib, time.Hour, 10),
		LimitBytes: 100 * gib,
	})
	if result.Method != MethodInsufficient {
		t.Fatalf("Method = %q for a zero-length cycle, want %q", result.Method, MethodInsufficient)
	}
}

// A bursty node's projection carries a larger uncertainty than a steady one's, which is the whole
// point of reporting one.
func TestABurstyNodeReportsMoreUncertainty(t *testing.T) {
	now := cycleStart.Add(10 * 24 * time.Hour)

	steadySamples := steady(gib/4, 6*time.Hour, 41)
	steadyResult := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: steadySamples, LimitBytes: 500 * gib,
	})

	// The same total over the same span, but unevenly: a quiet stretch then a busy one.
	bursty := make([]Sample, 0, 41)
	total := int64(0)
	for index := 0; index < 41; index++ {
		step := int64(gib / 64)
		if index > 30 {
			step = gib // a burst near the end
		}
		total += step
		bursty = append(bursty, Sample{At: cycleStart.Add(time.Duration(index) * 6 * time.Hour), Total: total})
	}
	burstyResult := Compute(now, Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: bursty, LimitBytes: 500 * gib,
	})

	if burstyResult.Method != MethodLinearRate {
		t.Fatalf("the bursty series was refused: %s", burstyResult.Reason)
	}
	if burstyResult.Basis.Uncertainty <= steadyResult.Basis.Uncertainty {
		t.Fatalf("uncertainty %.4f for the bursty series is not larger than %.4f for the steady one",
			burstyResult.Basis.Uncertainty, steadyResult.Basis.Uncertainty)
	}
}

// The forecast is a value, not a mutation: computing it twice from the same input gives the same
// answer, so a page and a notifier cannot disagree.
func TestTheForecastIsDeterministic(t *testing.T) {
	samples := steady(gib/4, 6*time.Hour, 41)
	now := cycleStart.Add(10 * 24 * time.Hour)
	request := Request{
		CycleStart: cycleStart, CycleEnd: cycleEnd, Samples: samples, LimitBytes: 500 * gib,
	}

	first := Compute(now, request)
	second := Compute(now, request)
	if first.ProjectedBytes != second.ProjectedBytes ||
		first.CrossesInDays != second.CrossesInDays ||
		first.Basis.Uncertainty != second.Basis.Uncertainty {
		t.Fatalf("two computations of the same input differ:\n%+v\n%+v", first, second)
	}
	// And the request's own slice was not reordered, so a caller can reuse it.
	if samples[0].Total != 0 {
		t.Fatal("Compute reordered or modified the caller's samples")
	}
}
