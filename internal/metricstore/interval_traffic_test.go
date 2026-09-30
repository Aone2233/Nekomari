package metricstore

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/metric"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestIntervalTrafficIsAdditiveAcrossBuckets(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	for i, sec := range []int{5, 55, 65, 115} {
		_, err := WriteReport(ctx, v2.Report{UUID: "interval-test", UpdatedAt: base.Add(time.Duration(sec) * time.Second), CounterEpoch: "epoch-a", SampleIntervalSeconds: 20, Uptime: int64(100 + sec), Network: v2.NetworkReport{TotalUp: int64(100 + i*50), TotalDown: int64(200 + i*50)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, interval := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, time.Hour} {
		points, err := s.Series(ctx, metric.AggregateQuery{Query: metric.Query{MetricName: MetricTrafficIntervalUp, EntityID: "interval-test", Start: base, End: base.Add(2 * time.Minute)}, Aggregation: metric.AggSum, Interval: interval}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		sum := 0.0
		for _, p := range points {
			sum += p.Value
		}
		if math.Abs(sum-150) > .001 {
			t.Fatalf("%s sum = %v, want 150", interval, sum)
		}
	}
}

func TestIntervalTrafficRejectsDiscontinuities(t *testing.T) {
	base := time.Now().UTC()
	previous := reportTrafficValues{timestamp: base, totalUp: 900, totalDown: 900, hasUp: true, hasDown: true, epoch: "a", uptime: 100}
	good := v2.Report{UUID: "node", UpdatedAt: base.Add(5 * time.Second), CounterEpoch: "a", SampleIntervalSeconds: 5, Uptime: 105, Network: v2.NetworkReport{TotalUp: 1000, TotalDown: 1000}}
	for _, name := range []string{"valid", "first", "epoch", "missing_epoch", "gap", "reset", "reboot", "failure"} {
		t.Run(name, func(t *testing.T) {
			r, p := good, previous
			switch name {
			case "first":
				p.timestamp = time.Time{}
			case "epoch":
				r.CounterEpoch = "b"
			case "missing_epoch":
				r.CounterEpoch = ""
			case "gap":
				r.UpdatedAt = base.Add(time.Minute)
			case "reset":
				r.Network.TotalUp = 100
			case "reboot":
				r.Uptime = 5
			case "failure":
				r.Quality = map[string]string{"network": "unknown"}
			}
			points := reportIntervalPoints(r, p)
			if name == "valid" {
				if len(points) != 3 || points[0].Value != 100 || points[2].Value != 1 {
					t.Fatalf("%+v", points)
				}
			} else if len(points) != 1 || points[0].Value != 0 {
				t.Fatalf("invented traffic: %+v", points)
			}
		})
	}
}

func TestIntervalTrafficReplayRetryCycleAndServerRestart(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	report := func(seconds int, counter, cycle int64) v2.Report {
		return v2.Report{UUID: "continuity", UpdatedAt: base.Add(time.Duration(seconds) * time.Second), CounterEpoch: "epoch", SampleIntervalSeconds: 5, Uptime: int64(100 + seconds), Network: v2.NetworkReport{TotalUp: counter, TotalDown: counter, CycleUp: cycle, CycleDown: cycle}}
	}
	write := func(r v2.Report) {
		t.Helper()
		if _, err := writeReportBatch(ctx, []v2.Report{r}); err != nil {
			t.Fatal(err)
		}
	}
	write(report(5, 1000, 900))
	write(report(10, 1010, 910))
	write(report(10, 1010, 910))
	write(report(7, 1004, 904))
	failed := report(15, 1020, 920)
	failed.CPU.Usage = math.NaN()
	if _, err := writeReportBatch(ctx, []v2.Report{failed}); err == nil {
		t.Fatal("invalid batch unexpectedly succeeded")
	}
	write(report(15, 1020, 920))
	write(report(20, 1030, 5)) // Billing rollover must not reset kernel continuity.
	clearReportTrafficStates()
	write(report(25, 1040, 15)) // Server restart has no durable epoch baseline.
	write(report(30, 1050, 25))
	points, err := s.Series(ctx, metric.AggregateQuery{Query: metric.Query{MetricName: MetricTrafficIntervalUp, EntityID: "continuity", Start: base, End: base.Add(time.Minute)}, Aggregation: metric.AggSum, Interval: time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Value != 40 || points[0].Count != 4 {
		t.Fatalf("replay/retry/cycle/restart invented or lost deltas: %+v", points)
	}
}
