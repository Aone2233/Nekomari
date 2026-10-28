package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestUptimeQualityAndLegacyReports(t *testing.T) {
	for _, tc := range []struct {
		name    string
		uptime  int64
		quality map[string]string
		want    bool
	}{
		{"explicit ok", 123, map[string]string{"uptime": "ok"}, true},
		{"zero or omitted with claimed ok", 0, map[string]string{"uptime": "ok"}, false},
		{"legacy positive", 456, nil, true},
		{"legacy missing or zero", 0, nil, false},
		{"unknown", 123, map[string]string{"uptime": "unknown"}, false},
		{"empty quality", 123, map[string]string{"uptime": ""}, false},
		{"negative", -1, map[string]string{"uptime": "ok"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := v2.Report{UUID: "node", UpdatedAt: time.Now().UTC(), Uptime: tc.uptime, Quality: tc.quality}
			found := false
			for _, p := range reportMetricPoints(r) {
				if p.MetricName == MetricUptime {
					if found || p.EntityID != r.UUID || !p.Timestamp.Equal(r.UpdatedAt) || p.Value != float64(tc.uptime) {
						t.Fatalf("invalid point: %+v", p)
					}
					found = true
				}
			}
			if found != tc.want {
				t.Fatalf("uptime point present = %t, want %t", found, tc.want)
			}
		})
	}
}

func TestUptimeHistoryRollupRetentionAndDeletion(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	for i, uptime := range []int64{1000, 1003, 2, 5} {
		_, err := WriteReport(ctx, v2.Report{UUID: "uptime-node", UpdatedAt: base.Add(time.Duration(i) * time.Second),
			Uptime: uptime, Quality: map[string]string{"uptime": "ok"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Compact(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	points, err := s.Series(ctx, metric.AggregateQuery{Query: metric.Query{MetricName: MetricUptime,
		EntityID: "uptime-node", Start: base.Add(-time.Minute), End: base.Add(time.Minute)},
		Aggregation: metric.AggLast, Interval: time.Minute}, time.Now().UTC())
	if err != nil || len(points) != 1 || points[0].Value != 5 || points[0].Count != 4 {
		t.Fatalf("last uptime rollup = %+v, err=%v", points, err)
	}
	if err := deleteUndefinedMetrics(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMetric(ctx, MetricUptime); err != nil {
		t.Fatalf("builtin was removed by reclaim: %v", err)
	}
	if _, err := s.SetMetricRetention(ctx, MetricUptime, 0); err != nil {
		t.Fatal(err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatal(err)
	}
	def, err := s.GetMetric(ctx, MetricUptime)
	if err != nil || def.RetentionDays != 0 {
		t.Fatalf("disabled definition not preserved: %+v, %v", def, err)
	}
	if _, err := WriteReport(ctx, v2.Report{UUID: "uptime-node", UpdatedAt: time.Now().UTC(), Uptime: 8}); err != nil {
		t.Fatal(err)
	}
	points, err = s.Series(ctx, metric.AggregateQuery{Query: metric.Query{MetricName: MetricUptime,
		EntityID: "uptime-node", Start: base.Add(-time.Minute), End: time.Now().Add(time.Minute)},
		Aggregation: metric.AggLast, Interval: time.Minute}, time.Now().UTC())
	if err != nil || len(points) != 0 {
		t.Fatalf("disabled metric retained samples: %+v, %v", points, err)
	}
}

func TestUptimeRecordsAreIncludedInSystemHistoryDeletion(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Add(-time.Minute)
	if _, err := WriteReport(ctx, v2.Report{UUID: "delete-uptime", UpdatedAt: base, Uptime: 123}); err != nil {
		t.Fatal(err)
	}
	assertMetricValues(t, s, MetricUptime, "delete-uptime", base.Add(-time.Second), base.Add(time.Second), []float64{123})
	if err := DeleteAllRecords(ctx); err != nil {
		t.Fatal(err)
	}
	assertMetricValues(t, s, MetricUptime, "delete-uptime", base.Add(-time.Second), base.Add(time.Second), nil)
}
