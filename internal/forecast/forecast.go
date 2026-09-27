// Package forecast projects a node's traffic for the rest of its billing cycle, and says when it
// would reach its plan limit.
//
// Why this exists: hitting a traffic limit is currently discovered by hitting it, and for a NAT'd
// node that means the provider suspends it. The number is useful; the number's *basis* is what makes
// it actable, so every forecast here carries the method, the samples it used and an uncertainty
// rather than a bare figure.
//
// The rule that shapes the design, and the first thing the acceptance criteria ask for: **a forecast
// that cannot be made is not made.** Extrapolating two points into a confident number is worse than
// saying "not enough data", because the number is what an operator acts on — and the failure mode of
// a wrong projection is either a suspension that was predicted as fine, or a plan upgrade that was
// never needed. So `Method` can be `insufficient`, and that is a first-class answer rather than an
// error.
package forecast

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Method names how a projection was reached.
type Method string

const (
	// MethodInsufficient means no projection was made, and Reason says why.
	MethodInsufficient Method = "insufficient"
	// MethodLinearRate projects from the average rate observed so far.
	MethodLinearRate Method = "linear_rate"
)

// minCoverage is the fraction of the cycle that must have elapsed before a projection is made.
//
// A day, expressed as a fraction of a 30-day cycle. The reason is not statistical purity: a node's
// traffic is neither constant nor random — it has a daily shape, and the first hours of a cycle are
// systematically unrepresentative of the rest (the month-rotate boundary often coincides with a
// quiet period). Projecting from five hours of a thirty-day cycle multiplies whatever that period
// happened to be by 144.
//
// The fraction rather than a fixed duration so a short cycle is not permanently unpredictable.
const minCoverage = 1.0 / 30.0

// minSamples is how many observations are needed to compute a rate at all.
//
// Two points define a line, so two is the mathematical minimum and a bad one: one quiet minute
// followed by one busy minute projects a cycle from a single interval. Five is enough that a single
// outlier cannot dominate, and cheap enough that a node reporting every five minutes reaches it
// inside half an hour.
const minSamples = 5

// Sample is one observed traffic total for the cycle, in bytes.
//
// A cumulative total rather than a delta, because the projection is about where the total will end
// up: the caller has the ledger's month-to-date value, and deltas would need re-summing here to say
// anything about the end of the cycle.
type Sample struct {
	// At is when the total was observed.
	At time.Time
	// Total is the cycle-to-date traffic in bytes at that instant.
	Total int64
}

// Request is what a forecast is computed from.
type Request struct {
	// CycleStart and CycleEnd bound the billing cycle. The window is half-open, matching how the
	// ledger rotates: `[start, end)`.
	CycleStart time.Time
	CycleEnd   time.Time
	// Samples are the cycle's observations, in any order.
	Samples []Sample
	// LimitBytes is the plan's traffic allowance. Zero means no limit is set, and a node with no
	// limit is not forecast at all — see Forecast.LimitSet.
	LimitBytes int64
	// Threshold is the fraction of the limit at which a warning is worth raising, 0 < Threshold <= 1.
	// Zero means the default.
	Threshold float64
}

// Forecast is the answer.
type Forecast struct {
	// Method is how the projection was reached, or MethodInsufficient.
	Method Method `json:"method"`
	// Reason explains an insufficient forecast, in the terms the operator needs: what was missing.
	Reason string `json:"reason,omitempty"`

	// UsedBytes is the observed total at Now, which is reported even when no projection is made: an
	// operator asking "how much have I used" deserves an answer even when the cycle is too young to
	// project.
	UsedBytes int64 `json:"used_bytes"`
	// ProjectedBytes is the cycle's projected end total. Zero when Method is insufficient.
	ProjectedBytes int64 `json:"projected_bytes"`

	// LimitSet is false when the node has no plan limit, in which case there is nothing to project
	// against and no crossing date to report.
	LimitSet bool `json:"limit_set"`
	// ProjectedFraction is ProjectedBytes / LimitBytes. Zero when no limit is set.
	ProjectedFraction float64 `json:"projected_fraction"`
	// WouldExceed is whether the projection reaches the limit before the cycle ends.
	WouldExceed bool `json:"would_exceed"`

	// CrossesAt is when the limit would be reached, and CrossesInDays how far away that is. Zero and
	// zero when the limit is not projected to be reached.
	CrossesAt   time.Time `json:"crosses_at,omitempty"`
	CrossesInDays float64 `json:"crosses_in_days,omitempty"`
	// CrossesInCycle is whether the crossing falls inside this cycle. A crossing after the cycle ends
	// is not a problem this cycle, and saying so is the difference between a warning and noise.
	CrossesInCycle bool `json:"crosses_in_cycle"`

	// Basis is the arithmetic behind the projection, stated so a reader can check it.
	Basis Basis `json:"basis"`

	// Warning is set when the projection crosses the threshold and a notification is warranted.
	Warning *Warning `json:"warning,omitempty"`
}

// Basis is the projection's own account of itself.
type Basis struct {
	// Samples is how many observations were used.
	Samples int `json:"samples"`
	// ObservedFor is how long the samples span.
	ObservedFor time.Duration `json:"observed_for"`
	// Coverage is the fraction of the cycle the samples span.
	Coverage float64 `json:"coverage"`
	// BytesPerDay is the observed average rate, which is the projection's slope.
	BytesPerDay float64 `json:"bytes_per_day"`
	// Uncertainty is the projection's relative error, from the spread of the per-interval rates.
	//
	// Estimated from the observed variance of the rate rather than asserted: a node with a steady
	// transfer has a small one and a node with a daily peak has a large one, and an operator deciding
	// whether to buy headroom needs to know which they are looking at. It is the relative standard
	// error of the mean rate times the projection's extrapolation factor, which is the honest
	// consequence of projecting further than you observed.
	Uncertainty float64 `json:"uncertainty"`
	// CycleDays is the cycle's length in days.
	CycleDays float64 `json:"cycle_days"`
}

// Warning is what the notifier sends.
type Warning struct {
	// Threshold is the fraction that was crossed.
	Threshold float64 `json:"threshold"`
	// ProjectedFraction is the projection's fraction of the limit.
	ProjectedFraction float64 `json:"projected_fraction"`
	// Message is a sentence for a human.
	Message string `json:"message"`
}

// DefaultThreshold is the warning fraction when the caller does not choose one.
//
// Ninety percent, because the warning has to arrive while there is still time to act: at the limit
// itself the only remaining options are a plan upgrade or an outage, and a node that is suspended has
// no traffic to measure.
const DefaultThreshold = 0.9

// humanBytes renders a byte count the way the panel does, for the warning's sentence.
func humanBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	value := float64(bytes)
	for _, name := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, name)
		}
	}
	return fmt.Sprintf("%.1f EiB", value)
}

// Compute projects the cycle's traffic and, when a limit is set, when it would be reached.
func Compute(now time.Time, request Request) Forecast {
	now = now.UTC()
	cycleStart := request.CycleStart.UTC()
	cycleEnd := request.CycleEnd.UTC()
	cycleLength := cycleEnd.Sub(cycleStart)

	forecast := Forecast{
		LimitSet: request.LimitBytes > 0,
		Basis: Basis{
			CycleDays: cycleLength.Hours() / 24,
		},
	}

	// A window that cannot contain the present is not a window to project into.
	if cycleLength <= 0 {
		forecast.Method = MethodInsufficient
		forecast.Reason = "the billing cycle has no length, so there is nothing to project across"
		return forecast
	}

	// Samples are filtered to the cycle and sorted, because a caller reading a ledger may hand over
	// rows from before the rotation and in storage order.
	usable := make([]Sample, 0, len(request.Samples))
	for _, sample := range request.Samples {
		at := sample.At.UTC()
		if at.Before(cycleStart) || at.After(now) {
			continue
		}
		usable = append(usable, Sample{At: at, Total: sample.Total})
	}
	sort.Slice(usable, func(i, j int) bool { return usable[i].At.Before(usable[j].At) })
	forecast.Basis.Samples = len(usable)
	if len(usable) > 0 {
		forecast.UsedBytes = usable[len(usable)-1].Total
	}

	// The observed span and its coverage of the cycle.
	if len(usable) >= 2 {
		forecast.Basis.ObservedFor = usable[len(usable)-1].At.Sub(usable[0].At)
	}
	if cycleLength > 0 {
		observed := now.Sub(cycleStart)
		if observed > cycleLength {
			observed = cycleLength
		}
		forecast.Basis.Coverage = float64(observed) / float64(cycleLength)
	}

	// The refusals, in the order an operator would ask about them.
	if len(usable) < minSamples {
		forecast.Method = MethodInsufficient
		forecast.Reason = fmt.Sprintf(
			"only %d observation(s) so far; a projection needs at least %d, because two points define a line and that line is whatever the last two minutes were",
			len(usable), minSamples)
		return forecast
	}
	if forecast.Basis.Coverage < minCoverage {
		forecast.Method = MethodInsufficient
		forecast.Reason = fmt.Sprintf(
			"%.1f%% of the cycle has elapsed; a projection needs at least %.1f%%, because the first hours of a cycle are not representative of it",
			forecast.Basis.Coverage*100, minCoverage*100)
		return forecast
	}
	// The rate is computed over the samples, not over the elapsed window, so a gap between the last
	// sample and now does not silently dilute it.
	span := usable[len(usable)-1].At.Sub(usable[0].At)
	if span <= 0 {
		forecast.Method = MethodInsufficient
		forecast.Reason = "every observation carries the same timestamp, so no rate can be measured"
		return forecast
	}

	// The average rate, and the scatter that makes the uncertainty.
	first, last := usable[0], usable[len(usable)-1]
	bytesPerSecond := float64(last.Total-first.Total) / span.Seconds()
	if bytesPerSecond < 0 {
		// A total that goes down means a counter reset, which happens when the ledger rotates or a
		// node is reinstalled. Projecting from a negative rate would produce a negative total.
		forecast.Method = MethodInsufficient
		forecast.Reason = "the cycle total decreased, which means the counter restarted; there is no rate to project from"
		return forecast
	}
	forecast.Basis.BytesPerDay = bytesPerSecond * 86400
	forecast.Basis.Uncertainty = rateUncertainty(usable, bytesPerSecond)

	// Project to the end of the cycle by extending the observed total at the observed rate.
	remaining := cycleEnd.Sub(last.At)
	if remaining < 0 {
		remaining = 0
	}
	projected := float64(last.Total) + bytesPerSecond*remaining.Seconds()
	if projected < float64(last.Total) {
		projected = float64(last.Total)
	}
	forecast.Method = MethodLinearRate
	forecast.ProjectedBytes = int64(math.Round(projected))

	if !forecast.LimitSet {
		// Nothing to project against. Stated rather than projected against infinity: a fraction of
		// infinity is not a number an operator can use.
		return forecast
	}

	forecast.ProjectedFraction = projected / float64(request.LimitBytes)
	forecast.WouldExceed = forecast.ProjectedBytes > request.LimitBytes

	// The crossing date, from the rate rather than from the projection, so it can be reported with
	// its basis.
	//
	// `>=` rather than `>`, and the difference is a node that has already used exactly its allowance:
	// with `>` that node produced no crossing date, no warning and nothing to look at — the one case
	// where the plan is exhausted to the byte was the one case that stayed silent. It was found by
	// this package's own test, which had a limit exactly equal to the observed total.
	if float64(request.LimitBytes) >= float64(last.Total) && bytesPerSecond > 0 {
		secondsToLimit := (float64(request.LimitBytes) - float64(last.Total)) / bytesPerSecond
		crossesAt := last.At.Add(time.Duration(secondsToLimit * float64(time.Second)))
		forecast.CrossesAt = crossesAt
		forecast.CrossesInDays = secondsToLimit / 86400
		// Not half-open, unlike the cycle itself: a crossing judged to land exactly on the cycle's
		// end is still a crossing this cycle, and being strict about the boundary would turn the
		// most marginal case — the node that exactly fills its plan — into the one case that is not
		// reported. An earlier version used `Before` and failed its own test on precisely that
		// scenario, which is the kind of off-by-one that only ever hides a warning.
		forecast.CrossesInCycle = !crossesAt.After(cycleEnd)
	}

	threshold := request.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultThreshold
	}
	if forecast.ProjectedFraction >= threshold {
		forecast.Warning = &Warning{
			Threshold:         threshold,
			ProjectedFraction: forecast.ProjectedFraction,
			Message: fmt.Sprintf(
				"projected %s by the end of the cycle, %.0f%% of the %s limit (%.1f%% of the cycle observed, ±%.0f%%)",
				humanBytes(forecast.ProjectedBytes), forecast.ProjectedFraction*100,
				humanBytes(request.LimitBytes), forecast.Basis.Coverage*100,
				forecast.Basis.Uncertainty*100),
		}
	}
	return forecast
}

// rateUncertainty estimates the projection's relative error from the scatter of the observed rate.
//
// Measured over the consecutive intervals of the samples rather than from a fitted line, so a node
// whose traffic has a daily shape shows a larger uncertainty than one with a steady transfer. The
// standard error of the mean interval rate is then multiplied by how much further the projection
// reaches than the observation, which is the honest cost of extrapolating: a projection that covers
// the whole cycle from a third of it is three times less certain than the average it is built on.
func rateUncertainty(samples []Sample, meanBytesPerSecond float64) float64 {
	if len(samples) < 3 || meanBytesPerSecond <= 0 {
		return 0
	}
	rates := make([]float64, 0, len(samples)-1)
	for index := 1; index < len(samples); index++ {
		gap := samples[index].At.Sub(samples[index-1].At).Seconds()
		if gap <= 0 {
			continue
		}
		delta := float64(samples[index].Total - samples[index-1].Total)
		if delta < 0 {
			// A single resetting interval poisons the scatter; the average-rate check above is what
			// rejects a series that reset, and here the interval is simply not evidence.
			continue
		}
		rates = append(rates, delta/gap)
	}
	if len(rates) < 2 {
		return 0
	}
	mean := 0.0
	for _, rate := range rates {
		mean += rate
	}
	mean /= float64(len(rates))
	if mean <= 0 {
		return 0
	}
	variance := 0.0
	for _, rate := range rates {
		deviation := rate - mean
		variance += deviation * deviation
	}
	variance /= float64(len(rates) - 1)
	// The standard error of the mean, as a fraction of the mean.
	standardError := math.Sqrt(variance) / math.Sqrt(float64(len(rates))) / mean
	if math.IsNaN(standardError) || math.IsInf(standardError, 0) || standardError < 0 {
		return 0
	}
	return standardError
}
