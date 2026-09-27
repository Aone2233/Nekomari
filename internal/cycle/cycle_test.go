package cycle

import (
	"testing"
	"time"
)

// The cases the agent's own GetLastResetDate is expected to answer, asserted here so the two
// implementations can be compared rather than only trusted. A disagreement is a forecast computed
// against a window the node is not in, which is wrong for one day a month — the day a limit tends to
// be hit.

func mustBounds(t *testing.T, resetDay int, location *time.Location, reference time.Time) Cycle {
	t.Helper()
	result, err := Bounds(resetDay, location, reference)
	if err != nil {
		t.Fatalf("Bounds(%d, %v): %v", resetDay, location, err)
	}
	return result
}

// The ordinary case: a reset day the month has.
func TestAMonthlyCycleStartsOnTheResetDay(t *testing.T) {
	utc := time.UTC
	// Mid-month, so the cycle began on the 15th of the same month.
	result := mustBounds(t, 15, utc, time.Date(2026, 9, 27, 12, 0, 0, 0, utc))

	if !result.Start.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, utc)) {
		t.Fatalf("Start = %s, want 2026-09-15T00:00:00Z", result.Start)
	}
	if !result.End.Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, utc)) {
		t.Fatalf("End = %s, want 2026-10-15T00:00:00Z", result.End)
	}
	if result.ResetDay != 15 {
		t.Fatalf("ResetDay = %d, want 15", result.ResetDay)
	}
	if result.Duration() != 30*24*time.Hour {
		t.Fatalf("Duration = %s, want 30 days", result.Duration())
	}
}

// Before the reset day, the cycle began last month. This is the case that makes a naive "same month,
// reset day" implementation wrong for the first half of every month.
func TestBeforeTheResetDayTheCycleBeganLastMonth(t *testing.T) {
	utc := time.UTC
	result := mustBounds(t, 15, utc, time.Date(2026, 9, 10, 12, 0, 0, 0, utc))

	if !result.Start.Equal(time.Date(2026, 8, 15, 0, 0, 0, 0, utc)) {
		t.Fatalf("Start = %s, want 2026-08-15T00:00:00Z", result.Start)
	}
	if !result.End.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, utc)) {
		t.Fatalf("End = %s, want 2026-09-15T00:00:00Z", result.End)
	}
	// And the year boundary is not special.
	wrapped := mustBounds(t, 15, utc, time.Date(2026, 1, 10, 0, 0, 0, 0, utc))
	if !wrapped.Start.Equal(time.Date(2025, 12, 15, 0, 0, 0, 0, utc)) {
		t.Fatalf("across the year boundary Start = %s, want 2025-12-15", wrapped.Start)
	}
}

// A reset day the month does not have rolls forward to the first of the next month.
//
// This is the case where guessing produces a plausible, wrong answer, so the expected values here come
// from reading the agent's `GetLastResetDate` rather than from intuition. With resetDay 31 and a
// reference of 15 February 2026 the answer is **31 January**, not 1 February:
//
//   - February's reset rolled forward to 1 March, which has not happened at 15 February
//   - so the agent falls back exactly one month, to January's reset, which is 31 January
//
// The consequence is a 29-day cycle from 31 January to 1 March, and it is what the node's counter
// actually did — a projection using 1 February to 1 March would be reading a window the agent never
// used. My first version walked back to "the last reset that has passed", which is a different
// question and gives a different answer on other days.
func TestAResetDayTheMonthLacksRollsToTheFirst(t *testing.T) {
	utc := time.UTC

	february := mustBounds(t, 31, utc, time.Date(2026, 2, 15, 0, 0, 0, 0, utc))
	if !february.Start.Equal(time.Date(2026, 1, 31, 0, 0, 0, 0, utc)) {
		t.Fatalf("in February Start = %s, want 2026-01-31 (February's reset rolled to 1 March and has not happened yet)", february.Start)
	}
	if !february.End.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, utc)) {
		t.Fatalf("in February End = %s, want 2026-03-01 (the 31st rolled forward)", february.End)
	}
	if february.Duration() != 29*24*time.Hour {
		t.Fatalf("Duration = %s, want 29 days: 31 January to 1 March", february.Duration())
	}

	// Once March's reset has happened the cycle is the March one — and its end is 1 May, not 31 March.
	//
	// April has 30 days, so April's reset rolls forward too. A node configured for the 31st therefore
	// resets on 1 March and again on 1 May, skipping April entirely: April has no 31st for it to land
	// on, and the roll-forward sends it to the next month's first. That is a property of the agent's
	// rule rather than of this implementation, and it is why `resetDay: 31` means "month end, or the
	// 1st when the month is too short" rather than "the 31st".
	march := mustBounds(t, 31, utc, time.Date(2026, 3, 15, 0, 0, 0, 0, utc))
	if !march.Start.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, utc)) {
		t.Fatalf("in March Start = %s, want 2026-03-01", march.Start)
	}
	if !march.End.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, utc)) {
		t.Fatalf("in March End = %s, want 2026-05-01: April's reset rolls forward too", march.End)
	}

	// A 30-day month rolls the 31st to the 1st as well.
	september := mustBounds(t, 31, utc, time.Date(2026, 9, 15, 0, 0, 0, 0, utc))
	if !september.End.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, utc)) {
		t.Fatalf("in September End = %s, want 2026-10-01", september.End)
	}

	// And in a 31-day month it does not roll at all.
	august := mustBounds(t, 31, utc, time.Date(2026, 8, 15, 0, 0, 0, 0, utc))
	if !august.End.Equal(time.Date(2026, 8, 31, 0, 0, 0, 0, utc)) {
		t.Fatalf("in August End = %s, want 2026-08-31", august.End)
	}

	// A February in a leap year has 29 days, so the 29th does not roll. The reference has to be on or
	// after the 29th: measured from 15 February the agent falls back to January's reset, which is
	// correct behaviour and would have made this assertion pass for the wrong reason.
	leap := mustBounds(t, 29, utc, time.Date(2028, 2, 29, 0, 0, 0, 0, utc))
	if !leap.Start.Equal(time.Date(2028, 2, 29, 0, 0, 0, 0, utc)) {
		t.Fatalf("2028-02-29 Start = %s, want the 29th (leap year)", leap.Start)
	}
	if !leap.End.Equal(time.Date(2028, 3, 29, 0, 0, 0, 0, utc)) {
		t.Fatalf("2028-02-29 End = %s, want 2028-03-29", leap.End)
	}
	// In a non-leap year the 29th rolls, so from 28 February the cycle is still January's.
	stillJanuary := mustBounds(t, 29, utc, time.Date(2026, 2, 28, 0, 0, 0, 0, utc))
	if !stillJanuary.Start.Equal(time.Date(2026, 1, 29, 0, 0, 0, 0, utc)) {
		t.Fatalf("2026 has no 29 February, so Start = %s, want 2026-01-29", stillJanuary.Start)
	}
	// In a non-leap year it rolls to 1 March, so mid-February is still inside the January cycle.
	nonLeap := mustBounds(t, 29, utc, time.Date(2026, 2, 28, 0, 0, 0, 0, utc))
	if !nonLeap.Start.Equal(time.Date(2026, 1, 29, 0, 0, 0, 0, utc)) {
		t.Fatalf("2026 has no 29 February, so Start = %s, want 2026-01-29", nonLeap.Start)
	}
	if !nonLeap.End.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, utc)) {
		t.Fatalf("2026 has no 29 February, so End = %s, want 2026-03-01", nonLeap.End)
	}
}

// The reset day itself belongs to the new cycle. Half-open, matching how the agent's reset behaves and
// how the forecast treats the boundary.
func TestTheResetInstantBelongsToTheNewCycle(t *testing.T) {
	utc := time.UTC
	onTheDay := mustBounds(t, 15, utc, time.Date(2026, 9, 15, 0, 0, 0, 0, utc))
	if !onTheDay.Start.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, utc)) {
		t.Fatalf("Start = %s, want the 15th itself", onTheDay.Start)
	}
	if onTheDay.Fraction(time.Date(2026, 9, 15, 0, 0, 0, 0, utc)) != 0 {
		t.Fatal("at the reset instant nothing of the cycle has elapsed")
	}
	// One second earlier still belongs to the previous cycle.
	before := mustBounds(t, 15, utc, time.Date(2026, 9, 14, 23, 59, 59, 0, utc))
	if !before.Start.Equal(time.Date(2026, 8, 15, 0, 0, 0, 0, utc)) {
		t.Fatalf("one second before the reset the cycle began at %s, want 2026-08-15", before.Start)
	}
	if !before.Contains(time.Date(2026, 9, 14, 23, 59, 59, 0, utc)) {
		t.Fatal("the instant before a reset belongs to the cycle that is ending")
	}
	if before.Contains(time.Date(2026, 9, 15, 0, 0, 0, 0, utc)) {
		t.Fatal("the reset instant does not belong to the cycle that ended")
	}
}

// The location is the node's own, because that is where its agent resets. A cycle computed in UTC for
// a Tokyo node would be off by nine hours, and the elapsed fraction would be wrong by a third of a
// day.
func TestTheCycleIsComputedInTheNodesTimezone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("no tzdata for Asia/Tokyo: %v", err)
	}

	// 2026-09-14T20:00Z is 2026-09-15T05:00 in Tokyo, so the Tokyo cycle has already reset.
	inTokyo := mustBounds(t, 15, tokyo, time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC))
	if !inTokyo.Start.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, tokyo)) {
		t.Fatalf("Tokyo Start = %s, want 2026-09-15T00:00+09:00", inTokyo.Start.In(tokyo))
	}
	// Whereas in UTC the same instant is still the previous cycle.
	inUTC := mustBounds(t, 15, time.UTC, time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC))
	if !inUTC.Start.Equal(time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC Start = %s, want 2026-08-15T00:00:00Z", inUTC.Start)
	}
	if inTokyo.Location != "Asia/Tokyo" {
		t.Fatalf("Location = %q, want the name the caller supplied so a mismatch is visible", inTokyo.Location)
	}

	// The zone offset appears in the elapsed fraction, which is the number a projection divides by.
	// 05:00 into a 30-day cycle is a slightly larger fraction than 20:00 into one starting 30 days
	// earlier, and the two must not be confused.
	tokyoFraction := inTokyo.Fraction(time.Date(2026, 9, 14, 20, 0, 0, 0, time.UTC))
	if tokyoFraction <= 0 || tokyoFraction > 0.02 {
		t.Fatalf("Tokyo fraction = %v, want a small positive number just after the reset", tokyoFraction)
	}
}

// A reset day outside the calendar is refused rather than turned into a one-instant cycle, which a
// caller would then divide by.
func TestAnImpossibleResetDayIsRefused(t *testing.T) {
	for _, day := range []int{0, -1, 32, 99} {
		if _, err := Bounds(day, time.UTC, time.Now()); err == nil {
			t.Errorf("reset day %d must be refused", day)
		}
	}
	// 1 and 31 are the ends of the valid range.
	for _, day := range []int{1, 31} {
		if _, err := Bounds(day, time.UTC, time.Now()); err != nil {
			t.Errorf("reset day %d must be accepted: %v", day, err)
		}
	}
}

// Elapsed and Fraction are clamped, so a report that arrived slightly after the reset cannot make a
// projection divide by more than its window.
func TestElapsedAndFractionAreClampedToTheCycle(t *testing.T) {
	utc := time.UTC
	result := mustBounds(t, 15, utc, time.Date(2026, 9, 20, 0, 0, 0, 0, utc))

	if result.Fraction(result.Start.Add(-time.Hour)) != 0 {
		t.Fatal("before the cycle the fraction is 0")
	}
	if result.Fraction(result.End.Add(time.Hour)) != 1 {
		t.Fatal("after the cycle the fraction is 1")
	}
	if fraction := result.Fraction(result.Start.Add(result.Duration() / 2)); fraction < 0.49 || fraction > 0.51 {
		t.Fatalf("halfway through the fraction is %v, want ~0.5", fraction)
	}
	if fraction := result.Fraction(result.End); fraction != 1 {
		t.Fatalf("at the end instant the fraction is %v, want 1 (the cycle is half-open, so the end is outside it)", fraction)
	}
}

// An unknown timezone falls back to UTC rather than failing: a node whose zone cannot be resolved is
// still a node whose traffic should be watched, and the name is reported so the fallback is visible.
func TestAnUnknownTimezoneFallsBackToUTC(t *testing.T) {
	if got := Location("Not/AZone"); got != time.UTC {
		t.Fatalf("Location(unknown) = %v, want UTC", got)
	}
	if got := Location(""); got != time.UTC {
		t.Fatalf("Location(empty) = %v, want UTC", got)
	}
	if got := Location("UTC"); got != time.UTC {
		t.Fatalf("Location(UTC) = %v, want UTC", got)
	}
	if got := Location("Asia/Tokyo"); got == time.UTC {
		t.Fatal("a resolvable zone must be used")
	}
	// And a nil location is UTC rather than a panic.
	result := mustBounds(t, 15, nil, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	if !result.Start.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("nil location Start = %s, want the UTC boundary", result.Start)
	}
}

// Every cycle must be forward in time, and every month must map to a reset that is not in the past.
//
// The invariants here are the ones that actually hold. My first version of this test also asserted
// that consecutive cycles chain without gap or overlap, and that is **false** for a reset day late in
// a short month — a property of the agent's rule, not a bug in this implementation:
//
//   - a window referenced on 15 February with resetDay 31 began on 31 January and ends on 1 March
//   - a window referenced on 15 March began on 1 March and ends on 1 May
//
// Those two are contiguous, but the same is not true of every pair: with resetDay 29 in 2026, the
// window for mid-March runs 29 March to 1 May while the one for mid-April runs 29 April to 29 May, so
// April's window *starts inside* March's. Forcing non-overlap would mean choosing a rule the agent
// does not implement, and then a projection would read a window the node's counter never used.
//
// What does hold, and is what a projection depends on: each cycle runs forward, and a reset day that
// the month has lands in that month rather than drifting.
func TestEveryCycleRunsForwardAndResetsLandWhereTheMonthAllows(t *testing.T) {
	utc := time.UTC
	for _, resetDay := range []int{1, 15, 28, 29, 30, 31} {
		for month := 1; month <= 12; month++ {
			reference := time.Date(2026, time.Month(month), 15, 0, 0, 0, 0, utc)
			current := mustBounds(t, resetDay, utc, reference)

			if !current.End.After(current.Start) {
				t.Fatalf("reset day %d, month %d: cycle %s..%s is not forward in time",
					resetDay, month, current.Start, current.End)
			}
			// The reference is inside its own cycle unless the reset it is measured from rolled past it,
			// which cannot happen: the start is at or before the reference by construction.
			if !current.Contains(reference) {
				t.Fatalf("reset day %d, month %d: the cycle %s..%s does not contain its own reference %s",
					resetDay, month, current.Start, current.End, reference)
			}
			// The fraction is in [0, 1]: zero exactly at a reset instant, one at the cycle's end, which is
			// the half-open boundary the agent's counter produces.
			if fraction := current.Fraction(reference); fraction < 0 || fraction > 1 {
				t.Fatalf("reset day %d, month %d: fraction %v is outside [0, 1]",
					resetDay, month, fraction)
			}
			// The declared reset day is reported back, so a reader can see what the boundaries came from.
			if current.ResetDay != resetDay {
				t.Fatalf("ResetDay = %d, want %d", current.ResetDay, resetDay)
			}

			// The start is at or before the reference and within one month of it. That bound is what the
			// rule guarantees, and it is as far as a month comparison can be taken: a roll-forward *moves* a
			// reset into the following month, so "a day the month has lands in that month" is not an
			// invariant — 29 February 2026 does not exist, and that reset lands on 1 March.
			//
			// Two attempts at asserting where the start lands by month were both wrong for that reason, which
			// is worth recording: the roll-forward makes month arithmetic on the *result* unreliable, so the
			// reliable assertions are the ones about ordering and containment.
			if current.Start.After(reference) {
				t.Fatalf("reset day %d, month %d: the cycle starts after its own reference (%s > %s)",
					resetDay, month, current.Start, reference)
			}
			if current.Start.Before(reference.AddDate(0, -1, -1)) {
				t.Fatalf("reset day %d, month %d: the cycle starts more than a month before its reference (%s)",
					resetDay, month, current.Start)
			}
		}
	}
}
