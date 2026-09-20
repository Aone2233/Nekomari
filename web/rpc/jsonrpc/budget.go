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

var querySlots = make(chan struct{}, 4)
var rpcConnections = make(chan struct{}, 256)

func expensiveQuery(method string) bool {
	switch method {
	case "public:queryMetrics", "public:getPingMetricStats", "public:getRecordsByUUID", "public:getPingRecords", "admin:dbQuery":
		return true
	}
	return false
}

func queryBudget(ctx context.Context, method string) (context.Context, func(), *rpc.JsonRpcError) {
	if !expensiveQuery(method) {
		return ctx, func() {}, nil
	}
	select {
	case querySlots <- struct{}{}:
	default:
		return ctx, nil, rpc.MakeError(rpc.InvalidParams, "query capacity reached; retry later", nil)
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	return metric.WithReadBudget(bounded, maxMetricTotalPoints), func() { cancel(); <-querySlots }, nil
}

func validateMetricWindow(start, end time.Time) error {
	if !end.After(start) || end.Sub(start) > 366*24*time.Hour {
		return fmt.Errorf("query window must be positive and at most 366 days")
	}
	return nil
}
