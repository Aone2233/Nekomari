package jsonrpc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestPublicPingRecordsPreserveAddressFamily(t *testing.T) {
	const uuid = "mint-ping-family"
	seedVisibilityClients(t, uuid, uuid+"-hidden")
	previous, _ := config.GetAs[string](metricstore.MetricDBDSNKey)
	if err := config.Set(metricstore.MetricDBDSNKey, filepath.Join(t.TempDir(), "metrics.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = metricstore.CloseStoreContext(context.Background())
		_ = config.Set(metricstore.MetricDBDSNKey, previous)
	})
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatal(err)
	}
	for i, family := range []string{"ipv4", "ipv6"} {
		if err := metricstore.WritePingRecord(context.Background(), models.PingRecord{
			Client: uuid, TaskId: 123, PingType: "tcp", Family: family,
			Time: time.Now().Add(-time.Duration(i+1) * time.Second), Value: 20 + i,
		}); err != nil {
			t.Fatal(err)
		}
	}
	value, rpcErr := publicGetPingRecords(guestContext(), &rpc.JsonRpcRequest{Params: map[string]any{"uuid": uuid, "hours": "1"}})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response struct {
		Records []struct {
			Family string `json:"family"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(mustMarshalJSON(t, value)), &response); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, record := range response.Records {
		seen[record.Family] = true
	}
	if len(response.Records) != 2 || !seen["ipv4"] || !seen["ipv6"] {
		t.Fatalf("public history lost address families: %+v", response)
	}
}
