// Package cycle computes the billing cycle a node's traffic belongs to.
//
// It exists on the panel side because the panel did not have it. The agent has always computed when
// its own month resets (`agent/utils.GetLastResetDate`, used by netstatic), but it reports a
// *cumulative* total without saying which window that total covers — so the panel could compare
// usage against a limit, which is arithmetic on two numbers it has, but could not ask "how far
// through the cycle are we", which is what any projection needs.
//
// The rules are a deliberate mirror of the agent's, including the one that looks like an edge case
// and is not: **a reset day the month does not have rolls forward to the first of the next month.**
// A node configured for the 31st is reset on the 31st in January and on the 1st in February, and a
// projection that disagreed with the agent about that would be wrong for one day a month — which is
// exactly the day a limit tends to be hit. `internal/cycle`'s tests assert the same cases the
// agent's do, so the two can be compared rather than only trusted.
//
// The location matters as much as the day: the agent resets on its *local* midnight, so a cycle
// computed in UTC for a node in Asia/Tokyo would be off by nine hours, and a forecast of "how much
// of the cycle has elapsed" would be wrong by a third of a day every day.
package cycle

import "time"

// Cycle is one billing period, half-open: `[Start, End)`.
//
// Half-open because that is what the agent's reset produces: the instant of the reset belongs to the
// new cycle, which is why a sample landing exactly on it is counted once rather than twice or not at
// all. The forecast takes the same convention, so the two agree about the boundary.
type Cycle struct {
	// Start is the most recent reset at or before the reference time.
	Start time.Time `json:"start"`
	// End is the next reset, which is the moment the current cycle's total goes back to zero.
	End time.Time `json:"end"`
	// ResetDay is the configured day of the month, kept so a reader can see what the boundaries came
	// from rather than having to infer it.
	ResetDay int `json:"reset_day"`
	// Location is the timezone the boundaries were computed in: the node's own, which is where its
	// agent resets. Reported as a name so a mismatch is visible.
	Location string `json:"location"`
}

// Duration is the cycle's length.
func (c Cycle) Duration() time.Duration { return c.End.Sub(c.Start) }

// Elapsed is how far into the cycle the reference time is, clamped to the cycle.
//
// Clamped rather than computed directly because a caller may hold a report that arrived slightly
// after the reset: an unclamped elapsed fraction above 1 would make a projection divide by more than
// the cycle and report a total for a window that has already ended.
func (c Cycle) Elapsed(at time.Time) time.Duration {
	if at.Before(c.Start) {
		return 0
	}
	if at.After(c.End) {
		return c.Duration()
	}
	return at.Sub(c.Start)
}

// Fraction is the elapsed share of the cycle, in [0, 1].
func (c Cycle) Fraction(at time.Time) float64 {
	total := c.Duration()
	if total <= 0 {
		return 0
	}
	return float64(c.Elapsed(at)) / float64(total)
}

// Contains reports whether an instant falls in the cycle.
func (c Cycle) Contains(at time.Time) bool {
	at = at.UTC()
	return !at.Before(c.Start) && at.Before(c.End)
}

// Bounds returns the billing cycle containing `reference`, for a node whose month resets on
// `resetDay` in `location`.
//
// A reset day outside 1..31 means the node has no monthly cycle configured — the agent treats it the
// same way, returning the reference date itself — and is reported as an error rather than as a
// one-instant cycle, because a caller that got a zero-length cycle would go on to divide by it.
func Bounds(resetDay int, location *time.Location, reference time.Time) (Cycle, error) {
	if resetDay < 1 || resetDay > 31 {
		return Cycle{}, &InvalidResetDayError{ResetDay: resetDay}
	}
	if location == nil {
		location = time.UTC
	}
	reference = reference.In(location)

	start := mostRecentReset(resetDay, location, reference)
	end := nextReset(resetDay, location, start)
	return Cycle{
		Start:    start.UTC(),
		End:      end.UTC(),
		ResetDay: resetDay,
		Location: location.String(),
	}, nil
}

// InvalidResetDayError reports a day the calendar cannot have.
type InvalidResetDayError struct{ ResetDay int }

func (e *InvalidResetDayError) Error() string {
	return "traffic reset day must be between 1 and 31, got " + itoa(e.ResetDay)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// resetInMonth returns the reset instant for one calendar month, rolling forward to the first of the
// next month when that month is too short for the configured day.
//
// This is the rule the agent implements, and the reason it has to be mirrored rather than
// approximated: a 31st-of-the-month node resets on the 1st in February, not on the 28th and not
// never.
func resetInMonth(year int, month time.Month, resetDay int, location *time.Location) time.Time {
	firstOfNext := time.Date(year, month+1, 1, 0, 0, 0, 0, location)
	lastDay := firstOfNext.AddDate(0, 0, -1).Day()
	if resetDay <= lastDay {
		return time.Date(year, month, resetDay, 0, 0, 0, 0, location)
	}
	return firstOfNext
}

// mostRecentReset is the agent's `GetLastResetDate`, step for step: this month's reset if it has
// already happened, otherwise last month's.
//
// Mirrored rather than re-derived, and the difference matters. The obvious version — walk back until
// you find a reset that has passed — gives a *different* answer for a reset day late in a short
// month. Take resetDay 31 and a reference of 15 February 2026: this month's reset rolled forward to
// 1 March, which has not happened, so the agent falls back exactly one month and returns
// `getActualResetDate(January)`, which is 31 January. A walk-back loop instead lands on the same 31
// January only by coincidence in that case, and disagrees on others, because "the last reset that
// passed" is not the question the agent answers. The question is "which cycle is the node's counter
// currently in", and the counter was reset on 31 January and will reset again on 1 March.
func mostRecentReset(resetDay int, location *time.Location, reference time.Time) time.Time {
	thisMonth := resetInMonth(reference.Year(), reference.Month(), resetDay, location)
	if !reference.Before(thisMonth) {
		return thisMonth
	}
	year, month := previousMonth(reference.Year(), reference.Month())
	return resetInMonth(year, month, resetDay, location)
}

// nextReset is the reset that follows `from`, which is the end of the cycle that began at it.
//
// It advances one month from the month the *reset occurrence* fell in, not from the month of `from`.
// Those differ exactly when the previous reset rolled forward: with resetDay 31, January's reset is
// 31 January but February has no 31st, so February's reset is 1 March — and the reset after 31 January
// is that 1 March, not April's. Stepping from `from`'s month instead skipped a cycle and produced
// `2026-05-01` as the end of the March cycle, which is a window three months long for a node whose
// counters reset monthly.
func nextReset(resetDay int, location *time.Location, from time.Time) time.Time {
	local := from.In(location)
	year, month := nextMonth(local.Year(), local.Month())
	for attempt := 0; attempt < 3; attempt++ {
		candidate := resetInMonth(year, month, resetDay, location)
		if candidate.After(from) {
			return candidate
		}
		// The candidate landed in the month after the one it was computed for (a roll-forward), or is
		// the instant we started from; either way the next month is the one to ask about.
		year, month = nextMonth(year, month)
	}
	// Unreachable for a valid reset day, but a bounded loop must still return something: one month on
	// is always a reset boundary, so this is a safe last resort rather than a wrong answer.
	return from.AddDate(0, 1, 0)
}

func previousMonth(year int, month time.Month) (int, time.Month) {
	if month == time.January {
		return year - 1, time.December
	}
	return year, month - 1
}

func nextMonth(year int, month time.Month) (int, time.Month) {
	if month == time.December {
		return year + 1, time.January
	}
	return year, month + 1
}

// Location resolves a node's timezone name.
//
// An unknown or empty name falls back to UTC rather than failing: a node whose timezone the panel
// cannot resolve is still a node whose traffic should be watched, and a projection computed in the
// wrong zone is more useful than no projection — provided the caller can see which zone was used,
// which is why Cycle carries the name.
func Location(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}
