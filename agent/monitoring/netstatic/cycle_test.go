package netstatic

import (
	"testing"
	"time"
)

// The cycle ledger must not depend on how long samples are retained, and must roll over at the reset day.
//
// ## What this pins
//
// The cycle cumulative used to be "sum the ledger samples in [reset day, now]". `purgeExpiredLocked` deletes
// samples older than `DataPreserveDay`, so **once the cycle outlives the retention window the cumulative
// shrinks** — and the panel read a shrinking value as a counter reset, recording the whole cumulative as a
// single increment.
//
// Observed on CLISP (`--month-rotate 9`, reset day 19 days back): single samples of 41 GB at 06:12 and 09:58,
// while that interface had carried 0.29 GB since boot and the measured rate was ~0.05 GB/day. Those 41 GB
// were the cycle cumulative counted as one interval's traffic.
//
// The accumulator is updated in the sampling path, so it is unaffected by pruning. That is the property these
// tests hold — the number below is *survives a purge that removes every sample*.

func TestCycleTotalSurvivesPurgingEverySample(t *testing.T) {
	resetState(t)

	// A 31-day retention with a 30-day-old cycle is the configuration that failed: the oldest part of the
	// cycle is already outside the window.
	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	store.Cycles = map[string]CycleTraffic{}
	sampledAt := uint64(time.Now().Unix())
	mu.Unlock()

	// Accumulate a cycle's worth of traffic, then purge as if the samples had aged out entirely.
	const perSample = 1 << 30 // 1 GiB
	for i := 0; i < 40; i++ {
		accumulateCycleLocked("ens17", perSample, perSample, sampledAt)
	}

	before, _, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic: %v", err)
	}
	if before["ens17"].Tx != 40*perSample {
		t.Fatalf("cycle total = %d, want %d", before["ens17"].Tx, 40*perSample)
	}

	// Empty the sample ledger the way retention eventually does, and re-ask.
	mu.Lock()
	store.Interfaces = map[string][]TrafficData{}
	mu.Unlock()

	after, _, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic after purge: %v", err)
	}
	if after["ens17"].Tx != 40*perSample {
		t.Errorf("cycle total = %d after every sample was purged, want %d unchanged.\n"+
			"A cumulative that shrinks when samples expire is what the panel misreads as a counter reset, "+
			"and then records in full as one interval's traffic.",
			after["ens17"].Tx, 40*perSample)
	}
}

// Crossing the reset day zeroes the cycle rather than carrying the previous cycle over.
//
// The dates are derived from the *current* reset day rather than hardcoded, because `GetCycleTraffic` compares
// against the cycle that is current **now**: a test that accumulated traffic dated months ago would be
// asserting against a stale cycle and read as empty for the wrong reason.
func TestCycleResetsWhenTheResetDayPasses(t *testing.T) {
	resetState(t)

	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	store.Cycles = map[string]CycleTraffic{}
	mu.Unlock()

	now := time.Now()
	currentReset := cycleResetDay(9, now)
	if currentReset == 0 {
		t.Fatal("cycleResetDay returned 0 for MonthRotate=9")
	}

	// A sample inside the current cycle, then one inside what will be the *next* cycle.
	insideThisCycle := uint64(time.Unix(int64(currentReset), 0).Add(time.Hour).Unix())
	nextReset := cycleResetDay(9, time.Unix(int64(currentReset), 0).AddDate(0, 1, 1))
	if nextReset == currentReset {
		t.Fatal("the next cycle's reset day equals the current one; the test cannot distinguish them")
	}
	insideNextCycle := uint64(time.Unix(int64(nextReset), 0).Add(time.Hour).Unix())

	accumulateCycleLocked("ens17", 5_000, 7_000, insideThisCycle)
	accumulateCycleLocked("ens17", 100, 200, insideNextCycle)

	// The ledger now holds the *next* cycle, so reading as of now reports no data for this cycle —
	// which is the correct answer, and is asserted in the stale-cycle test. Re-ask with the entry's own
	// date by checking the stored entry directly, then confirm the rollover happened.
	mu.RLock()
	entry := store.Cycles["ens17"]
	mu.RUnlock()

	if entry.Tx != 100 || entry.Rx != 200 {
		t.Errorf("cycle = tx %d rx %d after crossing the reset day, want tx 100 rx 200: the previous cycle's "+
			"total must not be carried into the new one", entry.Tx, entry.Rx)
	}
	if entry.ResetDay != nextReset {
		t.Errorf("entry reset day = %d, want the next cycle's %d", entry.ResetDay, nextReset)
	}
	if entry.Tx >= 5_000 {
		t.Error("the old cycle's volume is still included, so the rollover added rather than reset")
	}
}

// A read must not report a stale cycle, and must not mutate state to fix it either.
//
// The zeroing belongs to the sampling path. If `GetCycleTraffic` rewrote the ledger it would be a read with a
// side effect, and two concurrent readers could interleave with a sampler.
func TestAStaleCycleReadsAsEmptyWithoutMutating(t *testing.T) {
	resetState(t)

	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	// An entry from a much older cycle.
	store.Cycles = map[string]CycleTraffic{
		"ens17": {Tx: 9_000, Rx: 9_000, ResetDay: uint64(time.Date(2026, 1, 9, 0, 0, 0, 0, time.Local).Unix())},
	}
	mu.Unlock()

	got, _, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic: %v", err)
	}
	if got["ens17"].Tx != 0 {
		t.Errorf("a cycle from months ago reads as %d, want 0: reporting it would put the previous cycle's "+
			"traffic in this cycle's quota figure", got["ens17"].Tx)
	}

	// The stored entry is untouched, so the sampler remains the only writer.
	mu.RLock()
	stored := store.Cycles["ens17"].Tx
	mu.RUnlock()
	if stored != 9_000 {
		t.Errorf("stored entry was modified to %d by a read; zeroing belongs to the sampling path", stored)
	}
}

// Without `--month-rotate` there is no cycle, and the reset day is reported as zero rather than invented.
func TestNoCycleWhenMonthRotateIsUnset(t *testing.T) {
	resetState(t)

	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31})
	store.Config = config
	store.Cycles = map[string]CycleTraffic{}
	mu.Unlock()

	if day := cycleResetDay(0, time.Now()); day != 0 {
		t.Errorf("cycleResetDay(0, now) = %d, want 0", day)
	}
	accumulateCycleLocked("ens17", 1_000, 1_000, uint64(time.Now().Unix()))

	got, expectedDay, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic: %v", err)
	}
	if expectedDay != 0 {
		t.Errorf("expected reset day = %d, want 0 when no cycle is configured", expectedDay)
	}
	if got["ens17"].Tx != 0 {
		t.Errorf("cycle = %d with no cycle configured, want 0", got["ens17"].Tx)
	}
}

// The reset day is the most recent one that has already passed, and never a future date.
//
// An off-by-one here would zero the cycle one day early and under-report the quota, which is the direction
// that hurts: an operator would not be warned before exceeding it.
func TestCycleResetDayIsTheMostRecentPastOccurrence(t *testing.T) {
	loc := time.Local
	cases := []struct {
		now  time.Time
		day  int
		want time.Time
	}{
		// On the reset day itself, that day is the current cycle's start.
		{time.Date(2026, 9, 9, 0, 0, 0, 0, loc), 9, time.Date(2026, 9, 9, 0, 0, 0, 0, loc)},
		{time.Date(2026, 9, 9, 23, 59, 0, 0, loc), 9, time.Date(2026, 9, 9, 0, 0, 0, 0, loc)},
		// Before it, the previous month's occurrence.
		{time.Date(2026, 9, 8, 12, 0, 0, 0, loc), 9, time.Date(2026, 8, 9, 0, 0, 0, 0, loc)},
		// After it, this month's.
		{time.Date(2026, 9, 10, 12, 0, 0, 0, loc), 9, time.Date(2026, 9, 9, 0, 0, 0, 0, loc)},
		// A day that does not exist in every month is clamped, and the clamp must not produce a future date.
		{time.Date(2026, 2, 15, 12, 0, 0, 0, loc), 31, time.Date(2026, 1, 28, 0, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		got := cycleResetDay(tc.day, tc.now)
		if got != uint64(tc.want.Unix()) {
			t.Errorf("cycleResetDay(%d, %s) = %s, want %s",
				tc.day, tc.now.Format("2006-01-02 15:04"), time.Unix(int64(got), 0).Format("2006-01-02 15:04"),
				tc.want.Format("2006-01-02 15:04"))
		}
		if got > uint64(tc.now.Unix()) {
			t.Errorf("cycleResetDay(%d, %s) returned a future date %s: the cycle would start after now",
				tc.day, tc.now.Format("2006-01-02 15:04"), time.Unix(int64(got), 0).Format("2006-01-02"))
		}
	}
}
