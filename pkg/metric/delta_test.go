package metric

import (
	"testing"
	"time"
)

// AggDelta answers "how much did this cumulative grow", which is the only meaningful reading of a running
// total — and the question `sum` was being misused for.
//
// The measurements that motivated it: the same fleet on the same day gave **3.87 PB** under `sum` and under
// **1 TB** under `delta`. A running total summed over its own samples is its value multiplied by the sample
// count, so the wrong answer is not merely imprecise, it scales with how often it was sampled.

func points(values ...float64) []Point {
	base := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	out := make([]Point, 0, len(values))
	for i, v := range values {
		out = append(out, Point{Timestamp: base.Add(time.Duration(i) * time.Minute), Value: v})
	}
	return out
}

func TestAggDeltaSumsGrowthRatherThanValues(t *testing.T) {
	// A cumulative rising by 1 GB per sample: the growth is 3 GB, while the sum of the values is 10 GB.
	got, err := aggregateValue(points(1e9, 2e9, 3e9, 4e9), AggDelta)
	if err != nil {
		t.Fatalf("aggregateValue: %v", err)
	}
	if got != 3e9 {
		t.Errorf("delta = %g, want %g (the growth). The sum of the values would be %g, which is the figure "+
			"that produced 3.87 PB against a real total under 1 TB.", got, 3e9, 1e10)
	}

	sum, err := aggregateValue(points(1e9, 2e9, 3e9, 4e9), AggSum)
	if err != nil {
		t.Fatalf("aggregateValue(sum): %v", err)
	}
	if sum == got {
		t.Error("delta and sum produced the same value here, so this test cannot tell them apart")
	}
}

func TestAggDeltaTreatsADecreaseAsAReset(t *testing.T) {
	// A billing cycle rolling over: 5 GB, then the counter restarts and climbs to 2 GB.
	//
	// The growth is 5 GB (before the reset) + 2 GB (since it), not 2-5 = -3 GB, and not the whole 7 GB.
	got, err := aggregateValue(points(3e9, 5e9, 1e9, 2e9), AggDelta)
	if err != nil {
		t.Fatalf("aggregateValue: %v", err)
	}
	if got != 2e9+2e9 {
		t.Errorf("delta = %g, want %g: 2 GB up to the reset, then 2 GB after it", got, 4e9)
	}
	if got < 0 {
		t.Error("delta is negative; a reset must contribute the amount since the reset, never a subtraction")
	}
}

func TestAggDeltaNeedsAnInterval(t *testing.T) {
	// One point has no interval, so no growth can be derived. Returning the value would present a cumulative
	// as consumption — the mistake this aggregation exists to prevent, in a quieter form.
	for _, values := range [][]float64{{}, {7e9}} {
		got, err := aggregateValue(points(values...), AggDelta)
		if err != nil {
			t.Fatalf("aggregateValue(%v): %v", values, err)
		}
		if got != 0 {
			t.Errorf("delta over %d points = %g, want 0", len(values), got)
		}
	}
}

// The rollup path must produce the bucket's growth, and must read both ends of the bucket to do it.
func TestRollupBucketDeltaUsesBothEnds(t *testing.T) {
	b := &rollupBucket{count: 2, firstVal: 1000, lastVal: 1750}
	got, ok := b.value(AggDelta)
	if !ok {
		t.Fatal("rollupBucket.value reported AggDelta as underivable")
	}
	if got != 750 {
		t.Errorf("bucket delta = %g, want 750 (last - first)", got)
	}

	// A single sample in the bucket has no interval.
	if v, _ := (&rollupBucket{count: 1, firstVal: 5, lastVal: 5}).value(AggDelta); v != 0 {
		t.Errorf("single-sample bucket delta = %g, want 0", v)
	}

	// A reset inside the bucket.
	if v, _ := (&rollupBucket{count: 3, firstVal: 900, lastVal: 200}).value(AggDelta); v != 200 {
		t.Errorf("bucket delta across a reset = %g, want 200 (the amount since the reset)", v)
	}
}

func TestRollupFieldsForDeltaIncludeBothEnds(t *testing.T) {
	fields := rollupFieldsForAggregations([]Aggregation{AggDelta})
	if fields&rollupReadFirst == 0 || fields&rollupReadLast == 0 {
		t.Errorf("delta requested fields %b, which does not include both first and last; the bucket's growth "+
			"cannot be computed without them", fields)
	}
}
