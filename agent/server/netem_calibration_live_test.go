package server

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Operator-provided isolated network only. This test executes the production
// path; independent send and receive evidence must be collected separately.
func TestNetemCalibrationLive(t *testing.T) {
	target := os.Getenv("NEKOMARI_NETEM_TARGET")
	if target == "" {
		t.Skip("no isolated netem target")
	}
	const attempts = 120
	ok := 0
	for i := 0; i < attempts; i++ {
		_, _, err := icmpPing(target, 150*time.Millisecond)
		if err == nil {
			ok++
		} else if reason := unavailableReason(err); reason != "" {
			t.Fatalf("probe unavailable: %s", reason)
		}
	}
	fmt.Printf("NETEM_RESULT attempts=%d received=%d loss=%d\n", attempts, ok, attempts-ok)
}
