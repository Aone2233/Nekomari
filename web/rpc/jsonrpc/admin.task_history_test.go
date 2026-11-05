package jsonrpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestExecHistoryScopedDispatch(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := "rpc-history-synthetic"
	if err := db.Create(&models.Task{TaskId: id, Clients: models.StringArray{"deleted"}, Command: "synthetic-secret-command"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tasks SET history_order = rowid WHERE task_id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.TaskResult{TaskId: id, Client: "deleted", Result: "synthetic-secret-output", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	methods := []struct {
		name   string
		params map[string]any
	}{
		{"admin:listExecHistory", nil},
		{"admin:getExecHistoryDetail", map[string]any{"task_id": id}},
		{"admin:readExecHistoryChunk", map[string]any{"task_id": id, "kind": "command"}},
	}
	for _, method := range methods {
		for _, subject := range []struct {
			name      string
			principal *rpc.Principal
			allowed   bool
		}{
			{"session", rpc.NewUserPrincipal("admin"), true},
			{"tasks_read", rpc.NewScopedAPIKeyPrincipal("read", []string{rpc.ScopeTasksRead}), true},
			{"nodes_read", rpc.NewScopedAPIKeyPrincipal("nodes", []string{rpc.ScopeNodesRead}), false},
			{"tasks_exec", rpc.NewScopedAPIKeyPrincipal("exec", []string{rpc.ScopeTasksExec}), false},
			{"agent", rpc.NewAgentPrincipal("deleted"), false},
			{"guest", rpc.NewAnonymousPrincipal(), false},
		} {
			t.Run(method.name+"/"+subject.name, func(t *testing.T) {
				req := rpc.NewRequest(1, method.name, method.params)
				response := Dispatch(t.Context(), &rpc.ContextMeta{Principal: subject.principal}, req)
				if subject.allowed && response.Error != nil {
					t.Fatalf("allowed call failed: %v", response.Error)
				}
				if !subject.allowed && (response.Error == nil || response.Error.Code != rpc.PermissionDenied) {
					t.Fatalf("unrelated principal not denied: %+v", response)
				}
			})
		}
	}
	page, rpcErr := adminListExecHistory(t.Context(), rpc.NewRequest(1, "admin:listExecHistory", nil))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "synthetic-secret-") {
		t.Fatal("summary includes sensitive text")
	}
	if _, rpcErr = adminReadExecHistoryChunk(t.Context(), rpc.NewRequest(1, "admin:readExecHistoryChunk", map[string]any{"task_id": id, "kind": "command", "offset": -1})); rpcErr == nil || rpcErr.Code != rpc.InvalidParams {
		t.Fatalf("invalid offset accepted: %v", rpcErr)
	}
	if _, rpcErr = adminGetExecHistoryDetail(t.Context(), rpc.NewRequest(1, "admin:getExecHistoryDetail", map[string]any{"task_id": "not-present"})); rpcErr == nil || rpcErr.Code != rpc.NotFound {
		t.Fatalf("missing task: %v", rpcErr)
	}
	_, rpcErr = adminListExecHistory(context.Background(), rpc.NewRequest(1, "admin:listExecHistory", map[string]any{"cursor": 12}))
	if rpcErr == nil || rpcErr.Code != rpc.InvalidParams {
		t.Fatalf("non-string cursor accepted: %v", rpcErr)
	}
	_, err := tasks.ListHistory(context.Background(), strings.Repeat("x", 37))
	if err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
