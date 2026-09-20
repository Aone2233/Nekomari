package jsonrpc

import (
	"context"
	"testing"
	"time"
)

func TestMetricQueryBudgets(t *testing.T) {
	for _, n := range []int{-1, 4097, 1000000000} {
		if _, err := resolveMetricMaxPoints("cpu", publicMetricQueryParams{MaxPoints: n}); err == nil {
			t.Fatalf("accepted max_points %d", n)
		}
	}
	now := time.Now()
	if err := validateMetricWindow(now.Add(-367*24*time.Hour), now); err == nil {
		t.Fatal("unbounded window")
	}
	var release []func()
	defer func() {
		for _, done := range release {
			done()
		}
	}()
	for i := 0; i < cap(querySlots); i++ {
		ctx, done, err := queryBudget(context.Background(), "public:queryMetrics")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		release = append(release, done)
	}
	if _, _, err := queryBudget(context.Background(), "public:queryMetrics"); err == nil {
		t.Fatal("unbounded query concurrency")
	}
	if _, done, err := queryBudget(context.Background(), "common:getNodesLatestStatus"); err != nil {
		t.Fatal("live status blocked by chart queries")
	} else {
		done()
	}
}
