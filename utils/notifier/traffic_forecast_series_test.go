package notifier

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
)

// The projection used to read `net.total.up` / `net.total.down` — the kernel counter
// since boot — while the plan limit is written against the cycle-to-date total. The
// old comment even asserted the two were the same quantity. Nothing failed when that
// was wrong, because the tests fed synthetic samples straight into forecast.Compute
// and never looked at which series produced them.
//
// These two tests close that hole from both ends: the constant is pinned, and the
// reader is exercised against a result that carries both series at once.

func TestTheForecastReadsTheCycleSeriesNotTheBootCounter(t *testing.T) {
	if len(cycleTrafficMetricNames) != 2 {
		t.Fatalf("cycleTrafficMetricNames = %v, want exactly the up/down pair", cycleTrafficMetricNames)
	}
	if cycleTrafficMetricNames[0] != metricstore.MetricTrafficUp {
		t.Errorf("cycleTrafficMetricNames[0] = %q, want %q", cycleTrafficMetricNames[0], metricstore.MetricTrafficUp)
	}
	if cycleTrafficMetricNames[1] != metricstore.MetricTrafficDown {
		t.Errorf("cycleTrafficMetricNames[1] = %q, want %q", cycleTrafficMetricNames[1], metricstore.MetricTrafficDown)
	}
	for _, name := range cycleTrafficMetricNames {
		if name == metricstore.MetricNetTotalUp || name == metricstore.MetricNetTotalDown {
			t.Fatalf("%q is the kernel counter since boot; the limit is a per-cycle quota", name)
		}
	}
}

func TestCycleSamplesComeFromTheCycleSeries(t *testing.T) {
	base := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	bucket := func(name string, value float64) metric.AggregatePoint {
		return metric.AggregatePoint{MetricName: name, Bucket: base, Value: value}
	}

	// Both series are present and deliberately disagree: the boot counter is far
	// larger than the cycle total, which is what a node up for longer than one cycle
	// looks like. The samples must be the smaller pair.
	result := metric.BatchSeriesResult{
		Values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
			metricstore.MetricTrafficUp: {
				metric.AggMax: {bucket(metricstore.MetricTrafficUp, 100)},
			},
			metricstore.MetricTrafficDown: {
				metric.AggMax: {bucket(metricstore.MetricTrafficDown, 50)},
			},
			metricstore.MetricNetTotalUp: {
				metric.AggMax: {bucket(metricstore.MetricNetTotalUp, 999999)},
			},
			metricstore.MetricNetTotalDown: {
				metric.AggMax: {bucket(metricstore.MetricNetTotalDown, 888888)},
			},
		},
	}

	samples := cycleSamplesFromSeries("sum", result)
	if len(samples) != 1 {
		t.Fatalf("samples = %#v, want one bucket", samples)
	}
	if samples[0].Total != 150 {
		t.Fatalf("sample total = %d, want 150 (cycle up 100 + cycle down 50); "+
			"a total near 1888887 means the boot counter was read instead", samples[0].Total)
	}

	// 反向：只有开机累计时，投影拿不到任何样本，而不是把开机累计当周期用量。
	bootOnly := metric.BatchSeriesResult{
		Values: map[string]map[metric.Aggregation][]metric.AggregatePoint{
			metricstore.MetricNetTotalUp: {
				metric.AggMax: {bucket(metricstore.MetricNetTotalUp, 999999)},
			},
		},
	}
	if got := cycleSamplesFromSeries("sum", bootOnly); len(got) != 0 {
		t.Fatalf("samples = %#v, want none: the boot counter is not the cycle total", got)
	}
}
