package netstatic

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// pinLinkSpeed 替换 sysfs seam，让用例不依赖跑测试的机器的真实网卡速率。
func pinLinkSpeed(t *testing.T, mbps int, err error) {
	t.Helper()
	old := linkSpeedMbps
	linkSpeedMbps = func(string) (int, error) { return mbps, err }
	t.Cleanup(func() { linkSpeedMbps = old })
}

// sevenGiBInAMinute is just over a 1 Gbps link (≈1002 Mbps) and comfortably
// inside 10 Gbps, which is exactly the case that used to be thrown away on fast
// nodes: the guard assumed 1 Gbps for everyone.
const sevenGiBInAMinute = uint64(7 * 1024 * 1024 * 1024)

// A measured link rate, not a constant, decides whether a delta is possible.
func TestMeasuredLinkSpeedSetsTheDeltaCeiling(t *testing.T) {
	cases := []struct {
		name string
		mbps int
		want bool
		// rateText is what the rejection log must name, so an operator can tell a
		// real 1 Gbps limit from an unreadable sysfs attribute.
		rateText string
	}{
		{"sixty six hundred", 6600, true, ""},
		{"ten gigabit", 10000, true, ""},
		{"two and a half gigabit", 2500, true, ""},
		{"one gigabit", 1000, false, "1 Gbps"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinLinkSpeed(t, tc.mbps, nil)
			ok, reason := plausibleDelta("eth0", sevenGiBInAMinute, 0, time.Minute)
			if ok != tc.want {
				t.Fatalf("%d Mbps: plausibleDelta = %v (%s), want %v", tc.mbps, ok, reason, tc.want)
			}
			if tc.want {
				return
			}
			if reason == "" {
				t.Fatal("a rejected delta must say why")
			}
			for _, want := range []string{tc.rateText, "measured"} {
				if want == "" {
					continue
				}
				if !strings.Contains(reason, want) {
					t.Fatalf("reason %q does not mention %q", reason, want)
				}
			}
		})
	}
}

// An unreadable speed attribute (bridges, virtual devices, non-Linux) must keep
// the historical 1 Gbps assumption *with* its burst allowance, and the log has to
// say the rate was assumed rather than measured.
func TestUnreadableLinkSpeedFallsBackToTheAssumedRate(t *testing.T) {
	pinLinkSpeed(t, 0, errors.New("/sys/class/net/br0/speed: no such file or directory"))

	if ok, reason := plausibleDelta("br0", sevenGiBInAMinute, 0, time.Minute); !ok {
		t.Fatalf("the assumed 1 Gbps rate with its burst allowance must still allow 7 GiB/min, got: %s", reason)
	}

	// And the phantom delta that motivated the guard stays rejected.
	ok, reason := plausibleDelta("br0", 59_480*1024*1024, 0, time.Minute)
	if ok {
		t.Fatal("59.5 GB in a minute must stay implausible on an assumed 1 Gbps link")
	}
	if !strings.Contains(reason, "assumed") {
		t.Fatalf("reason %q does not say the rate was assumed", reason)
	}
	if !strings.Contains(reason, "1 Gbps") {
		t.Fatalf("reason %q does not name the assumed rate", reason)
	}
}

// A device that reports a non-positive speed has no usable rate; the fallback
// must be used instead of a zero ceiling (which would reject everything).
func TestNonPositiveLinkSpeedUsesTheAssumedRate(t *testing.T) {
	for _, mbps := range []int{0, -1} {
		pinLinkSpeed(t, mbps, nil)
		rate := linkRateFor("eth0")
		if rate.mbps != 0 {
			t.Fatalf("%d Mbps: rate = %+v, want the assumed rate", mbps, rate)
		}
		if !strings.Contains(rate.String(), "assumed") {
			t.Fatalf("%d Mbps: rate label %q does not say assumed", mbps, rate.String())
		}
		if rate.ceiling(time.Minute) == 0 {
			t.Fatalf("%d Mbps: ceiling = 0, every delta would be rejected", mbps)
		}
	}
}

// The ceiling stays proportional to the window at a measured rate too: a long
// legitimate gap (the agent was stopped) must not have its traffic discarded.
func TestMeasuredRateCeilingGrowsWithElapsedTime(t *testing.T) {
	pinLinkSpeed(t, 10000, nil)
	rate := linkRateFor("eth0")
	short := rate.ceiling(30 * time.Second)
	long := rate.ceiling(10 * time.Minute)
	if long <= short {
		t.Fatalf("ceiling for 10 minutes (%d) should exceed 30 seconds (%d)", long, short)
	}
	if got := rate.ceiling(0); got != 0 {
		t.Fatalf("no elapsed time means no traffic can have happened, got %d", got)
	}
	// Ten times the rate is ten times the ceiling: there is no hidden constant.
	if tenX := linkRateFor("eth0").ceiling(time.Minute); tenX != 10*linkRateFor("eth0").ceiling(6*time.Second) {
		t.Fatalf("ceiling is not linear in elapsed time: %d", tenX)
	}
}

// The sysfs reader is the seam's default, so it has to refuse a name that would
// escape the interface directory.
func TestReadLinkSpeedRejectsMalformedNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../etc", "eth0/../../../etc"} {
		if _, err := readLinkSpeedMbps(name); err == nil {
			t.Fatalf("readLinkSpeedMbps(%q) succeeded", name)
		}
	}
}
