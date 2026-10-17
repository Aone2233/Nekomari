package jsonrpc

import (
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/unlock"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

func TestNodeInsightsVisibilityAndRedaction(t *testing.T) {
	const visible, hidden = "mint-visible", "mint-hidden"
	seedVisibilityClients(t, visible, hidden)
	for _, uuid := range []string{visible, hidden} {
		if err := unlock.Save(uuid, v2.UnlockParams{EgressIP: "198.51.100.33", ProbedAt: time.Now().Add(-13 * time.Hour), Results: []v2.UnlockItem{{ID: "chatgpt", Status: "unlocked", Basis: "region"}}, Routes: []v2.RouteItem{{Carrier: "telecom", Target: "1.1.1.1", Label: "CN2"}}}); err != nil {
			t.Fatal(err)
		}
	}
	request := func(uuid string) *rpc.JsonRpcRequest {
		return &rpc.JsonRpcRequest{Params: map[string]any{"uuid": uuid}}
	}
	value, jerr := publicGetNodeInsights(guestContext(), request(visible))
	if jerr != nil {
		t.Fatal(jerr)
	}
	result := value.(nodeInsights)
	if result.Unlock == nil || !result.Unlock.Stale || result.Unlock.Results[0].Basis != "region" || result.Routes == nil {
		t.Fatalf("lost probe evidence: %+v", result)
	}
	if raw := mustMarshalJSON(t, value); strings.Contains(raw, "198.51.100.33") || strings.Contains(raw, "egress") {
		t.Fatalf("egress IP leaked: %s", raw)
	}
	_, hiddenErr := publicGetNodeInsights(guestContext(), request(hidden))
	_, missingErr := publicGetNodeInsights(guestContext(), request("mint-nonexistent"))
	if hiddenErr == nil || missingErr == nil || mustMarshalJSON(t, hiddenErr) != mustMarshalJSON(t, missingErr) {
		t.Fatal("hidden and unknown nodes must be indistinguishable")
	}
	if _, jerr := publicGetNodeInsights(adminContext(), request(hidden)); jerr != nil {
		t.Fatal(jerr)
	}
}

func TestNodeInsightsPrivateSiteGate(t *testing.T) {
	// The extension uses Dispatch, not a standalone public HTTP handler.
	if privateSiteLoginWhitelist["public:getNodeInsights"] {
		t.Fatal("node insights must not be a private-site login exemption")
	}
	previous, _ := config.GetAs[bool](config.PrivateSiteKey)
	if err := config.Set(config.PrivateSiteKey, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set(config.PrivateSiteKey, previous) })
	resp := Dispatch(nil, nil, &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: "public:getNodeInsights", Params: map[string]any{"uuid": "mint-visible"}})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "Private site") {
		t.Fatalf("private node insights accessible: %+v", resp)
	}
}

func TestRouteAndMediaTimestampsStayIndependent(t *testing.T) {
	const uuid = "mint-route-clock"
	seedVisibilityClients(t, uuid, "mint-route-clock-hidden")
	for _, age := range []time.Duration{25 * time.Hour, 31 * time.Hour} {
		routeTime := time.Now().Add(-age)
		if err := unlock.Save(uuid, v2.UnlockParams{ProbedAt: time.Now(), RoutesProbedAt: routeTime, Results: []v2.UnlockItem{{ID: "netflix"}}, Routes: []v2.RouteItem{{Carrier: "telecom", Target: "1.1.1.1"}}}); err != nil {
			t.Fatal(err)
		}
		value, jerr := publicGetNodeInsights(guestContext(), &rpc.JsonRpcRequest{Params: map[string]any{"uuid": uuid}})
		if jerr != nil {
			t.Fatal(jerr)
		}
		got := value.(nodeInsights)
		if got.Unlock.Stale || !got.Routes.ProbedAt.Equal(routeTime) || got.Routes.Stale != (age > 30*time.Hour) {
			t.Fatalf("route clock refreshed by media: %+v", got.Routes)
		}
	}
}
