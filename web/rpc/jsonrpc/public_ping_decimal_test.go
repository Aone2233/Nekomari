package jsonrpc

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

// The v1.6.24 release taught the agent and the metric store to carry sub-millisecond
// ping latency (value_us). These tests pin the RPC side: the public ping summary must
// report fractional milliseconds instead of truncating them to 0, and integer readings
// must keep the same JSON shape they always had.

func floatClose(left, right float64) bool {
	return math.Abs(left-right) < 1e-9
}

func TestPingRecordMillisecondsKeepsSubMillisecondPrecision(t *testing.T) {
	cases := []struct {
		name   string
		record models.PingRecord
		want   float64
	}{
		{"sub-millisecond probe", models.PingRecord{Value: 0, ValueUS: 420}, 0.42},
		{"microseconds win over the truncated value", models.PingRecord{Value: 1, ValueUS: 1200}, 1.2},
		{"no value_us falls back to whole milliseconds", models.PingRecord{Value: 7}, 7},
		{"loss keeps its negative marker", models.PingRecord{Value: -1}, -1},
		{"a measured zero stays zero", models.PingRecord{Value: 0, ValueUS: 0}, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := pingRecordMilliseconds(test.record); !floatClose(got, test.want) {
				t.Fatalf("pingRecordMilliseconds(%+v) = %v, want %v", test.record, got, test.want)
			}
		})
	}
}

func TestSummarizePingLatencyReportsFractionalMinMaxAvg(t *testing.T) {
	records := []models.PingRecord{
		{Value: 0, ValueUS: 420},      // 0.42
		{Value: 1, ValueUS: 1200},     // 1.2
		{Value: -1},                   // loss
		{Value: 123, ValueUS: 123400}, // 123.4
	}
	summary := summarizePingLatency(records)
	if summary.Total != 4 || summary.Loss != 1 || summary.Valid != 3 {
		t.Fatalf("summary counts = %+v, want 4 total / 1 loss / 3 valid", summary)
	}
	if !floatClose(summary.Min, 0.42) {
		t.Fatalf("Min = %v, want 0.42: the smallest fraction must not be dropped by a zero sentinel", summary.Min)
	}
	if !floatClose(summary.Max, 123.4) {
		t.Fatalf("Max = %v, want 123.4", summary.Max)
	}
	if want := (0.42 + 1.2 + 123.4) / 3; !floatClose(summary.Avg, want) {
		t.Fatalf("Avg = %v, want %v", summary.Avg, want)
	}
}

func TestSummarizePingLatencyWithNoMeasurements(t *testing.T) {
	summary := summarizePingLatency([]models.PingRecord{{Value: -1}, {Value: -1}})
	if summary.Total != 2 || summary.Loss != 2 || summary.Valid != 0 {
		t.Fatalf("summary = %+v, want two losses and no valid samples", summary)
	}
	if summary.Min != 0 || summary.Max != 0 || summary.Avg != 0 {
		t.Fatalf("latency = %v/%v/%v, want zeroes when nothing was measured", summary.Min, summary.Max, summary.Avg)
	}
}

// End to end through the registered handler: one sub-millisecond probe, one 1.2 ms probe
// and one loss, each on its own task so the three land in three series (one shared series
// would collapse to a single per-minute bucket).
func TestPublicPingRecordsReportFractionalMilliseconds(t *testing.T) {
	const uuid = "mint-ping-decimal"
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

	now := time.Now().UTC()
	records := []models.PingRecord{
		{Client: uuid, TaskId: 501, PingType: "tcp", Family: "ipv4", Time: now.Add(-4 * time.Second), Value: 0, ValueUS: 420},
		{Client: uuid, TaskId: 502, PingType: "tcp", Family: "ipv4", Time: now.Add(-3 * time.Second), Value: 1, ValueUS: 1200},
		{Client: uuid, TaskId: 503, PingType: "tcp", Family: "ipv4", Time: now.Add(-2 * time.Second), Value: -1},
	}
	for _, record := range records {
		if err := metricstore.WritePingRecord(context.Background(), record); err != nil {
			t.Fatalf("write ping record: %v", err)
		}
	}

	value, rpcErr := publicGetPingRecords(guestContext(), &rpc.JsonRpcRequest{
		Params: map[string]any{"uuid": uuid, "hours": "1"},
	})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	var response struct {
		BasicInfo []struct {
			Client string  `json:"client"`
			Loss   float64 `json:"loss"`
			Min    float64 `json:"min"`
			Max    float64 `json:"max"`
		} `json:"basic_info"`
		Records []struct {
			Value float64 `json:"value"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(mustMarshalJSON(t, value)), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.BasicInfo) != 1 {
		t.Fatalf("basic_info = %+v, want one client", response.BasicInfo)
	}
	info := response.BasicInfo[0]
	if info.Client != uuid {
		t.Fatalf("basic_info client = %q, want %q", info.Client, uuid)
	}
	if !floatClose(info.Min, 0.42) {
		t.Fatalf("basic_info.min = %v, want 0.42: a 420µs probe must not be truncated to 0", info.Min)
	}
	if !floatClose(info.Max, 1.2) {
		t.Fatalf("basic_info.max = %v, want 1.2", info.Max)
	}
	if want := 100.0 / 3; !floatClose(info.Loss, want) {
		t.Fatalf("basic_info.loss = %v, want %v", info.Loss, want)
	}

	if len(response.Records) != 3 {
		t.Fatalf("records = %+v, want three", response.Records)
	}
	seen := map[float64]bool{}
	for _, record := range response.Records {
		seen[record.Value] = true
	}
	for _, want := range []float64{-1, 0.42, 1.2} {
		found := false
		for got := range seen {
			if floatClose(got, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("records %v do not include %v", seen, want)
		}
	}
}
