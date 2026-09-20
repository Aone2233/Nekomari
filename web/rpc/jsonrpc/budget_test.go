package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/rpc"
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
	if _, done, err := queryBudget(context.Background(), "common:getNodesLatestStatus"); err != nil {
		t.Fatal("live status blocked by chart queries")
	} else {
		done()
	}
}

// holdChartSlots takes every public chart slot and returns one release per slot.
func holdChartSlots(t *testing.T) []func() {
	t.Helper()
	releases := make([]func(), 0, cap(querySlots))
	for i := 0; i < cap(querySlots); i++ {
		ctx, done, err := queryBudget(context.Background(), "public:queryMetrics")
		if err != nil {
			for _, release := range releases {
				release()
			}
			t.Fatalf("could not hold chart slot %d: %v", i, err)
		}
		if _, ok := ctx.Deadline(); !ok {
			done()
			t.Fatal("missing deadline")
		}
		releases = append(releases, done)
	}
	return releases
}

func releaseAll(releases []func()) {
	for _, release := range releases {
		release()
	}
}

// A saturated chart pool must queue the next request instead of answering
// "capacity reached" at once: the built-in UI has no HTTP replay, so an immediate
// rejection is a chart that never loads.
func TestChartQueriesQueueWhileThePoolIsFull(t *testing.T) {
	releases := holdChartSlots(t)
	// The closure reads releases when it runs: `defer releaseAll(releases)` would
	// capture the four-element slice and double-release the one freed below.
	defer func() { releaseAll(releases) }()

	admitted := make(chan *rpc.JsonRpcError, 1)
	go func() {
		// The slot is released inside the goroutine, so no test outcome can leak it
		// into the next test that shares this package-level pool.
		_, done, err := queryBudget(context.Background(), "public:queryMetrics")
		if err == nil {
			done()
		}
		admitted <- err
	}()

	select {
	case err := <-admitted:
		t.Fatalf("fifth query returned before a slot was free: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	releases[0]()
	releases = releases[1:]
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("queued query was rejected after a slot freed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued query never acquired a slot")
	}
}

func TestAcquireQuerySlotReportsSaturation(t *testing.T) {
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	if err := acquireQuerySlot(context.Background(), slots, 50*time.Millisecond); err == nil {
		t.Fatal("saturated pool admitted a caller")
	}
	<-slots
	if err := acquireQuerySlot(context.Background(), slots, time.Second); err != nil {
		t.Fatalf("free slot rejected: %v", err)
	}
	<-slots
}

// The admin SQL console must not consume a public chart slot or inherit the
// 15-second public deadline.
func TestAdminQueryBudgetIsIsolatedFromCharts(t *testing.T) {
	releases := holdChartSlots(t)
	defer func() { releaseAll(releases) }()

	ctx, done, err := queryBudget(context.Background(), "admin:dbQuery")
	if err != nil {
		t.Fatalf("admin query blocked by chart queries: %v", err)
	}
	defer done()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("admin query has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= queryWorkDeadline {
		t.Fatalf("admin query inherited the public %s deadline: %s left", queryWorkDeadline, remaining)
	}
}
