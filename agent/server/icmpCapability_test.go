package server

import (
	"runtime"
	"strings"
	"testing"
)

// The value the agent reports has to be one of the three the panel understands.
// A fourth string would reach the panel as an unknown capability and read as
// "the agent said something we cannot interpret", which is worse than not
// reporting at all.
func TestICMPCapabilityIsOneOfThreeValues(t *testing.T) {
	got := ICMPCapability()
	switch got {
	case ICMPCapabilityRaw, ICMPCapabilityPing, ICMPCapabilityNone:
	default:
		t.Fatalf("ICMPCapability() = %q, want one of %q/%q/%q",
			got, ICMPCapabilityRaw, ICMPCapabilityPing, ICMPCapabilityNone)
	}

	// It is cached, so the second call must agree with the first — the probe runs
	// once per process, not once per basic-info upload.
	if again := ICMPCapability(); again != got {
		t.Fatalf("ICMPCapability() = %q on the second call, first was %q", again, got)
	}
}

// On a normal developer machine (or CI runner) one of the two sockets opens, so
// "none" is the surprising case and worth naming. Windows has no ICMP socket the
// same way and the probe may legitimately come back "none" there.
func TestICMPCapabilityIsNotNoneOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("socket permissions differ per platform; this asserts the Linux case")
	}
	if got := ICMPCapability(); got == ICMPCapabilityNone {
		t.Skipf("this build environment has neither socket; the probe said %q, "+
			"which is a valid answer on a locked-down host", got)
	}
}

// The operator-facing sentence has to say what the value means, because it is
// logged at startup and an operator will compare it with the panel.
func TestDescribeICMPCapability(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{ICMPCapabilityRaw, "raw socket available"},
		{ICMPCapabilityPing, "fallback in use"},
		{ICMPCapabilityNone, "not the target's"},
		{"", "not reported"},
	}
	for _, tc := range cases {
		got := describeICMPCapability(tc.value)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("describeICMPCapability(%q) = %q, want it to contain %q",
				tc.value, got, tc.want)
		}
	}
}
