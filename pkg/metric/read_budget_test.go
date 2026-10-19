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

// spendRows charges n rows and fails the test when the budget rejects them.
func spendRows(t *testing.T, ctx context.Context, n int) {
	t.Helper()
	if err := spendReadBudget(ctx, n); err != nil {
		t.Fatalf("spend %d rows: %v", n, err)
	}
}

// TestReadBudgetByteDimensionBoundsManySmallRows verifies that a byte ceiling
// stops a read that would stay inside its row allowance: the row budget alone
// cannot bound the accumulator memory a query materializes.
func TestReadBudgetByteDimensionBoundsManySmallRows(t *testing.T) {
	s := newMemStore(t)
	ctx := context.Background()
	if err := s.CreateMetric(ctx, Definition{Name: "byte-budget", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		if err := s.Write(ctx, Point{MetricName: "byte-budget", EntityID: "node", Timestamp: now.Add(-time.Duration(i) * time.Minute), Value: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	query := Query{MetricName: "byte-budget", Start: now.Add(-5 * time.Minute), End: now}

	// Generous rows, room for only three of the four rows' worth of bytes.
	if _, err := s.Query(WithReadBudgetBytes(ctx, 1000, 3*estimatedBytesPerRow), query); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("byte budget: %v", err)
	}
	// Exactly the bytes of the four returned rows still fit.
	points, err := s.Query(WithReadBudgetBytes(ctx, 1000, 4*estimatedBytesPerRow), query)
	if err != nil || len(points) != 4 {
		t.Fatalf("bounded byte read: %d %v", len(points), err)
	}
	// maxBytes = 0 keeps the byte dimension disabled.
	points, err = s.Query(WithReadBudgetBytes(ctx, 1000, 0), query)
	if err != nil || len(points) != 4 {
		t.Fatalf("disabled byte dimension: %d %v", len(points), err)
	}
}

// TestReadBudgetRowDimensionStillBinds verifies the row ceiling still stops a
// read when the byte allowance is generous.
func TestReadBudgetRowDimensionStillBinds(t *testing.T) {
	s := newMemStore(t)
	ctx := context.Background()
	if err := s.CreateMetric(ctx, Definition{Name: "row-budget", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		if err := s.Write(ctx, Point{MetricName: "row-budget", EntityID: "node", Timestamp: now.Add(-time.Duration(i) * time.Minute), Value: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	query := Query{MetricName: "row-budget", Start: now.Add(-5 * time.Minute), End: now}
	if _, err := s.Query(WithReadBudgetBytes(ctx, 2, 1<<30), query); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("row budget: %v", err)
	}
	if points, err := s.Query(WithReadBudgetBytes(ctx, 4, 1<<30), query); err != nil || len(points) != 4 {
		t.Fatalf("bounded row read: %d %v", len(points), err)
	}
}

// TestReadBudgetSpendAccounting pins the accounting of both dimensions,
// including that a zero-row progress check never charges bytes.
func TestReadBudgetSpendAccounting(t *testing.T) {
	byBytes := WithReadBudgetBytes(context.Background(), 1000, 3*estimatedBytesPerRow)
	for i := 0; i < 3; i++ {
		spendRows(t, byBytes, 1)
	}
	if err := spendReadBudget(byBytes, 0); err != nil {
		t.Fatalf("zero-row progress check charged the byte budget: %v", err)
	}
	if err := spendReadBudget(byBytes, 1); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("exhausted byte budget reported %v", err)
	}

	byRows := WithReadBudgetBytes(context.Background(), 2, 0)
	spendRows(t, byRows, 2)
	if err := spendReadBudget(byRows, 1); !errors.Is(err, ErrReadBudget) {
		t.Fatalf("exhausted row budget reported %v", err)
	}

	// A byte budget large enough for n rows must not reject n rows: only
	// spending past the allowance is an error.
	spendRows(t, WithReadBudgetBytes(context.Background(), 1000, 2*estimatedBytesPerRow), 2)
}

// TestReadBudgetCancellationOutranksByteBudget verifies the caller's
// cancellation keeps priority over both budget dimensions.
func TestReadBudgetCancellationOutranksByteBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := spendReadBudget(WithReadBudgetBytes(ctx, 0, 0), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request reported %v", err)
	}
}
