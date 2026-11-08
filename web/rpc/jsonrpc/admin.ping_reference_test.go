package jsonrpc

import (
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/scheduler"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

func TestEditPingTaskReferencePreservesOmissionAndPersistsExplicitChanges(t *testing.T) {
	db := dbcore.GetDBInstance()
	task := models.PingTask{
		Name: "reference-edit-fixture", Type: "tcp", Target: "127.0.0.1:80",
		Reference: "old.example:80", Interval: 3600,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() {
		db.Delete(&models.PingTask{}, task.Id)
		scheduler.RemovePrefix("ping:")
	})

	edit := func(reference any, includeReference bool) {
		t.Helper()
		row := map[string]any{
			"id": task.Id, "name": task.Name, "type": task.Type,
			"target": task.Target, "interval": task.Interval, "clients": []string{},
		}
		if includeReference {
			row["reference"] = reference
		}
		_, rpcErr := adminEditPingTask(nil, &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION, Method: "admin:editPingTask",
			Params: map[string]any{"tasks": []any{row}},
		})
		if rpcErr != nil {
			t.Fatalf("edit: %+v", rpcErr)
		}
	}
	assertReference := func(want string) {
		t.Helper()
		var got models.PingTask
		if err := db.First(&got, task.Id).Error; err != nil {
			t.Fatalf("load task: %v", err)
		}
		if got.Reference != want {
			t.Fatalf("reference = %q, want %q", got.Reference, want)
		}
	}

	// The server view and older API clients omit the reference field.
	edit(nil, false)
	assertReference("old.example:80")
	edit("  new.example:443  ", true)
	assertReference("new.example:443")
	edit("", true)
	assertReference("")
	edit("  gateway.example  ", true)
	assertReference("gateway.example")
	edit(nil, true)
	assertReference("gateway.example")
}

func TestEditPingTaskReferenceReachesScheduledEvent(t *testing.T) {
	db := dbcore.GetDBInstance()
	const nodeID = "reference-test-synthetic-node"
	client := models.Client{UUID: nodeID, Token: "reference-test-synthetic-token", IPv4: "192.0.2.1"}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("create synthetic node: %v", err)
	}
	t.Cleanup(func() { db.Delete(&models.Client{}, "uuid = ?", nodeID) })
	agent_runtime.MarkV2Client(nodeID)
	task := models.PingTask{
		Name: "reference-dispatch-fixture", Type: "tcp", Target: "127.0.0.1:80",
		Reference: "old.example:80", Clients: models.StringArray{nodeID}, Interval: 1,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() {
		scheduler.RemovePrefix("ping:")
		agent_runtime.ResetClient(nodeID)
		db.Delete(&models.PingTask{}, task.Id)
	})
	_, rpcErr := adminEditPingTask(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:editPingTask",
		Params: map[string]any{"tasks": []any{map[string]any{
			"id": task.Id, "name": task.Name, "type": task.Type, "target": task.Target,
			"interval": task.Interval, "clients": []string{nodeID}, "reference": "new.example:443",
		}}},
	})
	if rpcErr != nil {
		t.Fatalf("edit: %+v", rpcErr)
	}
	events := agent_runtime.WaitV2Events(nodeID, nil, 3*time.Second)
	if len(events) != 1 || events[0].Method != v2.MethodAgentPing {
		t.Fatalf("scheduled events = %#v, want one ping", events)
	}
	params, ok := events[0].Params.(v2.PingParams)
	if !ok || params.TaskID != task.Id || params.Reference != "new.example:443" {
		t.Fatalf("scheduled ping params = %#v, want updated reference", events[0].Params)
	}
}

func TestEditPingTaskRejectsMalformedReference(t *testing.T) {
	_, rpcErr := adminEditPingTask(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:editPingTask",
		Params: map[string]any{"tasks": []any{map[string]any{
			"id": 1, "name": "bad-reference", "type": "tcp", "target": "127.0.0.1:80",
			"interval": 3600, "reference": map[string]any{"value": "unexpected"},
		}}},
	})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "Invalid request data") {
		t.Fatalf("malformed reference should be rejected, got %+v", rpcErr)
	}
}
