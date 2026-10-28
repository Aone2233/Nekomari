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

func TestUptimeQueryAggregationAndVisibility(t *testing.T) {
	const visible, hidden, deleted = "uptime-visible", "uptime-hidden", "uptime-deleted"
	seedVisibilityClients(t, visible, hidden)
	t.Cleanup(func() {
		_ = dbcore.GetDBInstance().Where("uuid IN ?", []string{visible, hidden}).Delete(&models.Client{}).Error
		dbcache.InvalidateAll()
	})
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1), metric.WithRollupPolicy(metric.RollupPolicy{
		RawRetention: 10 * time.Minute, Tiers: []metric.RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}, Compression: 30,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, metric.Definition{Name: metricstore.MetricUptime, Type: metric.TypeGauge, Unit: "s", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	for _, id := range []string{visible, hidden, deleted} {
		for i, value := range []float64{100, 103, 2, 5} {
			if err := s.Write(ctx, metric.Point{MetricName: metricstore.MetricUptime, EntityID: id,
				Timestamp: base.Add(time.Duration(i) * time.Second), Value: value}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.Compact(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	start, end := base.Add(-time.Minute), base.Add(time.Minute)
	params := publicMetricQueryParams{MetricKeys: []string{metricstore.MetricUptime}, EntityIDs: []string{visible, hidden, deleted}, Start: &start, End: &end}
	query := func(c context.Context, p publicMetricQueryParams) []publicMetricSeries {
		t.Helper()
		response, rpcErr := publicQueryMetricsWithStore(c, p, s)
		if rpcErr != nil {
			t.Fatalf("query failed: %+v", rpcErr)
		}
		return response.(map[string]any)["series"].([]publicMetricSeries)
	}
	for _, principal := range []*rpc.Principal{rpc.NewAnonymousPrincipal(), rpc.NewScopedAPIKeyPrincipal("key", []string{rpc.ScopeTasksExec})} {
		guest := rpc.NewContextWithMeta(ctx, &rpc.ContextMeta{Principal: principal})
		got := query(guest, params)
		if len(got) != 1 || got[0].EntityID != visible {
			t.Fatalf("unprivileged history = %+v", got)
		}
	}
	admin := query(adminContext(), params)
	if len(admin) != 2 {
		t.Fatalf("admin should see existing nodes only: %+v", admin)
	}
	for _, series := range admin {
		if series.Semantics != "node_uptime_seconds;not_reboot_count" || series.Quality != "positive_ok_or_legacy;zero_missing_unknown" ||
			series.DownsampleAlgorithm != "last" || len(series.Points) != 1 || *series.Points[0].Value != 5 {
			t.Fatalf("uptime semantics or last sample changed: %+v", series)
		}
	}
	params.Aggregation = "max"
	if got := query(adminContext(), params); *got[0].Points[0].Value != 103 {
		t.Fatalf("explicit aggregation was not respected: %+v", got)
	}
	params.AggregationByMetric = map[string]string{metricstore.MetricUptime: "last"}
	if got := query(adminContext(), params); *got[0].Points[0].Value != 5 {
		t.Fatalf("per-metric override was not respected: %+v", got)
	}
}
