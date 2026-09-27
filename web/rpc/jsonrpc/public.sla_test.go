package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/rpc"
)

// These cover the decisions the RPC handler makes before any data is read. The
// arithmetic behind the report is tested in internal/sla; what is tested here is that
// the handler asks for the right window and refuses the wrong ones.

// base is a fixed instant so the expected boundaries are readable.
var base = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func TestResolveSlaWindowPresets(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		window string
		want   time.Duration
		label  string
	}{
		{"", 24 * time.Hour, "24h"},
		{"24h", 24 * time.Hour, "24h"},
		{"7d", 7 * 24 * time.Hour, "7d"},
		{"30d", 30 * 24 * time.Hour, "30d"},
		{"90d", 90 * 24 * time.Hour, "90d"},
	}
	for _, tc := range cases {
		t.Run("window="+tc.window, func(t *testing.T) {
			window, label, _, rpcErr := resolveSlaWindow(ctx, publicSlaReportParams{Window: tc.window}, base, base)
			if rpcErr != nil {
				t.Fatalf("unexpected error: %+v", rpcErr)
			}
			if window != tc.want {
				t.Fatalf("window = %s, want %s", window, tc.want)
			}
			if label != tc.label {
				t.Fatalf("label = %q, want %q", label, tc.label)
			}
		})
	}
}

// A window the server does not offer is refused rather than silently treated as the
// default, which would answer a different question than the one asked.
func TestResolveSlaWindowRejectsUnknownPresets(t *testing.T) {
	_, _, _, rpcErr := resolveSlaWindow(context.Background(),
		publicSlaReportParams{Window: "1y"}, base, base)
	if rpcErr == nil {
		t.Fatal("an unknown window must be refused")
	}
	if rpcErr.Code != rpc.InvalidParams {
		t.Fatalf("code = %v, want InvalidParams", rpcErr.Code)
	}
}

// An explicit range wins over a preset, and a range that ends before it starts is
// refused rather than producing a negative window.
func TestResolveSlaWindowCustomRange(t *testing.T) {
	start := base.Add(-6 * time.Hour)
	window, label, _, rpcErr := resolveSlaWindow(context.Background(), publicSlaReportParams{
		Start: &start, End: &base, Window: "30d",
	}, base, base)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	if window != 6*time.Hour {
		t.Fatalf("window = %s, want 6h: an explicit range must win over the preset", window)
	}
	if label != "custom" {
		t.Fatalf("label = %q, want custom", label)
	}

	earlier := base.Add(-time.Hour)
	if _, _, _, rpcErr := resolveSlaWindow(context.Background(), publicSlaReportParams{
		Start: &base, End: &earlier,
	}, base, base); rpcErr == nil {
		t.Fatal("an end before the start must be refused")
	}
}

// Hours is the escape hatch for a caller that wants neither a preset nor an explicit
// range, and it is labelled custom because it is not one of the offered windows.
func TestResolveSlaWindowHours(t *testing.T) {
	window, label, _, rpcErr := resolveSlaWindow(context.Background(),
		publicSlaReportParams{Hours: 12}, base, base)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	if window != 12*time.Hour {
		t.Fatalf("window = %s, want 12h", window)
	}
	if label != "custom" {
		t.Fatalf("label = %q, want custom", label)
	}
}

// The bucket width is chosen so a long window does not ask for more buckets than the
// store will serve, and it never goes below the store's finest tier.
func TestSlaBucketInterval(t *testing.T) {
	cases := []struct {
		window time.Duration
		want   time.Duration
	}{
		{time.Hour, time.Minute},
		{24 * time.Hour, 5 * time.Minute}, // 288 buckets, under the target
		{7 * 24 * time.Hour, time.Hour},   // 168
		{30 * 24 * time.Hour, 6 * time.Hour},
		{90 * 24 * time.Hour, 6 * time.Hour},
		{0, time.Minute},
	}
	for _, tc := range cases {
		if got := slaBucketInterval(tc.window); got != tc.want {
			t.Errorf("slaBucketInterval(%s) = %s, want %s", tc.window, got, tc.want)
		}
	}
}

// Every window a caller can ask for must produce at most the bucket target, which is
// what keeps a 90-day report from asking the store for 129 600 buckets per series.
func TestEveryPresetStaysWithinTheBucketTarget(t *testing.T) {
	for label, window := range slaWindowPresets {
		interval := slaBucketInterval(window)
		if buckets := int(window / interval); buckets > slaBucketTarget {
			t.Errorf("%s at %s is %d buckets, over the %d target",
				label, interval, buckets, slaBucketTarget)
		}
		if interval < time.Minute {
			t.Errorf("%s produced a sub-minute interval %s", label, interval)
		}
	}
}

// With no store there is no retention to clamp against, and a report must still be
// answerable rather than refused because an optional read failed.
func TestRetentionWindowWithoutAStoreIsZero(t *testing.T) {
	if got := metricRetentionWindow(context.Background()); got != 0 {
		t.Fatalf("retention = %s, want 0 when no store is configured", got)
	}
	// And a zero retention must not clamp anything.
	window, _, reason, rpcErr := resolveSlaWindow(context.Background(),
		publicSlaReportParams{Window: "90d"}, base, base)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	if window != 90*24*time.Hour {
		t.Fatalf("window = %s, want 90d unclamped", window)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty when nothing was clamped", reason)
	}
}
