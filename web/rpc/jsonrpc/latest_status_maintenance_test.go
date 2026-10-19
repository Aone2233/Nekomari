package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"github.com/Aone2233/nekomari/utils/notifier"
	agent_runtime "github.com/Aone2233/nekomari/web/agent"
)

// seedMaintenanceWindow stores one window directly. The admin save path (with
// its validation and cache invalidation) is covered by admin.maintenance_test.go;
// what these tests need is a stored window and a cold notifier cache.
func seedMaintenanceWindow(t *testing.T, window models.MaintenanceWindow) models.MaintenanceWindow {
	t.Helper()
	if err := dbcore.GetDBInstance().Create(&window).Error; err != nil {
		t.Fatalf("seed maintenance window: %v", err)
	}
	notifier.InvalidateMaintenanceWindows()
	return window
}

// resetMaintenanceWindows gives a test a known window table and a cold cache,
// and restores both afterwards: the package shares one in-memory database.
func resetMaintenanceWindows(t *testing.T) {
	t.Helper()
	clearWindows(t)
	t.Cleanup(func() { clearWindows(t) })
}

func TestMaintenanceStatusForUsesWindowScopeAndBoundaries(t *testing.T) {
	resetMaintenanceWindows(t)
	now := time.Now().UTC().Truncate(time.Second)

	window := seedMaintenanceWindow(t, models.MaintenanceWindow{
		Name: "kernel upgrade", Reason: "reboot",
		Start: now.Add(-time.Minute), End: now.Add(time.Hour),
		Clients: models.StringArray{"node-a"},
	})

	status := maintenanceStatusFor(now, "node-a")
	if status == nil {
		t.Fatal("a node inside a window must report maintenance state")
	}
	if status.Name != "kernel upgrade" || status.Reason != "reboot" {
		t.Fatalf("maintenance = %+v, want the window's name and reason", status)
	}
	if status.Until == nil || !status.Until.Equal(window.End.UTC()) {
		t.Fatalf("until = %v, want the window end %v", status.Until, window.End.UTC())
	}

	// Scope: a window naming one client must not touch another.
	if got := maintenanceStatusFor(now, "node-b"); got != nil {
		t.Fatalf("node-b is not covered by node-a's window, got %+v", got)
	}

	// Boundaries: the window is half-open, so it is open before End and closed at End.
	if got := maintenanceStatusFor(window.End.Add(-time.Second), "node-a"); got == nil {
		t.Fatal("the window must still be open one second before its end")
	}
	if got := maintenanceStatusFor(window.End, "node-a"); got != nil {
		t.Fatalf("the window must be closed at its end, got %+v", got)
	}

	// A window that has already ended is not maintenance.
	expired := seedMaintenanceWindow(t, models.MaintenanceWindow{
		Name: "yesterday", Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour),
		Clients: models.StringArray{"node-c"},
	})
	_ = expired
	if got := maintenanceStatusFor(now, "node-c"); got != nil {
		t.Fatalf("an expired window must not report maintenance, got %+v", got)
	}
}

// TestLatestStatusCarriesMaintenanceOnlyWhileItIsActive is the wire contract:
// the field appears while the node is inside a window (name/reason/until) and is
// absent for a node outside it, so callers that predate the field see no change.
func TestLatestStatusCarriesMaintenanceOnlyWhileItIsActive(t *testing.T) {
	resetMaintenanceWindows(t)
	now := time.Now().UTC().Truncate(time.Second)

	window := seedMaintenanceWindow(t, models.MaintenanceWindow{
		Name: "kernel upgrade", Reason: "reboot",
		Start: now.Add(-time.Minute), End: now.Add(time.Hour),
		Clients: models.StringArray{"node-a"},
	})

	report := v2.Report{UpdatedAt: now, SampledAt: now, ReceivedAt: now}
	report.UUID = "node-a"
	agent_runtime.RecordReport(report)
	report.UUID = "node-b"
	agent_runtime.RecordReport(report)
	t.Cleanup(func() {
		agent_runtime.DeleteLatestReport("node-a")
		agent_runtime.DeleteLatestReport("node-b")
	})

	result, rpcErr := getNodesLatestStatus(guestContext(), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "common:getNodesLatestStatus",
		Params: map[string]any{"uuids": []string{"node-a", "node-b"}, "include_ping": false},
	})
	if rpcErr != nil {
		t.Fatalf("getNodesLatestStatus: %+v", rpcErr)
	}
	records, ok := result.(map[string]latestStatusRecord)
	if !ok {
		t.Fatalf("result type = %T, want map[string]latestStatusRecord", result)
	}

	inWindow, ok := records["node-a"]
	if !ok {
		t.Fatalf("node-a missing from the response: %+v", records)
	}
	if inWindow.Maintenance == nil {
		t.Fatal("node-a is inside a window but the record has no maintenance state")
	}
	if inWindow.Maintenance.Name != "kernel upgrade" || inWindow.Maintenance.Reason != "reboot" {
		t.Fatalf("maintenance = %+v", inWindow.Maintenance)
	}
	if inWindow.Maintenance.Until == nil || !inWindow.Maintenance.Until.Equal(window.End.UTC()) {
		t.Fatalf("until = %v, want %v", inWindow.Maintenance.Until, window.End.UTC())
	}

	outsideWindow, ok := records["node-b"]
	if !ok {
		t.Fatalf("node-b missing from the response: %+v", records)
	}
	if outsideWindow.Maintenance != nil {
		t.Fatalf("node-b is outside the window, got %+v", outsideWindow.Maintenance)
	}

	encodedIn, err := json.Marshal(inWindow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedIn), `"maintenance":{"name":"kernel upgrade","reason":"reboot","until":"`) {
		t.Fatalf("wire record for node-a lost the maintenance contract: %s", encodedIn)
	}
	encodedOut, err := json.Marshal(outsideWindow)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedOut), `"maintenance"`) {
		t.Fatalf("wire record for node-b must omit the maintenance field: %s", encodedOut)
	}
}

// TestLatestStatusSingleNodeCarriesMaintenance pins the single-uuid branch too:
// both branches go through the same appendOne, and a regression that only adds
// the field to the map form would be invisible from the dashboard's detail page.
func TestLatestStatusSingleNodeCarriesMaintenance(t *testing.T) {
	resetMaintenanceWindows(t)
	now := time.Now().UTC().Truncate(time.Second)

	seedMaintenanceWindow(t, models.MaintenanceWindow{
		Name: "reboot", Start: now.Add(-time.Minute), End: now.Add(time.Hour),
		Clients: models.StringArray{"node-solo"},
	})

	report := v2.Report{UpdatedAt: now, SampledAt: now, ReceivedAt: now}
	report.UUID = "node-solo"
	agent_runtime.RecordReport(report)
	t.Cleanup(func() { agent_runtime.DeleteLatestReport("node-solo") })

	result, rpcErr := getNodesLatestStatus(guestContext(), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "common:getNodesLatestStatus",
		Params: map[string]any{"uuid": "node-solo", "include_ping": false},
	})
	if rpcErr != nil {
		t.Fatalf("getNodesLatestStatus: %+v", rpcErr)
	}
	record, ok := result.(latestStatusRecord)
	if !ok {
		t.Fatalf("result type = %T, want latestStatusRecord", result)
	}
	if record.Maintenance == nil || record.Maintenance.Name != "reboot" {
		t.Fatalf("single-node record lost its maintenance state: %+v", record.Maintenance)
	}
}
