package metric

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadBudgetAppliesBeforeRawAndRollupMaterialization(t *testing.T) {
	s := newMemStore(t)
	ctx := context.Background()
	if err := s.CreateMetric(ctx, Definition{Name: "budget", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		if err := s.Write(ctx, Point{MetricName: "budget", EntityID: "node", Timestamp: now.Add(-time.Duration(i) * time.Minute), Value: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	query := Query{MetricName: "budget", Start: now.Add(-5 * time.Minute), End: now}
	if _, err := s.Query(WithReadBudget(ctx, 2), query); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("raw budget: %v", err)
	}
	if points, err := s.Query(WithReadBudget(ctx, 100), query); err != nil || len(points) != 4 {
		t.Fatalf("bounded raw read: %d %v", len(points), err)
	}
	if _, err := s.Series(WithReadBudget(ctx, 2), AggregateQuery{Query: query, Aggregation: AggAvg, Interval: time.Minute}, now); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("rollup budget: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Query(cancelled, query); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query: %v", err)
	}
}

// A corrupt row must be reported as a scan failure, not as an exhausted read
// budget: "narrow the query" is the wrong instruction for data that cannot be
// decoded, and the budget error used to win because it was checked first.
func TestScanFailureOutranksTheReadBudget(t *testing.T) {
	corrupt := errors.New("corrupt rollup row")
	if err := readBudgetAfterScan(WithReadBudget(context.Background(), 1), corrupt); !errors.Is(err, corrupt) {
		t.Fatalf("scan failure reported as %v", err)
	}
	if err := readBudgetAfterScan(WithReadBudget(context.Background(), 0), nil); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("exhausted budget not reported: %v", err)
	}
	if err := readBudgetAfterScan(WithReadBudget(context.Background(), 1), nil); err != nil {
		t.Fatalf("healthy row rejected: %v", err)
	}
}
