package netstatic

import (
	"testing"
	"time"
)

// The cycle cumulative is assembled from two sources that must not overlap.
//
// ## The property
//
//   - the **accumulator** counts traffic from the moment it started, and is unaffected by sample pruning
//   - the **ledger** holds the history from before that moment, while the samples are still retained
//
// `StartedAt` is the boundary. Adding the two is only correct if the ledger contribution stops there, and the
// tests below are about exactly that — a version that added the whole cycle's ledger on top of the accumulator
// would double-count everything the accumulator had already seen, which is the same class of mistake as the
// defect this replaced.

// A cycle that began before the accumulator existed is reported in full: the ledger supplies the part the
// accumulator could not have seen.
//
// This is the case that was wrong in the field. The accumulator alone reported 0.14 GB while the provider
// billed 57 GB for the same period, because the accumulator only counts from its own first sample.
func TestTheLedgerSuppliesThePartBeforeTheAccumulatorStarted(t *testing.T) {
	resetState(t)

	resetDay := cycleResetDay(9, time.Now())
	if resetDay == 0 {
		t.Fatal("cycleResetDay returned 0")
	}

	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	// History from before the accumulator: three samples of 1 GiB each, all inside this cycle.
	store.Interfaces = map[string][]TrafficData{
		"eth0": {
			{Timestamp: resetDay + 10, Tx: 1 << 30, Rx: 1 << 30},
			{Timestamp: resetDay + 20, Tx: 1 << 30, Rx: 1 << 30},
			{Timestamp: resetDay + 30, Tx: 1 << 30, Rx: 1 << 30},
		},
	}
	store.Cycles = map[string]CycleTraffic{}
	mu.Unlock()

	// The accumulator starts later and sees one sample of 1 GiB.
	startedAt := resetDay + 100
	accumulateCycleLocked("eth0", 1<<30, 1<<30, startedAt)

	got, _, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic: %v", err)
	}
	const want = 4 << 30 // three from the ledger, one from the accumulator
	if got["eth0"].Tx != want {
		t.Errorf("cycle tx = %d, want %d (3 GiB of retained history + 1 GiB accumulated).\n"+
			"Reporting only the accumulator loses the history; reporting only the ledger loses the part that "+
			"has aged out. Both are needed and they must not overlap.", got["eth0"].Tx, want)
	}
}

// Samples the accumulator has already counted are not counted a second time.
func TestSamplesAfterTheAccumulatorStartedAreNotAddedTwice(t *testing.T) {
	resetState(t)

	resetDay := cycleResetDay(9, time.Now())
	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	store.Interfaces = map[string][]TrafficData{}
	store.Cycles = map[string]CycleTraffic{}
	mu.Unlock()

	startedAt := resetDay + 100
	accumulateCycleLocked("eth0", 5<<30, 5<<30, startedAt)

	// The same traffic also reaching the ledger, as it does in reality: sampleOnceLocked appends to
	// staticCache *and* accumulates. Timestamps at or after StartedAt must be skipped.
	mu.Lock()
	staticCache = map[string][]TrafficData{
		"eth0": {
			{Timestamp: startedAt, Tx: 5 << 30, Rx: 5 << 30},
			{Timestamp: startedAt + 30, Tx: 2 << 30, Rx: 2 << 30},
		},
	}
	mu.Unlock()

	got, _, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic: %v", err)
	}
	if got["eth0"].Tx != 5<<30 {
		t.Errorf("cycle tx = %d, want %d.\n"+
			"Anything at or after StartedAt was already accumulated; adding it again doubles the figure — the "+
			"same shape of error as the phantom samples.", got["eth0"].Tx, int64(5)<<30)
	}
}

// With no accumulator at all, the ledger is the only history and is reported whole.
//
// This is a node that was configured for a cycle but has not sampled yet, and it must not report zero for a
// cycle it has complete records of.
func TestWithNoAccumulatorTheLedgerIsReportedWhole(t *testing.T) {
	resetState(t)

	resetDay := cycleResetDay(9, time.Now())
	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	store.Interfaces = map[string][]TrafficData{
		"eth0": {
			{Timestamp: resetDay + 10, Tx: 7 << 30, Rx: 3 << 30},
		},
	}
	store.Cycles = map[string]CycleTraffic{}
	mu.Unlock()

	got, _, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic: %v", err)
	}
	if got["eth0"].Tx != 7<<30 || got["eth0"].Rx != 3<<30 {
		t.Errorf("cycle = tx %d rx %d, want tx %d rx %d", got["eth0"].Tx, got["eth0"].Rx, int64(7)<<30, int64(3)<<30)
	}
}

// Samples from a previous cycle are never included, whichever source would have supplied them.
func TestOnlyTheCurrentCycleIsIncluded(t *testing.T) {
	resetState(t)

	resetDay := cycleResetDay(9, time.Now())
	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	store.Interfaces = map[string][]TrafficData{
		"eth0": {
			// One sample from before this cycle began, one inside it.
			{Timestamp: resetDay - 3600, Tx: 9 << 30, Rx: 9 << 30},
			{Timestamp: resetDay + 10, Tx: 1 << 30, Rx: 1 << 30},
		},
	}
	store.Cycles = map[string]CycleTraffic{}
	mu.Unlock()

	got, _, err := GetCycleTraffic()
	if err != nil {
		t.Fatalf("GetCycleTraffic: %v", err)
	}
	if got["eth0"].Tx != 1<<30 {
		t.Errorf("cycle tx = %d, want %d: a sample from before the reset day belongs to the previous cycle",
			got["eth0"].Tx, int64(1)<<30)
	}
}

// An accumulator inherited from a ledger without `StartedAt` is re-baselined rather than trusted.
//
// A zero `StartedAt` would make the boundary check pass every ledger sample through, double-counting the whole
// cycle against an accumulator that had already counted part of it. Re-baselining costs one cycle of accuracy
// and cannot silently inflate.
func TestAnAccumulatorWithoutStartedAtIsRebaselined(t *testing.T) {
	resetState(t)

	resetDay := cycleResetDay(9, time.Now())
	mu.Lock()
	config = configOrDefault(NetStaticConfig{DataPreserveDay: 31, MonthRotate: 9})
	store.Config = config
	store.Interfaces = map[string][]TrafficData{}
	store.Cycles = map[string]CycleTraffic{
		// An entry written by the version before StartedAt existed.
		"eth0": {Tx: 40 << 30, Rx: 40 << 30, ResetDay: resetDay},
	}
	mu.Unlock()

	// One sample arrives; the accumulator must restart rather than keep the stale total with no boundary.
	accumulateCycleLocked("eth0", 1<<30, 1<<30, resetDay+100)

	mu.RLock()
	entry := store.Cycles["eth0"]
	mu.RUnlock()

	if entry.StartedAt == 0 {
		t.Error("StartedAt is still zero, so the ledger boundary cannot be applied and the next read would " +
			"double-count the cycle against the accumulator")
	}
	if entry.Tx != 1<<30 {
		t.Errorf("accumulator tx = %d, want %d: an entry with no StartedAt is re-baselined, not carried forward",
			entry.Tx, int64(1)<<30)
	}
}
