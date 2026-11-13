package jsonrpc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestProbeUnknownDiagnosticsReadPersistedReasons(t *testing.T) {
	previous, err := config.GetAs[string](metricstore.MetricDBDSNKey, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = config.Set(metricstore.MetricDBDSNKey, filepath.Join(t.TempDir(), "metrics.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = metricstore.CloseStoreContext(context.Background())
		_ = config.Set(metricstore.MetricDBDSNKey, previous)
	})
	if err = metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	if err = metricstore.WritePingRecord(t.Context(), models.PingRecord{Client: "node-unknown", TaskId: 123, PingType: "tcp", Time: time.Now().UTC(), Value: -1, ProbeStatus: "unknown", ProbeReason: "dns_failure"}); err != nil {
		t.Fatal(err)
	}
	value, rpcErr := adminGetProbeUnknowns(t.Context(), &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Params: map[string]any{"task_id": 123}})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	items := value.(map[string]any)["items"].([]probeUnknownItem)
	if len(items) != 1 || items[0].Client != "node-unknown" || items[0].Reason != "dns_failure" || items[0].Count != 1 {
		t.Fatalf("diagnostics = %+v", items)
	}
}
