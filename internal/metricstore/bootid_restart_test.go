package metricstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestRestartDetectionUsesBootIDEvenWhenUptimeLooksSane(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	uuid := fmt.Sprintf("restart-bootid-%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	reports := []v2.Report{
		{UUID: uuid, UpdatedAt: base, Uptime: 100000, BootIDHash: "a", Quality: map[string]string{"uptime": "ok"}},
		{UUID: uuid, UpdatedAt: base.Add(time.Second), Uptime: 100001, BootIDHash: "a", Quality: map[string]string{"uptime": "ok"}},
		// A new boot id is a restart even though uptime did not regress.
		{UUID: uuid, UpdatedAt: base.Add(2 * time.Second), Uptime: 5, BootIDHash: "b", Quality: map[string]string{"uptime": "ok"}},
	}
	for _, report := range reports {
		if _, err := WriteReport(ctx, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := FlushReportBatch(ctx); err != nil {
		t.Fatal(err)
	}
	assertMetricValues(t, s, MetricRestarts, uuid, base.Add(-time.Second), base.Add(3*time.Second), []float64{1})
}
