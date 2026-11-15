package plugin

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// The schedule, metrics and notification are synthetic; no external effects.
func TestKomariMarketTrafficReport(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	archive := verifiedMarketPluginArchive(t, dir, "traffic-report")
	withTempDataDir(t)
	Init(gin.New())
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = old })
	messages := make(chan string, 3)
	handlers := map[string]rpc.Handler{
		"common:getNodes": func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
			return map[string]any{"fixture": map[string]any{"uuid": "fixture", "name": "Fixture server"}}, nil
		},
		"public:queryMetrics": func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
			return map[string]any{"series": []any{
				map[string]any{"entity_id": "fixture", "metric_key": "traffic.up", "count": 1, "points": []any{map[string]any{"value": 1048576}}},
				map[string]any{"entity_id": "fixture", "metric_key": "traffic.down", "count": 1, "points": []any{map[string]any{"value": 2097152}}},
			}}, nil
		},
		"admin:sendNotification": func(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
			var p struct {
				Event struct {
					Message string `json:"message"`
				} `json:"event"`
			}
			_ = req.BindParams(&p)
			select {
			case messages <- p.Event.Message:
			default:
			}
			return nil, nil
		},
	}
	for method, handler := range handlers {
		if err := rpc.Register(method, handler); err != nil {
			t.Fatal(err)
		}
		name := method
		t.Cleanup(func() { rpc.Unregister(name) })
	}
	if _, err := InstallZip(archive); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfiguration("traffic-report", map[string]any{"all_nodes": true, "daily_enabled": true, "daily_cron": "@every 100ms", "weekly_enabled": false, "monthly_enabled": false}); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("traffic-report", true, true); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "Fixture server") || !strings.Contains(message, "1.00 MB") || !strings.Contains(message, "2.00 MB") {
			t.Fatalf("report did not contain fixture traffic: %s", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no report; logs: %s", GetLogs("traffic-report"))
	}
	if err := SetEnabled("traffic-report", false, false); err != nil {
		t.Fatal(err)
	}
}
