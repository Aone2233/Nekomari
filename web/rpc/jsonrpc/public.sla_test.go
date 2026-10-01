package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
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

// F2/P3-1：请求里没有任何一个可见实体时，报告必须是空的。
//
// 修复前的行为就是线上实测到的那个：实体列表解析为空之后直接交给 store，而 store 把空列表
// 当作「没有过滤条件」，于是 getSlaReport 报出全队 —— 包括唯一那台 hidden=1 的节点
// （实测 count=10）。同族的 getPingMetricStats 一直返回空结果，这里钉住同一个约定。
//
// 同时钉住反向的一半：判空不能把「存在且可见」的节点也吞掉。
func TestPublicSlaReportReturnsNothingWhenNoEntityIsVisible(t *testing.T) {
	const visible = "sla-visible-node"
	const hidden = "sla-hidden-node"

	db := dbcore.GetDBInstance()
	for _, client := range []models.Client{
		{UUID: visible, Name: "visible", Token: "test-only-token-sla-visible"},
		{UUID: hidden, Name: "hidden", Token: "test-only-token-sla-hidden", Hidden: true},
	} {
		if err := db.Create(&client).Error; err != nil {
			t.Fatal(err)
		}
	}
	dbcache.InvalidateAll()

	ctx := context.Background()
	store, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer func() { _ = store.Close() }()
	// The report reads these two series, so define them: an empty window must answer as
	// empty rather than as "unknown metric".
	for _, name := range []string{metricstore.MetricPingLoss, metricstore.MetricPingLatency} {
		if err := store.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}
	// The hidden node does have data. This is what makes the test able to fail before the
	// fix: an empty entity list reaches the reader, the reader applies no filter, and the
	// hidden node is reported. Without a second node's samples there would be nothing to
	// leak and the assertion would pass vacuously.
	if err := store.Write(ctx, metric.Point{
		MetricName: metricstore.MetricPingLoss,
		EntityID:   hidden,
		Timestamp:  time.Now().UTC().Add(-time.Minute),
		Value:      1,
	}); err != nil {
		t.Fatalf("write hidden node sample: %v", err)
	}

	guest := rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{Principal: rpc.NewAnonymousPrincipal()})

	for _, tc := range []struct {
		name   string
		params publicSlaReportParams
	}{
		{"unknown uuid", publicSlaReportParams{UUID: "sla-never-existed", Window: "24h"}},
		{"hidden uuid as guest", publicSlaReportParams{UUID: hidden, Window: "24h"}},
		{"only unknown entity ids", publicSlaReportParams{
			EntityIDs: []string{"sla-never-existed-a", "sla-never-existed-b"}, Window: "24h",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, rpcErr := publicGetSlaReportWithStore(guest, tc.params, store)
			if rpcErr != nil {
				t.Fatalf("unexpected error: %+v", rpcErr)
			}
			report, ok := result.(publicSlaReportResponse)
			if !ok {
				t.Fatalf("result type = %T, want publicSlaReportResponse", result)
			}
			if report.Count != 0 || len(report.Nodes) != 0 {
				t.Fatalf("an empty entity list leaked the fleet: count=%d nodes=%d", report.Count, len(report.Nodes))
			}
			// An empty report is still a report: the window it describes must survive,
			// or a caller cannot tell "nothing to report" from a malformed answer.
			if report.Window != "24h" || report.IntervalSeconds <= 0 || len(report.Presets) == 0 {
				t.Fatalf("the empty answer lost its window metadata: %+v", report)
			}
		})
	}

	t.Run("visible uuid still reports the node", func(t *testing.T) {
		result, rpcErr := publicGetSlaReportWithStore(guest,
			publicSlaReportParams{UUID: visible, Window: "24h"}, store)
		if rpcErr != nil {
			t.Fatalf("unexpected error: %+v", rpcErr)
		}
		report, ok := result.(publicSlaReportResponse)
		if !ok {
			t.Fatalf("result type = %T, want publicSlaReportResponse", result)
		}
		if report.Count != 1 || len(report.Nodes) != 1 || report.Nodes[0].EntityID != visible {
			t.Fatalf("a visible node was dropped by the empty-list guard: %+v", report)
		}
	})
}
