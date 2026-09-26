package netstatic

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The bug these pin down, from the fleet: a node reported a single 59.5 GB traffic
// delta in the minute its agent started, against a real rate near 7 GB/day and an
// interface whose lifetime counter read 187 MB. The panel's "today" went from 7 GB
// to 160 GB. Two things were wrong, and each has a test below:
//
//  1. the counter baseline lived only in memory, so a restart lost it and the whole
//     cumulative counter was attributed to one sampling interval
//  2. nothing checked whether a delta was physically possible for the elapsed time
func TestPlausibleDeltaRejectsMoreThanTheLinkCouldCarry(t *testing.T) {
	// One sampling interval on a 1 Gbps link, with the configured headroom.
	elapsed := time.Duration(DefaultDetectInterval) * time.Second
	ceiling := ceilingBytes(elapsed)

	cases := []struct {
		name    string
		tx      uint64
		rx      uint64
		elapsed time.Duration
		want    bool
	}{
		{"a quiet interval", 1 << 10, 1 << 10, elapsed, true},
		{"exactly at the ceiling", ceiling, ceiling, elapsed, true},
		{"one byte over", ceiling + 1, 0, elapsed, false},
		{"the observed 59.5 GB in a minute", 59_480 * 1024 * 1024, 0, time.Minute, false},
		// 5 GiB in a minute is 716 Mbps, so it fits a 1 Gbps link and must pass.
		// (7 GiB would be 1002 Mbps and must *not* pass — that was the first
		// version of this case, and the guard was right to reject it.)
		{"a busy but possible minute", 5 * 1024 * 1024 * 1024, 0, time.Minute, true},
		{"no elapsed time at all", 1, 1, 0, false},
		{"a stale baseline", 1 << 20, 1 << 20, deltaGapLimit() + time.Second, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := plausibleDelta("eth0", tc.tx, tc.rx, tc.elapsed)
			if ok != tc.want {
				t.Fatalf("plausibleDelta(tx=%d, rx=%d, elapsed=%s) = %v (%s), want %v",
					tc.tx, tc.rx, tc.elapsed, ok, reason, tc.want)
			}
			if !ok && reason == "" {
				t.Fatal("a rejected delta must say why: the log line is the only trace it leaves")
			}
		})
	}
}

// The ceiling has to grow with the window, or a legitimately long gap (the agent was
// stopped for hours) would have its whole real accumulated traffic discarded.
func TestCeilingGrowsWithElapsedTime(t *testing.T) {
	short := ceilingBytes(30 * time.Second)
	long := ceilingBytes(10 * time.Minute)
	if long <= short {
		t.Fatalf("ceiling for 10 minutes (%d) should exceed 30 seconds (%d)", long, short)
	}
	if ceilingBytes(0) != 0 {
		t.Fatal("no elapsed time means no traffic can have happened")
	}
}

// The core of the fix: the baseline survives a restart. Without this, the first
// sample after a restart compares a live cumulative counter against nothing.
func TestBaselineSurvivesARestart(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	SaveFilePath = filepath.Join(dir, "net_static.json")

	// A running agent has just sampled these counters.
	const tx, rx = uint64(5_000_000_000), uint64(6_000_000_000)
	now := uint64(time.Now().Unix())
	lastCounters["eth0"] = CounterSample{Tx: tx, Rx: rx, At: now}
	store.Interfaces["eth0"] = []TrafficData{{Timestamp: now, Tx: 1, Rx: 1}}

	mu.Lock()
	if err := saveToFileLocked(); err != nil {
		mu.Unlock()
		t.Fatalf("save: %v", err)
	}
	// Simulate the restart: everything in memory goes away, including the baseline.
	lastCounters = map[string]CounterSample{}
	store = NetStatic{Interfaces: map[string][]TrafficData{}}
	mu.Unlock()

	if err := loadFromFileLocked(); err != nil {
		t.Fatalf("load: %v", err)
	}

	restored, ok := lastCounters["eth0"]
	if !ok {
		t.Fatal("the baseline did not survive the restart, so the next delta would span the whole uptime")
	}
	if restored.Tx != tx || restored.Rx != rx {
		t.Fatalf("restored baseline = {Tx:%d Rx:%d}, want {Tx:%d Rx:%d}", restored.Tx, restored.Rx, tx, rx)
	}
	if restored.At != now {
		t.Fatalf("restored timestamp = %d, want %d: without it the delta cannot be bounded", restored.At, now)
	}
}

// A restart with no baseline recorded (an older ledger, or a first run) must not
// produce a delta either: `At == 0` makes any delta implausible, so the first
// interval is skipped rather than counted against a counter that may be years old.
func TestAMissingBaselineSkipsTheFirstIntervalInsteadOfCountingIt(t *testing.T) {
	ok, reason := plausibleDelta("eth0", 59_000_000_000, 0,
		time.Duration(uint64(time.Now().Unix()))*time.Second)
	if ok {
		t.Fatal("a baseline with no timestamp must not be trusted")
	}
	if reason == "" {
		t.Fatal("expected a reason for rejecting the delta")
	}
}

// After a rejected delta the baseline is taken from the current reading, so the next
// interval is measured normally instead of staying poisoned.
func TestRejectionRebaselinesRatherThanFailingForever(t *testing.T) {
	resetState(t)
	now := uint64(time.Now().Unix())
	// A baseline that is far too old for the delta about to be presented.
	lastCounters["eth0"] = CounterSample{Tx: 0, Rx: 0, At: now - uint64(24*3600)}

	elapsed := time.Duration(now-lastCounters["eth0"].At) * time.Second
	if ok, _ := plausibleDelta("eth0", 1<<30, 1<<30, elapsed); ok {
		t.Fatal("a day-old baseline must not be trusted for a one-bucket delta")
	}

	// And a fresh baseline is accepted again.
	lastCounters["eth0"] = CounterSample{Tx: 1000, Rx: 1000, At: now}
	dtx := safeDelta(2000, 1000)
	if ok, reason := plausibleDelta("eth0", dtx, dtx, 30*time.Second); !ok {
		t.Fatalf("a normal interval after rebaselining should be accepted, got: %s", reason)
	}
}

// A load must leave the ledger due for a rewrite.
//
// This is the fault that made the fix look broken in production: startup wrote the
// file while the baseline was still empty, `shouldRewriteFileLocked` then treated
// that write as recent, and the real baseline waited a full rewrite interval (30
// minutes by default) before it could reach disk. Read fourteen minutes after the
// rollout, every node's ledger still had no `last_counters`.
func TestALoadMakesTheBaselineDueForRewrite(t *testing.T) {
	resetState(t)
	dir := t.TempDir()
	SaveFilePath = filepath.Join(dir, "net_static.json")
	oldRewrite := DefaultRewriteInterval
	DefaultRewriteInterval = 1800
	t.Cleanup(func() { DefaultRewriteInterval = oldRewrite })

	// A ledger with no baseline, as an older agent would have written.
	store.Interfaces["eth0"] = []TrafficData{{Timestamp: 1, Tx: 1, Rx: 1}}
	mu.Lock()
	if err := saveToFileLocked(); err != nil {
		mu.Unlock()
		t.Fatalf("save: %v", err)
	}
	// The startup write just happened, so by the interval alone nothing is due.
	if shouldRewriteFileLocked(nowUnix()) {
		mu.Unlock()
		t.Fatal("nothing changed since the write, so no rewrite should be due")
	}
	mu.Unlock()

	if err := loadFromFileLocked(); err != nil {
		t.Fatalf("load: %v", err)
	}
	// The sampler has since recorded a baseline.
	lastCounters["eth0"] = CounterSample{Tx: 100, Rx: 200, At: uint64(time.Now().Unix())}

	if !shouldRewriteFileLocked(nowUnix()) {
		t.Fatal("the baseline is in memory only; the next save must be allowed to write it")
	}

	mu.Lock()
	err := saveToFileLocked()
	mu.Unlock()
	if err != nil {
		t.Fatalf("save after load: %v", err)
	}
	raw, err := os.ReadFile(SaveFilePath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !contains(string(raw), "last_counters") {
		t.Fatalf("the baseline still did not reach disk: %s", raw)
	}
}

// The snapshot handed to the ledger must be a copy: the sampler keeps mutating the
// live map, and sharing it would make "the file's baseline" change under the writer.
func TestSnapshotIsACopy(t *testing.T) {
	resetState(t)
	lastCounters["eth0"] = CounterSample{Tx: 1, Rx: 2, At: 3}
	snapshot := snapshotLastCountersLocked()

	lastCounters["eth0"] = CounterSample{Tx: 9, Rx: 9, At: 9}
	if snapshot["eth0"].Tx != 1 {
		t.Fatalf("snapshot changed with the live map: got Tx=%d, want 1", snapshot["eth0"].Tx)
	}
	if snapshotLastCountersLocked() == nil && len(lastCounters) > 0 {
		t.Fatal("a non-empty baseline must produce a non-nil snapshot")
	}
}

// An empty baseline writes no field at all, so a ledger that never had one stays
// byte-compatible with older agent versions.
func TestEmptyBaselineIsOmittedFromTheLedger(t *testing.T) {
	resetState(t)
	lastCounters = map[string]CounterSample{}
	if got := snapshotLastCountersLocked(); got != nil {
		t.Fatalf("expected nil for an empty baseline, got %v", got)
	}

	dir := t.TempDir()
	SaveFilePath = filepath.Join(dir, "net_static.json")
	store.Interfaces["eth0"] = []TrafficData{{Timestamp: 1, Tx: 1, Rx: 1}}
	mu.Lock()
	err := saveToFileLocked()
	mu.Unlock()
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(SaveFilePath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if contains(string(raw), "last_counters") {
		t.Fatalf("an empty baseline should not be written: %s", raw)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}

