package metricstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func restartReport(uuid string, at time.Time, uptime int64, quality string) v2.Report {
	report := v2.Report{UUID: uuid, UpdatedAt: at.UTC(), Uptime: uptime}
	if quality != "" {
		report.Quality = map[string]string{"uptime": quality}
	}
	return report
}

func latestRestartValues(t *testing.T, s *metric.Store, entityID string) []float64 {
	t.Helper()
	points, err := s.Latest(context.Background(), MetricRestarts, entityID, 64)
	if err != nil {
		t.Fatalf("read restart points: %v", err)
	}
	values := make([]float64, 0, len(points))
	for _, point := range points {
		values = append(values, point.Value)
	}
	return values
}

func TestRestartDetectionCountsLargeUptimeRegressionsOnce(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	uuid := fmt.Sprintf("restart-detect-%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	// The second reboot is only detectable after the post-reboot reading has
	// climbed past the detector floor again.
	sequence := []int64{100000, 100059, 15, 74, 400, 20}
	wantRestartValues := []float64{1, 2}
	for i, uptime := range sequence {
		if _, err := WriteReport(ctx, restartReport(uuid, base.Add(time.Duration(i)*time.Second), uptime, "ok")); err != nil {
			t.Fatal(err)
		}
	}
	assertMetricValues(t, s, MetricRestarts, uuid, base.Add(-time.Second), base.Add(time.Duration(len(sequence))*time.Second), wantRestartValues)
}

func TestRestartDetectionToleratesSmallUptimeJitter(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	uuid := fmt.Sprintf("restart-jitter-%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	// A regression smaller than the floor is reordering/jitter, not a reboot.
	for i, uptime := range []int64{100000, 99900, 99910} {
		if _, err := WriteReport(ctx, restartReport(uuid, base.Add(time.Duration(i)*time.Second), uptime, "ok")); err != nil {
			t.Fatal(err)
		}
	}
	assertMetricValues(t, s, MetricRestarts, uuid, base.Add(-time.Second), base.Add(5*time.Second), nil)
}

func TestRestartDetectionIgnoresInvalidUptimeSamples(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	uuid := fmt.Sprintf("restart-invalid-%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	for i, report := range []v2.Report{
		restartReport(uuid, base, 50000, "ok"),
		restartReport(uuid, base.Add(time.Second), 20, "unknown"),
		restartReport(uuid, base.Add(2*time.Second), 50060, "ok"),
	} {
		if _, err := WriteReport(ctx, report); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	// The invalid sample is neither counted nor used as a baseline, so the
	// following healthy climb reads as normal service, not a reboot.
	assertMetricValues(t, s, MetricRestarts, uuid, base.Add(-time.Second), base.Add(5*time.Second), nil)
}

func TestRestartDetectionSeedsFromStoredHistoryAfterPanelRestart(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	uuid := fmt.Sprintf("restart-seed-%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	if err := s.WriteBatch(ctx, []metric.Point{
		{MetricName: MetricUptime, EntityID: uuid, Timestamp: base.Add(-2 * time.Minute), Value: 86400},
	}); err != nil {
		t.Fatal(err)
	}
	// A fresh detector must read the stored baseline and catch the reboot that
	// happened while the panel itself was restarting.
	if _, err := WriteReport(ctx, restartReport(uuid, base, 45, "ok")); err != nil {
		t.Fatal(err)
	}
	assertMetricValues(t, s, MetricRestarts, uuid, base.Add(-time.Second), base.Add(time.Second), []float64{1})
}
