//go:build linux && (amd64 || arm64)

package server

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestICMPTimestampClockValidation(t *testing.T) {
	start := time.Now()
	end := start.Add(20 * time.Millisecond)
	for _, tc := range []struct {
		name   string
		end    time.Time
		tx, rx int64
		valid  bool
	}{
		{"normal", end, start.UnixNano() + int64(time.Millisecond), start.UnixNano() + int64(10*time.Millisecond), true},
		{"backward", end, start.UnixNano() + int64(10*time.Millisecond), start.UnixNano() + int64(time.Millisecond), false},
		{"before_send", end, start.UnixNano() - int64(5*time.Millisecond), start.UnixNano() + int64(time.Millisecond), false},
		{"after_finish", end, start.UnixNano(), end.UnixNano() + int64(5*time.Millisecond), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkedICMPInterval(start, tc.end, tc.tx, tc.rx)
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if _, err := validateICMPInterval(start.UnixNano(), end.UnixNano()+int64(time.Second), 20*time.Millisecond, start.UnixNano(), end.UnixNano()); err == nil {
		t.Fatal("wall clock jump accepted")
	}
}
func TestICMPTimestampLoopback(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1"} {
		for _, raw := range []bool{false, true} {
			t.Run(ip+map[bool]string{true: "/raw", false: "/ping"}[raw], func(t *testing.T) {
				v, ok, err, handled := preciseICMP(t.Context(), ip, time.Second, raw)
				if !handled {
					t.Skip("socket or timestamp unsupported")
				}
				if err != nil || !ok || v < 0 {
					t.Fatalf("latency=%d replied=%v err=%v", v, ok, err)
				}
			})
		}
	}
}
func TestICMPTimestampCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err, handled := preciseICMP(ctx, "127.0.0.1", time.Second, false)
	if !handled {
		t.Skip("socket or timestamp unsupported")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestICMPTimestampUnavailableIsNotRetried(t *testing.T) {
	calls := 0
	r := pinnedProbeRunner{resolver: pinnedResolver{lookup: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}}, icmp: func(context.Context, string, time.Duration, bool) (int64, bool, error) {
		calls++
		return -1, false, &timestampProbeError{errors.New("missing timestamp")}
	}}
	out := r.probeICMP(t.Context(), "example.test", "ipv4", time.Second)
	if calls != 1 || out.attempted || !errors.Is(out.err, errPinnedLocalSocket) {
		t.Fatalf("calls=%d outcome=%+v", calls, out)
	}
}

func TestICMPTimestampField(t *testing.T) {
	target := os.Getenv("NEKOMARI_ICMP_TIMESTAMP_TARGET")
	if target == "" {
		t.Skip("no isolated calibration target")
	}
	for i := 0; i < 30; i++ {
		latency, family, err := icmpPing(target, time.Second)
		if err != nil {
			t.Fatalf("sample=%d: %v", i, err)
		}
		fmt.Printf("ICMP_TIMESTAMP_SAMPLE index=%d rtt_us=%d family=%s\n", i, latency, family)
		time.Sleep(100 * time.Millisecond)
	}
}

func TestICMPTimestampNoReply(t *testing.T) {
	target := os.Getenv("NEKOMARI_ICMP_NO_REPLY")
	if target == "" {
		t.Skip("no isolated silent target")
	}
	for _, raw := range []bool{true, false} {
		start := time.Now()
		_, replied, err, handled := preciseICMP(t.Context(), target, 200*time.Millisecond, raw)
		if !handled || err != nil || replied {
			t.Fatalf("raw=%v handled=%v replied=%v err=%v", raw, handled, replied, err)
		}
		if time.Since(start) < 180*time.Millisecond {
			t.Fatalf("raw=%v timeout returned early", raw)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	start := time.Now()
	_, _, err, handled := preciseICMP(ctx, target, 5*time.Second, false)
	if !handled || !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("cancellation: handled=%v err=%v elapsed=%s", handled, err, time.Since(start))
	}
}
