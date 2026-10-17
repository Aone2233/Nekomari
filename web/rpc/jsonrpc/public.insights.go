package jsonrpc

import (
	"context"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/unlock"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/web/api/ipinfo"
)

type insightUnlock struct {
	ProbedAt time.Time       `json:"probed_at"`
	Stale    bool            `json:"stale"`
	Results  []v2.UnlockItem `json:"results"`
}
type insightRoutes struct {
	ProbedAt time.Time      `json:"probed_at"`
	Stale    bool           `json:"stale"`
	Results  []v2.RouteItem `json:"results"`
}
type nodeInsights struct {
	Version int                    `json:"version"`
	Unlock  *insightUnlock         `json:"unlock"`
	Routes  *insightRoutes         `json:"routes"`
	IP      *ipinfo.NetworkSummary `json:"ip"`
}

func init() {
	regPublic("getNodeInsights", publicGetNodeInsights, "Read node probe evidence without exposing its egress IP")
}

func publicGetNodeInsights(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID      string `json:"uuid"`
		IncludeIP bool   `json:"include_ip"`
	}
	if err := req.BindParams(&params); err != nil || params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	hidden, err := clientVisibility()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "failed to read node visibility", nil)
	}
	if !clientHistoryReadable(hidden, isLoginFromCtx(ctx), params.UUID) {
		return nil, rpc.MakeError(rpc.InvalidParams, "Node not found", nil)
	}
	report, err := unlock.LoadVisible(params.UUID, isLoginFromCtx(ctx))
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "failed to read node insights", nil)
	}
	response := nodeInsights{Version: 1}
	if report != nil {
		stale := time.Since(report.ProbedAt) > 12*time.Hour || report.ProbedAt.After(time.Now().Add(5*time.Minute))
		items := report.Results
		if items == nil {
			items = []v2.UnlockItem{}
		}
		response.Unlock = &insightUnlock{ProbedAt: report.ProbedAt, Stale: stale, Results: items}
		if len(report.Routes) > 0 {
			routeTime := report.RoutesProbedAt
			if routeTime.IsZero() {
				routeTime = report.ProbedAt
			}
			routeStale := time.Since(routeTime) > 30*time.Hour || routeTime.After(time.Now().Add(5*time.Minute))
			response.Routes = &insightRoutes{ProbedAt: routeTime, Stale: routeStale, Results: report.Routes}
		}
	}
	if params.IncludeIP {
		// Resolve only server-owned addresses after the same visibility gate as history.
		if node, err := clients.GetClientByUUID(params.UUID); err == nil {
			address := node.IPv4
			if address == "" {
				address = node.IPv6
			}
			budget, cancel := context.WithTimeout(ctx, 8*time.Second)
			response.IP, _ = ipinfo.Default().NodeNetworkSummary(budget, address)
			cancel()
		}
	}
	return response, nil
}
