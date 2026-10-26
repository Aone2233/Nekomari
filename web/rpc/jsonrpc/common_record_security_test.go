package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestCommonRecordsRejectsUnsafeInputs(t *testing.T) {
	end := time.Now().UTC()
	tests := []struct {
		name   string
		params map[string]any
	}{
		{"hours above ceiling", map[string]any{"hours": 366*24 + 1}},
		{"hours would overflow duration", map[string]any{"hours": int(^uint(0) >> 1)}},
		{"malformed hours", map[string]any{"hours": "many"}},
		{"explicit window above ceiling", map[string]any{"start": end.Add(-366*24*time.Hour - time.Nanosecond), "end": end}},
		{"equal bounds", map[string]any{"start": end, "end": end}},
		{"reversed bounds", map[string]any{"start": end.Add(time.Hour), "end": end}},
		{"ancient start", map[string]any{"start": "0001-01-01T00:00:00Z", "end": end}},
		{"partial start above ceiling", map[string]any{"start": end.Add(-367 * 24 * time.Hour)}},
		{"future partial start", map[string]any{"start": end.Add(time.Hour)}},
		{"timestamp without timezone", map[string]any{"start": "2026-10-20 12:00:00"}},
		{"numeric timestamp", map[string]any{"end": 12345}},
		{"invalid negative maxCount", map[string]any{"maxCount": -2}},
	}
	for _, role := range []string{rpc.RoleGuest, rpc.RoleAdmin} {
		for _, kind := range []string{"load", "ping"} {
			for _, test := range tests {
				t.Run(role+"/"+kind+"/"+test.name, func(t *testing.T) {
					params := map[string]any{"type": kind, "uuid": "input-validation-only"}
					for key, value := range test.params {
						params[key] = value
					}
					response := OnInternalRequest(context.Background(), role, "common:getRecords", params)
					if response.Error == nil || response.Error.Code != rpc.InvalidParams {
						t.Fatalf("response = %s, want InvalidParams before a history read", mustMarshalJSON(t, response))
					}
				})
			}
		}
	}
}

func TestCommonRecordsPreservesWindowDefaultsAndBoundary(t *testing.T) {
	end := time.Now().UTC()
	tests := []struct {
		name   string
		params map[string]any
		window time.Duration
	}{
		{"omitted hours", nil, time.Hour},
		{"zero hours", map[string]any{"hours": 0}, time.Hour},
		{"negative hours keep the legacy default", map[string]any{"hours": -1}, time.Hour},
		{"maximum hours", map[string]any{"hours": 366 * 24}, 366 * 24 * time.Hour},
		{"maximum explicit window", map[string]any{"start": end.Add(-366 * 24 * time.Hour), "end": end}, 366 * 24 * time.Hour},
		{"partial end", map[string]any{"end": end}, time.Hour},
		{"offset timestamps", map[string]any{"start": "2026-10-20T10:00:00+08:00", "end": "2026-10-20T11:00:00+08:00"}, time.Hour},
		{"explicit bounds override hours", map[string]any{"hours": 366*24 + 1, "start": end.Add(-time.Hour), "end": end}, time.Hour},
	}
	for _, kind := range []string{"load", "ping"} {
		for _, test := range tests {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				params := map[string]any{"type": kind, "uuid": "window-unknown-node"}
				for key, value := range test.params {
					params[key] = value
				}
				result, rpcErr := getRecords(guestContext(), &rpc.JsonRpcRequest{Params: params})
				if rpcErr != nil {
					t.Fatal(rpcErr)
				}
				var response struct {
					From time.Time `json:"from"`
					To   time.Time `json:"to"`
				}
				if err := json.Unmarshal([]byte(mustMarshalJSON(t, result)), &response); err != nil {
					t.Fatal(err)
				}
				if got := response.To.Sub(response.From); got != test.window {
					t.Fatalf("window = %s, want %s", got, test.window)
				}
				if response.From.Location() != time.UTC || response.To.Location() != time.UTC {
					t.Fatalf("window was not normalized to UTC: %+v", response)
				}
			})
		}
	}
}

type commonRecordsFixture struct {
	visible string
	hidden  string
	deleted string
	taskID  uint
	start   time.Time
	end     time.Time
}

func seedCommonRecordsFixture(t *testing.T) commonRecordsFixture {
	t.Helper()
	prefix := fmt.Sprintf("query-%d", time.Now().UnixNano())
	fixture := commonRecordsFixture{
		visible: prefix + "-visible",
		hidden:  prefix + "-hidden",
		deleted: prefix + "-deleted",
		start:   time.Now().UTC().Truncate(time.Minute).Add(-time.Hour),
		end:     time.Now().UTC(),
	}
	previous, err := config.GetAs[string](metricstore.MetricDBDSNKey, "")
	if err != nil {
		t.Fatal(err)
	}
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
	db := dbcore.GetDBInstance()
	uuids := []string{fixture.visible, fixture.hidden, fixture.deleted}
	t.Cleanup(func() {
		_ = db.Where("uuid IN ?", uuids).Delete(&models.Client{}).Error
		dbcache.InvalidateAll()
	})
	for _, uuid := range uuids {
		client := models.Client{UUID: uuid, Name: uuid, Token: "test-only-" + uuid, Hidden: uuid == fixture.hidden}
		if err := db.Create(&client).Error; err != nil {
			t.Fatal(err)
		}
	}
	task := models.PingTask{
		Name: "query history fixture", Clients: models.StringArray(uuids),
		Type: "tcp", Target: "127.0.0.1:80", Interval: 60,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	fixture.taskID = task.Id
	t.Cleanup(func() { _ = db.Delete(&models.PingTask{}, task.Id).Error })
	for _, uuid := range uuids {
		for i := 0; i < 2; i++ {
			at := fixture.end.Truncate(time.Minute).Add(-time.Duration(5+i*2) * time.Minute)
			if err := metricstore.GetStore().WriteBatch(context.Background(), []metric.Point{{
				MetricName: metricstore.MetricCPU, EntityID: uuid, Timestamp: at, Value: float64(10 + i),
			}}); err != nil {
				t.Fatal(err)
			}
			if err := metricstore.WritePingRecord(context.Background(), models.PingRecord{
				Client: uuid, TaskId: task.Id, Time: at, Value: 20 + i, PingType: "tcp",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Deletion only removes the client row; the metric store still holds its samples.
	if err := db.Where("uuid = ?", fixture.deleted).Delete(&models.Client{}).Error; err != nil {
		t.Fatal(err)
	}
	dbcache.InvalidateAll()
	return fixture
}

func (fixture commonRecordsFixture) request(kind, uuid string) *rpc.JsonRpcRequest {
	return &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "common:getRecords",
		Params: map[string]any{
			"type": kind, "uuid": uuid, "start": fixture.start, "end": fixture.end,
			"task_id": fixture.taskID, "maxCount": -1,
		},
	}
}

func TestCommonRecordsFiltersDeletedAndHiddenHistory(t *testing.T) {
	fixture := seedCommonRecordsFixture(t)
	for _, kind := range []string{"load", "ping"} {
		t.Run(kind, func(t *testing.T) {
			read := func(ctx context.Context, uuid string) string {
				t.Helper()
				result, rpcErr := getRecords(ctx, fixture.request(kind, uuid))
				if rpcErr != nil {
					t.Fatal(rpcErr)
				}
				return mustMarshalJSON(t, result)
			}
			unknown := read(guestContext(), "query-no-such-node")
			for _, uuid := range []string{fixture.hidden, fixture.deleted} {
				if got := read(guestContext(), uuid); got != unknown {
					t.Fatalf("unreadable history differs from unknown UUID:\n got: %s\nwant: %s", got, unknown)
				}
				if got := read(adminContext(), uuid); !strings.Contains(got, uuid) || strings.Contains(got, `"count":0`) {
					t.Fatalf("admin cannot read retained history for %s: %s", uuid, got)
				}
			}
			for _, uuid := range []string{fixture.visible, ""} {
				got := read(guestContext(), uuid)
				if !strings.Contains(got, fixture.visible) || strings.Contains(got, `"count":0`) {
					t.Fatalf("visible history disappeared for uuid=%q: %s", uuid, got)
				}
				for _, forbidden := range []string{fixture.hidden, fixture.deleted} {
					if strings.Contains(got, forbidden) {
						t.Fatalf("guest history or task metadata leaks %s: %s", forbidden, got)
					}
				}
			}
			adminAll := read(adminContext(), "")
			for _, uuid := range []string{fixture.visible, fixture.hidden, fixture.deleted} {
				if !strings.Contains(adminAll, uuid) {
					t.Fatalf("admin all-client history lost %s: %s", uuid, adminAll)
				}
			}
		})
	}
}

func TestCommonRecordsPropagatesReadBudgetAndCancellation(t *testing.T) {
	fixture := seedCommonRecordsFixture(t)
	for _, kind := range []string{"load", "ping"} {
		for _, uuid := range []string{fixture.visible, ""} {
			t.Run(kind+"/"+uuid, func(t *testing.T) {
				req := fixture.request(kind, uuid)
				for _, maxCount := range []int{-1, 1} {
					req.Params.(map[string]any)["maxCount"] = maxCount
					ctx := metric.WithReadBudget(adminContext(), 0)
					_, rpcErr := getRecords(ctx, req)
					if rpcErr == nil || !strings.Contains(mustMarshalJSON(t, rpcErr), metric.ErrReadBudget.Error()) {
						t.Fatalf("maxCount=%d bypassed the read budget: %v", maxCount, rpcErr)
					}
					ctx = metric.WithReadBudgetBytes(adminContext(), 100, 1)
					_, rpcErr = getRecords(ctx, req)
					if rpcErr == nil || !strings.Contains(mustMarshalJSON(t, rpcErr), metric.ErrReadBudget.Error()) {
						t.Fatalf("maxCount=%d bypassed the byte budget: %v", maxCount, rpcErr)
					}
				}
				ctx, cancel := context.WithCancel(adminContext())
				cancel()
				_, rpcErr := getRecords(ctx, req)
				if rpcErr == nil || !strings.Contains(mustMarshalJSON(t, rpcErr), context.Canceled.Error()) {
					t.Fatalf("canceled context was discarded: %v", rpcErr)
				}
			})
		}
	}
}

func TestCommonRecordsPreservesDownsamplingContract(t *testing.T) {
	fixture := seedCommonRecordsFixture(t)
	for _, kind := range []string{"load", "ping"} {
		for _, loadType := range []string{"", "cpu"} {
			for _, maxCount := range []int{0, -1, 1} {
				t.Run(fmt.Sprintf("%s/%s/%d", kind, loadType, maxCount), func(t *testing.T) {
					req := fixture.request(kind, fixture.visible)
					req.Params.(map[string]any)["maxCount"] = maxCount
					req.Params.(map[string]any)["load_type"] = loadType
					result, rpcErr := getRecords(guestContext(), req)
					if rpcErr != nil {
						t.Fatal(rpcErr)
					}
					var response struct {
						Count int `json:"count"`
					}
					if err := json.Unmarshal([]byte(mustMarshalJSON(t, result)), &response); err != nil {
						t.Fatal(err)
					}
					want := 2
					if maxCount == 1 {
						want = 1
					}
					if response.Count != want {
						t.Fatalf("count = %d, want %d", response.Count, want)
					}
				})
			}
		}
	}
}
