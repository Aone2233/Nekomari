package jsonrpc

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils/notifier"
)

// H3's server half, against the real database TestMain wires.
//
// The rule these cover that nothing else can: **a window is stored, read back, and acted on with
// the same boundaries.** The arithmetic is tested in internal/maintenance; what is tested here is
// that an operator's window becomes a stored row, that the row is refused when it cannot mean
// anything, and that the notifier's cached view of it is invalidated by a write — because a stale
// cache is suppression that does not happen, and the symptom is silence where silence is
// indistinguishable from "nothing went wrong".

// saveWindow goes through the registered handler, so parameter binding is part of what is tested.
func saveWindow(t *testing.T, params map[string]any) (any, *rpc.JsonRpcError) {
	t.Helper()
	return adminSaveMaintenance(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:saveMaintenanceWindow", Params: params,
	})
}

func listWindows(t *testing.T) (any, *rpc.JsonRpcError) {
	t.Helper()
	return adminListMaintenance(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:listMaintenanceWindows",
	})
}

// clearWindows empties the table so each test starts from a known state: the suite shares one
// in-memory database.
func clearWindows(t *testing.T) {
	t.Helper()
	db := dbcore.GetDBInstance()
	if err := db.Where("1 = 1").Delete(&models.MaintenanceWindow{}).Error; err != nil {
		t.Fatalf("clear maintenance windows: %v", err)
	}
	notifier.InvalidateMaintenanceWindows()
}

func TestSaveAndListAMaintenanceWindow(t *testing.T) {
	clearWindows(t)

	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	end := start.Add(2 * time.Hour)

	if _, rpcErr := saveWindow(t, map[string]any{
		"name":   "kernel upgrade",
		"start":  start.Format(time.RFC3339),
		"end":    end.Format(time.RFC3339),
		"reason": "reboot",
	}); rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}

	result, rpcErr := listWindows(t)
	if rpcErr != nil {
		t.Fatalf("list: %+v", rpcErr)
	}
	payload := result.(map[string]any)
	windows := payload["windows"].([]maintenanceWindowView)
	if len(windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(windows))
	}
	window := windows[0]
	if window.Name != "kernel upgrade" || window.Reason != "reboot" {
		t.Fatalf("window = %+v", window)
	}
	// Stored and reported in UTC, so the page and the notifier compare the same instants.
	if window.Start != start.Format(time.RFC3339) || window.End != end.Format(time.RFC3339) {
		t.Fatalf("times round-tripped as %s..%s, want %s..%s",
			window.Start, window.End, start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	// It is open now: the window spans the present, with no client list, so it is fleet-wide.
	if !window.Open {
		t.Error("a window spanning now must be reported open")
	}
	if !window.CoversEverything {
		t.Error("a window with no client list covers every node")
	}
	if window.RemainingSeconds <= 0 {
		t.Errorf("remaining = %d, want positive", window.RemainingSeconds)
	}
	// And the list says which window is open, for a page that wants one line.
	active := payload["active"].([]string)
	if len(active) != 1 || active[0] != "kernel upgrade" {
		t.Fatalf("active = %v, want the window's name", active)
	}
}

// A window that ends before it starts would never be open, and saving it would leave an operator
// believing suppression is in place.
func TestAWindowThatCannotBeOpenIsRefused(t *testing.T) {
	clearWindows(t)
	now := time.Now().UTC().Truncate(time.Second)

	cases := []map[string]any{
		{"name": "backwards", "start": now.Add(time.Hour).Format(time.RFC3339), "end": now.Format(time.RFC3339)},
		{"name": "zero length", "start": now.Format(time.RFC3339), "end": now.Format(time.RFC3339)},
		{"name": "no timezone", "start": "2026-09-27 20:00:00", "end": now.Add(time.Hour).Format(time.RFC3339)},
		{"name": "", "start": now.Format(time.RFC3339), "end": now.Add(time.Hour).Format(time.RFC3339)},
		{"name": "missing start", "end": now.Add(time.Hour).Format(time.RFC3339)},
	}
	for _, params := range cases {
		if _, rpcErr := saveWindow(t, params); rpcErr == nil {
			t.Errorf("accepted an impossible window: %+v", params)
		}
	}

	// And nothing was written.
	result, _ := listWindows(t)
	if got := len(result.(map[string]any)["windows"].([]maintenanceWindowView)); got != 0 {
		t.Fatalf("%d windows were stored despite every save being refused", got)
	}
}

// Updating a window must be able to *empty* its client list. GORM's struct update skips zero
// values, so an implementation that used one would leave the old node list in place and the
// window would keep covering nodes the operator had removed.
func TestAnUpdateCanWidenScopeToEveryNode(t *testing.T) {
	clearWindows(t)
	now := time.Now().UTC().Truncate(time.Second)
	db := dbcore.GetDBInstance()

	row := models.MaintenanceWindow{
		Name: "scoped", Start: now.Add(-time.Hour), End: now.Add(time.Hour),
		Clients: []string{"node-a", "node-b"},
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	notifier.InvalidateMaintenanceWindows()

	if _, rpcErr := saveWindow(t, map[string]any{
		"id": row.ID, "name": "scoped",
		"start": now.Add(-time.Hour).Format(time.RFC3339),
		"end":   now.Add(time.Hour).Format(time.RFC3339),
		// clients omitted: every node
	}); rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}

	var stored models.MaintenanceWindow
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(stored.Clients) != 0 {
		t.Fatalf("clients = %v, want empty: the update must be able to widen the scope", stored.Clients)
	}
}

// The cache is what lets the notifier avoid a database read per connection, and a write must
// invalidate it. Otherwise an operator creates a window, reboots a node, and gets an alert —
// which is the feature not working, with no error anywhere to explain it.
func TestAWriteInvalidatesTheNotifierCache(t *testing.T) {
	clearWindows(t)
	now := time.Now().UTC().Truncate(time.Second)

	// Prime the cache while there are no windows.
	before := notifier.ActiveFor(now, "node-a")
	if before.Active {
		t.Fatal("expected no active window before one is created")
	}

	if _, rpcErr := saveWindow(t, map[string]any{
		"name":  "now open",
		"start": now.Add(-time.Minute).Format(time.RFC3339),
		"end":   now.Add(time.Hour).Format(time.RFC3339),
	}); rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}

	// Read again immediately: the handler invalidated the cache, so this must see the new window
	// rather than the cached empty list.
	after := notifier.ActiveFor(now, "node-a")
	if !after.Active {
		t.Fatal("the window was created but the notifier still sees no window: the cache was not invalidated")
	}
	if after.Window == nil || after.Window.Name != "now open" {
		t.Fatalf("reported window = %v, want the new one", after.Window)
	}

	// And deleting it stops suppression just as immediately.
	if _, rpcErr := adminDeleteMaintenance(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Method: "admin:deleteMaintenanceWindow",
		Params: map[string]any{"id": after.Window.ID},
	}); rpcErr != nil {
		t.Fatalf("delete: %+v", rpcErr)
	}
	if got := notifier.ActiveFor(now, "node-a"); got.Active {
		t.Fatal("the window was deleted but the notifier still suppresses")
	}
}

// A scoped window suppresses its nodes and only its nodes, which is the property a fleet-wide
// window cannot demonstrate.
func TestAScopedWindowSuppressesOnlyItsNodes(t *testing.T) {
	clearWindows(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, rpcErr := saveWindow(t, map[string]any{
		"name":    "one node",
		"start":   now.Add(-time.Minute).Format(time.RFC3339),
		"end":     now.Add(time.Hour).Format(time.RFC3339),
		"clients": []string{"node-a"},
	}); rpcErr != nil {
		t.Fatalf("save: %+v", rpcErr)
	}

	if decision := notifier.For(now, "node-a"); !decision.Suppress {
		t.Fatal("the window's own node must be suppressed")
	}
	if decision := notifier.For(now, "node-b"); decision.Suppress {
		t.Fatal("another node must not be suppressed by a scoped window")
	}
	// And the decision names the window, which is what the log line records.
	decision := notifier.For(now, "node-a")
	if decision.Reason == "" {
		t.Fatal("a suppression must say which window caused it")
	}
}
