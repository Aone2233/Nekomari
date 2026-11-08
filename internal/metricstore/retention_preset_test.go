package metricstore

import (
	"context"
	"testing"

	"github.com/Aone2233/nekomari/pkg/metric"
)

func TestApplyRetentionPresetUpdatesNodeAndPingOnly(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	previous := store
	store = s
	defer func() { store = previous }()

	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatal(err)
	}
	// A disabled definition must stay disabled: a preset is not a re-enable.
	if _, err := s.SetMetricRetention(ctx, MetricCPU, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMetricRetention(ctx, MetricSwap, 0); err != nil {
		t.Fatal(err)
	}

	updated, err := ApplyRetentionPreset(ctx, 30, 7)
	if err != nil {
		t.Fatal(err)
	}
	if updated == 0 {
		t.Fatal("preset updated no metrics")
	}
	for _, tc := range []struct {
		name string
		want int
	}{
		{MetricCPU, 0}, // explicitly disabled, left alone
		{MetricSwap, 0},
		{MetricRAM, 30},
		{MetricDisk, 30},
		{MetricPingLatency, 7},
		{MetricPingLossPct, 7},
		{MetricHTTPStatus, 7},
		{MetricPanelRSS, defaultBuiltinMetricRetentionDays},
	} {
		def, err := s.GetMetric(ctx, tc.name)
		if err != nil {
			t.Fatalf("get %s: %v", tc.name, err)
		}
		if def.RetentionDays != tc.want {
			t.Errorf("%s retention = %d, want %d", tc.name, def.RetentionDays, tc.want)
		}
	}
}
