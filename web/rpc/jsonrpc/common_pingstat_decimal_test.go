package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// resetPingCaches gives each case a cold ping summary and task cache; both are
// process-wide and the package shares one in-memory database.
func resetPingCaches(t *testing.T) {
	t.Helper()
	pingStatsCache.Flush()
	pingTasksCache.Flush()
	t.Cleanup(func() {
		pingStatsCache.Flush()
		pingTasksCache.Flush()
	})
}

// The node card summary is a per-task struct with Latest/Avg/Min/Max. Before v1.6.24 it
// was int, so a 420µs probe read as 0 ms; these tests pin the fractional summary.
func TestGetPingStatsForNodeKeepsFractionalMilliseconds(t *testing.T) {
	resetPingCaches(t)
	uuid := fmt.Sprintf("pingstat-decimal-%d", time.Now().UnixNano())
	base := time.Now().UTC()

	task := models.PingTask{Id: 7, Name: "gateway", Clients: models.StringArray{uuid}, Type: "tcp"}
	records := map[string][]models.PingRecord{uuid: {
		{TaskId: 7, Time: base.Add(-4 * time.Minute), Value: 123},
		{TaskId: 7, Time: base.Add(-3 * time.Minute), Value: -1},               // loss
		{TaskId: 7, Time: base.Add(-2 * time.Minute), Value: 1, ValueUS: 1200}, // 1.2
		{TaskId: 7, Time: base.Add(-1 * time.Minute), Value: 0, ValueUS: 420},  // 0.42, newest valid
	}}

	stats := getPingStatsForNode(uuid, []models.PingTask{task}, records)
	stat, ok := stats["7"]
	if !ok {
		t.Fatalf("ping stats = %+v, want task 7", stats)
	}
	if !floatClose(stat.Latest, 0.42) {
		t.Fatalf("latest = %v, want 0.42: the newest valid probe is 420µs", stat.Latest)
	}
	if !floatClose(stat.Min, 0.42) {
		t.Fatalf("min = %v, want 0.42", stat.Min)
	}
	if !floatClose(stat.Max, 123) {
		t.Fatalf("max = %v, want 123", stat.Max)
	}
	if want := (123.0 + 1.2 + 0.42) / 3; !floatClose(stat.Avg, want) {
		t.Fatalf("avg = %v, want %v", stat.Avg, want)
	}
	if !floatClose(stat.Loss, 100.0/4) {
		t.Fatalf("loss = %v, want 25", stat.Loss)
	}
	if stat.Tail < 0 {
		t.Fatalf("tail = %v, want non-negative", stat.Tail)
	}

	// The wire shape: latency fields stay JSON numbers, now with fractions.
	encoded, err := json.Marshal(stat)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"latest", "avg", "min", "max"} {
		if _, ok := decoded[field].(float64); !ok {
			t.Fatalf("pingStat.%s is not a JSON number: %s", field, encoded)
		}
	}
	if got := decoded["latest"].(float64); !floatClose(got, 0.42) {
		t.Fatalf("wire latest = %v, want 0.42 (%s)", got, encoded)
	}
}

// The old code used "min == 0 means unset", so a genuine 0 reading was replaced by the
// next larger value. minSet is what this pins.
func TestGetPingStatsForNodeDoesNotTreatZeroAsUnset(t *testing.T) {
	resetPingCaches(t)
	uuid := fmt.Sprintf("pingstat-zero-%d", time.Now().UnixNano())
	base := time.Now().UTC()

	task := models.PingTask{Id: 9, Name: "loopback", Clients: models.StringArray{uuid}, Type: "tcp"}
	records := map[string][]models.PingRecord{uuid: {
		{TaskId: 9, Time: base.Add(-2 * time.Minute), Value: 0},
		{TaskId: 9, Time: base.Add(-1 * time.Minute), Value: 5},
	}}
	stat := getPingStatsForNode(uuid, []models.PingTask{task}, records)["9"]
	if !floatClose(stat.Min, 0) {
		t.Fatalf("min = %v, want 0: a measured zero is not an unset value", stat.Min)
	}
	if !floatClose(stat.Max, 5) {
		t.Fatalf("max = %v, want 5", stat.Max)
	}
}

// A task whose probes all failed has no latency: Latest keeps the -1 sentinel and the
// latency fields stay 0, exactly as the integer version reported.
func TestGetPingStatsForNodeAllLoss(t *testing.T) {
	resetPingCaches(t)
	uuid := fmt.Sprintf("pingstat-loss-%d", time.Now().UnixNano())
	base := time.Now().UTC()

	task := models.PingTask{Id: 11, Name: "dead-target", Clients: models.StringArray{uuid}, Type: "icmp"}
	records := map[string][]models.PingRecord{uuid: {
		{TaskId: 11, Time: base.Add(-2 * time.Minute), Value: -1},
		{TaskId: 11, Time: base.Add(-1 * time.Minute), Value: -1},
	}}
	stat := getPingStatsForNode(uuid, []models.PingTask{task}, records)["11"]
	if stat.Latest != -1 {
		t.Fatalf("latest = %v, want the -1 sentinel when nothing was measured", stat.Latest)
	}
	if stat.Min != 0 || stat.Max != 0 || stat.Avg != 0 {
		t.Fatalf("latency = %v/%v/%v, want zeroes", stat.Min, stat.Max, stat.Avg)
	}
	if !floatClose(stat.Loss, 100) {
		t.Fatalf("loss = %v, want 100", stat.Loss)
	}
}

func TestGetPingStatsForNodeWithoutRecordsIsEmpty(t *testing.T) {
	resetPingCaches(t)
	uuid := fmt.Sprintf("pingstat-empty-%d", time.Now().UnixNano())
	task := models.PingTask{Id: 13, Name: "idle", Clients: models.StringArray{uuid}, Type: "tcp"}

	stats := getPingStatsForNode(uuid, []models.PingTask{task}, map[string][]models.PingRecord{})
	if len(stats) != 0 {
		t.Fatalf("stats = %+v, want none without records", stats)
	}
}

// End to end through common:getNodesLatestStatus, in both response shapes the handler
// has: the single-uuid record and the map. The card reads the map's ping summary, so a
// regression that only fixes the internal helper would still show 0 ms.
func TestNodesLatestStatusReportsFractionalPingStats(t *testing.T) {
	resetPingCaches(t)
	uuid := fmt.Sprintf("mint-pingstat-%d", time.Now().UnixNano())
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
	task := models.PingTask{Name: "decimal", Clients: models.StringArray{uuid}, Type: "tcp", Target: "127.0.0.1", Interval: 60}
	if err := dbcore.GetDBInstance().Create(&task).Error; err != nil {
		t.Fatalf("create ping task: %v", err)
	}
	t.Cleanup(func() {
		_ = dbcore.GetDBInstance().Delete(&models.PingTask{}, task.Id).Error
	})
	// The handler reads the task list through a 30 s cache; it was primed above.
	pingTasksCache.Flush()

	for _, record := range []models.PingRecord{
		{Client: uuid, TaskId: task.Id, PingType: "tcp", Time: now.Add(-3 * time.Minute), Value: 0, ValueUS: 420},
		{Client: uuid, TaskId: task.Id, PingType: "tcp", Time: now.Add(-2 * time.Minute), Value: 1, ValueUS: 1200},
		{Client: uuid, TaskId: task.Id, PingType: "tcp", Time: now.Add(-1 * time.Minute), Value: -1},
	} {
		if err := metricstore.WritePingRecord(context.Background(), record); err != nil {
			t.Fatalf("write ping record: %v", err)
		}
	}

	report := v2.Report{UUID: uuid, UpdatedAt: now, SampledAt: now, ReceivedAt: now}
	agent_runtime.RecordReport(report)
	t.Cleanup(func() { agent_runtime.DeleteLatestReport(uuid) })

	check := func(t *testing.T, record latestStatusRecord) {
		t.Helper()
		stat, ok := record.Ping[fmt.Sprintf("%d", task.Id)]
		if !ok {
			t.Fatalf("ping summary missing task %d: %+v", task.Id, record.Ping)
		}
		// Newest valid probe is the 1.2 ms one: the newest record is a loss and must
		// not become "latest 0".
		if !floatClose(stat.Latest, 1.2) {
			t.Fatalf("latest = %v, want 1.2", stat.Latest)
		}
		if !floatClose(stat.Min, 0.42) {
			t.Fatalf("min = %v, want 0.42", stat.Min)
		}
		if !floatClose(stat.Max, 1.2) {
			t.Fatalf("max = %v, want 1.2", stat.Max)
		}
		if want := (0.42 + 1.2) / 2; !floatClose(stat.Avg, want) {
			t.Fatalf("avg = %v, want %v", stat.Avg, want)
		}
		if want := 100.0 / 3; !floatClose(stat.Loss, want) {
			t.Fatalf("loss = %v, want %v", stat.Loss, want)
		}
	}

	t.Run("single uuid", func(t *testing.T) {
		value, rpcErr := getNodesLatestStatus(guestContext(), &rpc.JsonRpcRequest{
			Params: map[string]any{"uuid": uuid},
		})
		if rpcErr != nil {
			t.Fatalf("getNodesLatestStatus: %+v", rpcErr)
		}
		record, ok := value.(latestStatusRecord)
		if !ok {
			t.Fatalf("result type = %T, want latestStatusRecord", value)
		}
		check(t, record)

		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if !containsNumber(encoded, "latest", 1.2) {
			t.Fatalf("wire record lost the fractional latest: %s", encoded)
		}
	})

	t.Run("uuid map", func(t *testing.T) {
		value, rpcErr := getNodesLatestStatus(guestContext(), &rpc.JsonRpcRequest{
			Params: map[string]any{"uuids": []string{uuid}},
		})
		if rpcErr != nil {
			t.Fatalf("getNodesLatestStatus: %+v", rpcErr)
		}
		records, ok := value.(map[string]latestStatusRecord)
		if !ok {
			t.Fatalf("result type = %T, want map", value)
		}
		check(t, records[uuid])
	})
}

// containsNumber reports whether the encoded object contains key with the given numeric
// value at any depth, which is enough to pin "the wire kept the fraction".
func containsNumber(encoded []byte, key string, want float64) bool {
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return false
	}
	ping, _ := decoded["ping"].(map[string]any)
	for _, value := range ping {
		stat, ok := value.(map[string]any)
		if !ok {
			continue
		}
		number, ok := stat[key].(float64)
		if ok && floatClose(number, want) {
			return true
		}
	}
	return false
}
