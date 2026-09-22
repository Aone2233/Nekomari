package upload

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestContentionCountsRefusalsByOperation pins that a refused acquisition is
// attributed to the operation that was refused. The attribution is the point of
// the counter: "who blocks whom" is the question the decision to keep or split
// the lock depends on, and a single total could not answer it.
func TestContentionCountsRefusalsByOperation(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}

	// Hold the lock the way an in-flight finalization does.
	store.mu.Lock()

	if _, err := store.Init(PurposeTheme, "theme.zip", 8); !errors.Is(err, ErrBusy) {
		t.Fatalf("Init: want ErrBusy, got %v", err)
	}
	if err := store.SaveChunk("ignored", 0, strings.NewReader("x")); !errors.Is(err, ErrBusy) {
		t.Fatalf("SaveChunk: want ErrBusy, got %v", err)
	}
	if _, err := store.Merge("ignored"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Merge: want ErrBusy, got %v", err)
	}
	if _, err := store.Complete("ignored", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("Complete: want ErrBusy, got %v", err)
	}
	if err := store.Cancel("ignored"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Cancel: want ErrBusy, got %v", err)
	}
	if err := store.CleanupExpired(); !errors.Is(err, ErrBusy) {
		t.Fatalf("CleanupExpired: want ErrBusy, got %v", err)
	}

	store.mu.Unlock()

	want := map[string]int{opInit: 1, opChunk: 1, opMerge: 1, opComplete: 1, opCancel: 1, opCleanup: 1}
	busy := store.Contention().Busy
	if len(busy) != len(want) {
		t.Fatalf("busy = %v, want %v", busy, want)
	}
	for op, count := range want {
		if busy[op] != count {
			t.Errorf("busy[%s] = %d, want %d", op, busy[op], count)
		}
	}
}

// TestContentionRecordsHoldDuration covers the other half: a successful hold is
// timed, because Complete holds the lock across the whole installation and that
// duration is what an unrelated administrator would be waiting behind.
func TestContentionRecordsHoldDuration(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}

	if _, err := store.Init(PurposeTheme, "theme.zip", 8); err != nil {
		t.Fatal(err)
	}

	timing, ok := store.Contention().Hold[opInit]
	if !ok || timing.Count != 1 {
		t.Fatalf("hold[%s] = %+v (present=%v), want exactly one hold", opInit, timing, ok)
	}
	if timing.Max <= 0 || timing.Total <= 0 {
		t.Fatalf("hold duration was not measured: %+v", timing)
	}
	if busy := store.Contention().Busy; len(busy) != 0 {
		t.Fatalf("a successful acquisition was counted as a refusal: %v", busy)
	}
}

// TestContentionSummaryIsReportedOnlyOnce pins the log gate: the figures are
// handed to the logger once and then marked reported, so the hourly pass does
// not repeat the same numbers forever.
func TestContentionSummaryIsReportedOnlyOnce(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}

	if _, err := store.Init(PurposeTheme, "theme.zip", 8); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.takeContentionSummary(); !ok {
		t.Fatal("caller activity did not mark the figures for reporting")
	}
	if _, ok := store.takeContentionSummary(); ok {
		t.Fatal("the same figures were reported twice")
	}
	// Reporting must not clear the counters themselves.
	if store.Contention().Hold[opInit].Count != 1 {
		t.Fatal("reporting cleared the counters")
	}
}

// TestMaintenanceAloneDoesNotTriggerTheContentionLog is what keeps an idle panel
// quiet: the hourly pass takes the lock on every tick whether or not anyone is
// uploading, so it must be recorded without being treated as activity.
func TestMaintenanceAloneDoesNotTriggerTheContentionLog(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}

	if err := store.CleanupExpired(); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.takeContentionSummary(); ok {
		t.Fatal("the hourly maintenance pass alone marked the figures for reporting")
	}
	if store.Contention().Hold[opCleanup].Count != 1 {
		t.Fatalf("the maintenance hold was not recorded: %+v", store.Contention().Hold)
	}
}

// TestExpirePassLogsContentionOnlyWhenThereWasActivity ties the counter to the
// only thing that reads it in production.
func TestExpirePassLogsContentionOnlyWhenThereWasActivity(t *testing.T) {
	store := &Store{Root: t.TempDir(), MaxSize: 1 << 20, FreeSpace: unlimitedFreeSpace}
	logs := captureLogs(t)

	store.expirePass()
	if lines := logs.matching("lock activity"); len(lines) != 0 {
		t.Fatalf("an idle pass logged contention: %v", lines)
	}

	if _, err := store.Init(PurposeTheme, "theme.zip", 8); err != nil {
		t.Fatal(err)
	}
	store.expirePass()
	lines := logs.matching("lock activity")
	if len(lines) != 1 {
		t.Fatalf("want exactly one contention line, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], opInit) {
		t.Fatalf("the line does not name the operation: %v", lines[0])
	}

	store.expirePass()
	if lines := logs.matching("lock activity"); len(lines) != 1 {
		t.Fatalf("an hour with no new activity logged again: %v", lines)
	}
}

// TestContentionFormattingIsStable keeps the log line comparable between runs
// instead of reshuffling with Go's map order.
func TestContentionFormattingIsStable(t *testing.T) {
	if got := formatBusy(map[string]int{opMerge: 2, opChunk: 10, opInit: 1}); got != "chunk=10,init=1,merge=2" {
		t.Fatalf("formatBusy = %q", got)
	}
	if got := formatBusy(nil); got != "none" {
		t.Fatalf("formatBusy(nil) = %q", got)
	}
	hold := map[string]OpTiming{opComplete: {Count: 2, Max: 1500 * time.Millisecond}}
	if got := formatHold(hold); got != "complete=2/1.5s" {
		t.Fatalf("formatHold = %q", got)
	}
	if got := formatHold(nil); got != "none" {
		t.Fatalf("formatHold(nil) = %q", got)
	}
}
