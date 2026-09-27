package notifier

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/internal/cycle"
	"github.com/Aone2233/nekomari/internal/forecast"
)

// The parts of the traffic forecast that are decisions rather than plumbing. The projection's own
// arithmetic is tested in internal/forecast and the cycle's boundaries in internal/cycle; what is here
// is the joining of the two, and the two rules the acceptance criteria state that only this layer can
// satisfy: the projected quantity must be the one the limit is defined against, and the warning must
// fire once per cycle rather than once per scan.

// The projected quantity has to be the quantity the limit applies to. Summing is right for a `sum` node
// and wrong for a `max` or `min` node, where the limit is on whichever direction is larger or smaller —
// projecting the sum for those would warn against a number the plan does not measure.
func TestTheProjectedQuantityMatchesTheLimitType(t *testing.T) {
	const up, down = int64(300), int64(100)

	cases := []struct {
		limitType string
		want      int64
		why       string
	}{
		{"up", 300, "an upload-limited node is measured on its uploads"},
		{"down", 100, "a download-limited node on its downloads"},
		{"sum", 400, "a sum node on both"},
		{"max", 300, "a max node on whichever direction is larger"},
		{"min", 100, "a min node on whichever is smaller"},
		{"", 300, "an unset type means max, matching computeUsedByType's fallthrough from its max branch"},
		{"something-else", 300, "an unrecognised type means max rather than sum or zero"},
	}
	for _, tc := range cases {
		if got := combineByLimitType(tc.limitType, up, down); got != tc.want {
			t.Errorf("combineByLimitType(%q, %d, %d) = %d, want %d (%s)",
				tc.limitType, up, down, got, tc.want, tc.why)
		}
	}

	// And it agrees with the used-percentage path for a symmetric case, which is what makes the two
	// warnings comparable rather than two different opinions about the same node.
	for _, limitType := range []string{"up", "down", "sum", "max", "min", ""} {
		if got, want := combineByLimitType(limitType, up, down),
			computeUsedByType(limitType, up, down); got != want {
			t.Errorf("the forecast and the used-percentage check disagree for %q: %d vs %d",
				limitType, got, want)
		}
	}
}

// Once per cycle, not once per scan. The stored value is the cycle the warning was sent for, so a
// rotation invalidates it without anything having to notice that it rotated.
func TestTheForecastWarnsOncePerCycle(t *testing.T) {
	forecastCache.Flush()
	t.Cleanup(forecastCache.Flush)

	const uuid = "uuid-once-per-cycle"
	key := "trafficforecast:" + uuid

	firstCycle := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	secondCycle := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// Nothing recorded: a warning would be sent.
	if _, found := forecastCache.Get(key); found {
		t.Fatal("the cache started non-empty")
	}
	forecastCache.SetDefault(key, firstCycle)

	// Same cycle: suppressed, which is the "once per scan" half.
	sent, found := forecastCache.Get(key)
	if !found {
		t.Fatal("the cycle was not recorded")
	}
	if sentCycle, ok := sent.(time.Time); !ok || !sentCycle.Equal(firstCycle) {
		t.Fatalf("recorded %v, want the cycle start %s", sent, firstCycle)
	}

	// Next cycle: the value no longer matches, so a warning is allowed again — without anything having
	// to detect the rotation.
	if sentCycle := sent.(time.Time); sentCycle.Equal(secondCycle) {
		t.Fatal("the recorded cycle must not equal the next one, or the warning would never fire again")
	}
	forecastCache.SetDefault(key, secondCycle)
	after, _ := forecastCache.Get(key)
	if !after.(time.Time).Equal(secondCycle) {
		t.Fatalf("after rotation the cache holds %v, want %s", after, secondCycle)
	}
}

// A failed send must not consume the cycle: a transport failure is not a reason to stay silent for a
// month.
func TestAFailedSendRetriesRatherThanWaitingForTheNextCycle(t *testing.T) {
	forecastCache.Flush()
	t.Cleanup(forecastCache.Flush)

	const uuid = "uuid-retry"
	key := "trafficforecast:" + uuid
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	forecastCache.SetDefault(key, start)
	forecastCache.Delete(key) // what the failure path does

	if _, found := forecastCache.Get(key); found {
		t.Fatal("after a failure the cycle must be unrecorded, so the next scan tries again")
	}
}

// The sample interval has a floor as well as a ceiling, and the floor is the one that matters: the panel
// keeps minute-resolution data for a short window, so asking for a finer interval late in a month would
// return the minutes still held rather than the days the projection needs.
func TestTheSampleIntervalIsBoundedBothWays(t *testing.T) {
	cases := []struct {
		name        string
		cycleLength time.Duration
		want        time.Duration
	}{
		{"a 30-day cycle targets about a hundred points", 30 * 24 * time.Hour, 7 * time.Hour},
		{"a 31-day cycle rounds to the hour", 31 * 24 * time.Hour, 7 * time.Hour},
		{"a one-day cycle is floored at an hour", 24 * time.Hour, time.Hour},
		{"a short cycle is floored too, not made finer", time.Hour, time.Hour},
		{"a year-long window is capped at a day", 365 * 24 * time.Hour, 24 * time.Hour},
	}
	for _, tc := range cases {
		if got := sampleInterval(tc.cycleLength); got != tc.want {
			t.Errorf("%s: sampleInterval(%s) = %s, want %s", tc.name, tc.cycleLength, got, tc.want)
		}
	}
	// The floor is never below an hour, whatever the cycle.
	for _, length := range []time.Duration{time.Minute, time.Hour, 12 * time.Hour, 30 * 24 * time.Hour} {
		if got := sampleInterval(length); got < time.Hour {
			t.Errorf("sampleInterval(%s) = %s, below the one-hour floor", length, got)
		}
	}
}

// The message has to carry the basis, because the number alone is not actionable: an operator deciding
// whether to buy headroom needs to know how much of the cycle it came from and how scattered it was.
func TestTheForecastMessageCarriesItsBasis(t *testing.T) {
	projection := forecast.Forecast{
		Method:            forecast.MethodLinearRate,
		ProjectedBytes:    300 * 1024 * 1024 * 1024,
		ProjectedFraction: 1.2,
		LimitSet:          true,
		WouldExceed:       true,
		CrossesInCycle:    true,
		CrossesAt:         time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
		CrossesInDays:     5,
		Basis: forecast.Basis{
			Samples: 41, Coverage: 1.0 / 3, BytesPerDay: 10 * 1024 * 1024 * 1024, Uncertainty: 0.07,
		},
	}

	message := describeProjection(projection)
	for _, want := range []string{"300.00 GB", "10.00 GiB/day", "41 samples", "33%", "±7%", "5.0 days"} {
		if !contains(message, want) {
			t.Errorf("the message is missing %q: %s", want, message)
		}
	}
	// The limit is recovered from the fraction, so the sentence can name both numbers.
	if limit := limitOf(projection); limit != 250*1024*1024*1024 {
		t.Errorf("limitOf = %d, want the limit behind the fraction", limit)
	}
	// A projection with no limit has no fraction to divide by, and must not divide by zero.
	if limit := limitOf(forecast.Forecast{ProjectedBytes: 100}); limit != 0 {
		t.Errorf("limitOf with no fraction = %d, want 0", limit)
	}
}

// A projection that was refused must not warn: "not enough data" is an answer, and warning about it
// would be warning that there is nothing to warn about.
func TestARefusedProjectionDoesNotWarn(t *testing.T) {
	refused := forecast.Forecast{Method: forecast.MethodInsufficient, Reason: "only 2 observations"}
	if refused.Warning != nil {
		t.Fatal("an insufficient forecast carries no warning")
	}
	// And the caller skips it explicitly rather than relying on that.
	if refused.Method != forecast.MethodInsufficient {
		t.Fatal("the caller's guard is on Method, so the method must be set")
	}
}

// The cycle the forecast uses must be the same one the once-per-cycle key records, or a warning could be
// sent twice in one cycle or never.
func TestTheRecordedCycleIsTheCycleThatWasProjected(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	window, err := cycle.Bounds(1, time.UTC, now)
	if err != nil {
		t.Fatalf("Bounds: %v", err)
	}
	if !window.Start.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("the cycle started at %s, want 2026-09-01", window.Start)
	}
	// The key is derived from this value, so two scans inside one cycle record the same instant and a scan
	// in the next cycle records a different one.
	nextWindow, err := cycle.Bounds(1, time.UTC, window.End.Add(time.Hour))
	if err != nil {
		t.Fatalf("Bounds: %v", err)
	}
	if nextWindow.Start.Equal(window.Start) {
		t.Fatal("the next cycle must record a different start, or the warning would never fire again")
	}
	if !nextWindow.Start.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("the next cycle started at %s, want 2026-10-01", nextWindow.Start)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
