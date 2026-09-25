package server

import (
	"testing"
	"time"
)

// TestReconnectBackoffGrowsThenHoldsAtTheCap pins the shape of the retry curve:
// a single blip still retries in seconds, a long outage settles at the cap, and
// no input can push the result outside [base, max].
func TestReconnectBackoffGrowsThenHoldsAtTheCap(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{4, 40 * time.Second},
		{5, 80 * time.Second},
		{6, 2 * time.Minute}, // 160s would be next; the cap holds it
		{9, 2 * time.Minute},
		{64, 2 * time.Minute}, // shifting this far would overflow a Duration
		{1000, 2 * time.Minute},
		{0, 5 * time.Second}, // defensive: treated as the first failure
		{-3, 5 * time.Second},
	}
	for _, tc := range cases {
		// Jitter is ±25%, so sample enough times to cover the band and check the
		// envelope rather than one draw.
		lo := tc.want - tc.want/4
		for i := 0; i < 200; i++ {
			got := reconnectBackoff(tc.failures)
			if got < reconnectBaseDelay || got > reconnectMaxDelay {
				t.Fatalf("reconnectBackoff(%d) = %v, outside [%v, %v]",
					tc.failures, got, reconnectBaseDelay, reconnectMaxDelay)
			}
			if tc.failures >= 6 {
				continue // at the cap every draw must be <= max; already checked
			}
			if got < lo || got > tc.want+tc.want/4 {
				t.Fatalf("reconnectBackoff(%d) = %v, want about %v (±25%%)",
					tc.failures, got, tc.want)
			}
		}
	}
}

// TestReconnectBackoffJitters stops the backoff from being a fixed interval in
// disguise: a fleet that lost the panel at the same moment must not come back in
// lockstep, because the panel's first second after recovery is its weakest.
func TestReconnectBackoffJitters(t *testing.T) {
	seen := make(map[time.Duration]struct{})
	for i := 0; i < 200; i++ {
		seen[reconnectBackoff(6)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("backoff at the cap produced %d distinct values, want jitter", len(seen))
	}
}

// TestFailureCounterResetsAfterSuccess is the "a single blip recovers in seconds"
// half: the backoff must not stay at the cap once a connection succeeds.
func TestFailureCounterResetsAfterSuccess(t *testing.T) {
	var counter failureCounter

	if got := counter.current(); got != 0 {
		t.Fatalf("fresh counter reports %d failures, want 0", got)
	}
	for i := 1; i <= 6; i++ {
		if got := counter.fail(); got != i {
			t.Fatalf("fail() = %d, want %d", got, i)
		}
	}
	if got := reconnectBackoff(counter.current() + 1); got < reconnectMaxDelay-reconnectMaxDelay/4 {
		t.Fatalf("after six failures the next wait is %v, expected near the cap", got)
	}

	counter.reset()
	if got := counter.current(); got != 0 {
		t.Fatalf("after reset the counter reports %d failures, want 0", got)
	}
	if got := reconnectBackoff(counter.current() + 1); got > reconnectBaseDelay+reconnectBaseDelay/4 {
		t.Fatalf("after a success the next wait is %v, expected to start over at ~%v",
			got, reconnectBaseDelay)
	}
}
