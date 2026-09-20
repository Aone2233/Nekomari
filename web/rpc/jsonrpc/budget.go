package jsonrpc

import (
	"context"
	"fmt"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"time"
)

const maxMetricPoints = 4096
const maxMetricEntities = 256
const maxMetricKeys = 32
const maxMetricTotalPoints = 250000

// Historical-query admission.
//
// The pool used to reject the fifth concurrent request outright. The built-in UI
// no longer replays a failed call over HTTP (see docs/RESOURCE-HARDENING.md), so
// that rejection was simply a chart that did not load for whoever lost the race.
// A request now waits for a slot instead, bounded so the wait cannot outlive the
// caller: the queue wait plus the work deadline stay under the 30-second context
// the RPC WebSocket transport puts around each request. Over HTTP the caller's own
// context applies and the same two bounds still cap the work.
const (
	querySlotsCapacity = 4
	queryQueueWait     = 10 * time.Second
	queryWorkDeadline  = 15 * time.Second

	// The administrator SQL console is deliberately outside the public chart pool.
	// Sharing it meant an admin query during an incident queued behind four public
	// charts, and four public charts queued behind an admin query. It also carries
	// no metric read budget: it runs raw SQL against arbitrary tables, not metric
	// rollups, so "narrow the metric query" would be the wrong instruction.
	adminQuerySlotsCapacity = 2
	adminQueryQueueWait     = 5 * time.Second
	adminQueryWorkDeadline  = 20 * time.Second
)

var querySlots = make(chan struct{}, querySlotsCapacity)
var adminQuerySlots = make(chan struct{}, adminQuerySlotsCapacity)
var rpcConnections = make(chan struct{}, 256)

type queryClass int

const (
	queryUnbudgeted queryClass = iota
	queryMetricHistory
	queryAdminConsole
)

func classifyQuery(method string) queryClass {
	switch method {
	case "public:queryMetrics", "public:getPingMetricStats", "public:getRecordsByUUID", "public:getPingRecords":
		return queryMetricHistory
	case "admin:dbQuery":
		return queryAdminConsole
	}
	return queryUnbudgeted
}

// queryBudget admits an expensive query and returns the context its work runs
// under plus the release function. Methods outside both pools get the request
// context unchanged and a no-op release.
func queryBudget(ctx context.Context, method string) (context.Context, func(), *rpc.JsonRpcError) {
	class := classifyQuery(method)
	if class == queryUnbudgeted {
		return ctx, func() {}, nil
	}
	slots, wait, deadline := querySlots, queryQueueWait, queryWorkDeadline
	if class == queryAdminConsole {
		slots, wait, deadline = adminQuerySlots, adminQueryQueueWait, adminQueryWorkDeadline
	}
	if err := acquireQuerySlot(ctx, slots, wait); err != nil {
		// A no-op release, so a caller that defers it unconditionally cannot panic
		// even though it should have checked the error first.
		return ctx, func() {}, rpc.MakeError(rpc.Unavailable, "query capacity is saturated; retry later", nil)
	}
	bounded, cancel := context.WithTimeout(ctx, deadline)
	release := func() { cancel(); <-slots }
	if class == queryAdminConsole {
		return bounded, release, nil
	}
	return metric.WithReadBudget(bounded, maxMetricTotalPoints), release, nil
}

// acquireQuerySlot takes a slot immediately when one is free and otherwise waits
// up to wait for one. A saturated pool is a delay, not an error, until the wait
// expires or the caller goes away.
func acquireQuerySlot(ctx context.Context, slots chan struct{}, wait time.Duration) error {
	select {
	case slots <- struct{}{}:
		return nil
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return nil
	case <-timer.C:
		return fmt.Errorf("query queue wait of %s expired", wait)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validateMetricWindow(start, end time.Time) error {
	if !end.After(start) || end.Sub(start) > 366*24*time.Hour {
		return fmt.Errorf("query window must be positive and at most 366 days")
	}
	return nil
}
