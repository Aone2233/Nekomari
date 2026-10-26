package jsonrpc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"gorm.io/gorm"
)

func TestAdminLogsRejectsUnsafePagination(t *testing.T) {
	maxInt := strconv.Itoa(int(^uint(0) >> 1))
	tests := []struct {
		name   string
		params any
	}{
		{"limit above ceiling", map[string]any{"limit": "1001"}},
		{"unbounded limit", map[string]any{"limit": maxInt}},
		{"zero limit", map[string]any{"limit": "0"}},
		{"negative limit", map[string]any{"limit": "-1"}},
		{"malformed limit", map[string]any{"limit": "lots"}},
		{"oversized integer limit", map[string]any{"limit": "999999999999999999999999"}},
		{"wrong limit type", map[string]any{"limit": 100}},
		{"zero page", map[string]any{"page": "0"}},
		{"negative page", map[string]any{"page": "-1"}},
		{"malformed page", map[string]any{"page": "many"}},
		{"oversized integer page", map[string]any{"page": "999999999999999999999999"}},
		{"wrong page type", map[string]any{"page": 1}},
		{"offset would overflow", map[string]any{"limit": "1000", "page": maxInt}},
		{"default limit offset would overflow", map[string]any{"page": maxInt}},
		{"malformed filter cannot silently default", map[string]any{"msg_type": []any{"warn"}}},
		{"malformed positional limit", []any{map[string]any{"not": "a string"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := OnInternalRequest(context.Background(), rpc.RoleAdmin, "admin:getLogs", test.params)
			if response.Error == nil || response.Error.Code != rpc.InvalidParams {
				t.Fatalf("response = %s, want InvalidParams", mustMarshalJSON(t, response))
			}
		})
	}
}

func TestAdminLogsPagingBoundsPreserveFiltersAndCSVPageSize(t *testing.T) {
	db := dbcore.GetDBInstance()
	msgType := fmt.Sprintf("query-logs-%d", time.Now().UnixNano())
	logs := make([]models.Log, 1001)
	for i := range logs {
		logs[i] = models.Log{Message: fmt.Sprintf("query-log-%d", i), MsgType: msgType, Time: time.Now().UTC().Add(time.Duration(i) * time.Second)}
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Where("msg_type IN ?", []string{msgType, msgType + "-other"}).Delete(&models.Log{}).Error
	})
	if err := db.Create(&models.Log{Message: "must-not-match", MsgType: msgType + "-other"}).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		limit  string
		page   string
		count  int
		offset int
	}{
		{"defaults", "", "", 100, 0},
		{"CSV maximum page size", "1000", "1", 1000, 0},
		{"last page", "1000", "2", 1, 1000},
		{"normal pagination", "2", "2", 2, 2},
		{"page past the end", "1000", "3", 0, 2000},
		{"largest non-overflowing offset", "1", strconv.Itoa(int(^uint(0) >> 1)), 0, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, rpcErr := adminGetLogs(adminContext(), &rpc.JsonRpcRequest{Params: map[string]any{
				"limit": test.limit, "page": test.page, "msg_type": "  " + msgType + "  ",
			}})
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			response := result.(map[string]any)
			rows := response["logs"].([]models.Log)
			if response["total"] != int64(len(logs)) || len(rows) != test.count {
				t.Fatalf("total=%v, rows=%d; want total=%d, rows=%d", response["total"], len(rows), len(logs), test.count)
			}
			if rows == nil {
				t.Fatal("an empty page must serialize as [], not null")
			}
			if len(rows) > 0 && rows[0].ID != logs[len(logs)-1-test.offset].ID {
				t.Fatalf("first row = %+v, want newest row at offset %d", rows[0], test.offset)
			}
			for _, row := range rows {
				if row.MsgType != msgType {
					t.Fatalf("message type filter was lost: %+v", row)
				}
			}
		})
	}
}

func TestAdminLogsSkipsFetchPastTotal(t *testing.T) {
	db := dbcore.GetDBInstance()
	const callbackName = "test:logs-past-total"
	var fetches int
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]models.Log); ok {
			fetches++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })
	if _, rpcErr := adminGetLogs(adminContext(), &rpc.JsonRpcRequest{Params: map[string]any{
		"limit": "1", "page": strconv.Itoa(int(^uint(0) >> 1)),
	}}); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if fetches != 0 {
		t.Fatalf("issued %d history fetches for an offset past total", fetches)
	}
}

func TestAdminLogsRespectsRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(adminContext())
	cancel()
	_, rpcErr := adminGetLogs(ctx, &rpc.JsonRpcRequest{})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, context.Canceled.Error()) {
		t.Fatalf("canceled request still queried audit logs: %v", rpcErr)
	}
}
