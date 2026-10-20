package notifier

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	corewindow "github.com/Aone2233/nekomari/internal/maintenance"
)

// clearMaintenanceWindows empties the window table and drops the cache, so the shared
// in-memory test database gives each case a known state.
func clearMaintenanceWindows(t *testing.T) {
	t.Helper()
	if err := dbcore.GetDBInstance().Where("1 = 1").Delete(&models.MaintenanceWindow{}).Error; err != nil {
		t.Fatalf("clear maintenance windows: %v", err)
	}
	Invalidate()
}

func seedNotifierWindows(t *testing.T, windows ...models.MaintenanceWindow) {
	t.Helper()
	for _, window := range windows {
		if err := dbcore.GetDBInstance().Create(&window).Error; err != nil {
			t.Fatalf("seed window %q: %v", window.Name, err)
		}
	}
	Invalidate()
}

func windowNames(windows []corewindow.Window) []string {
	names := make([]string, 0, len(windows))
	for _, window := range windows {
		names = append(names, window.Name)
	}
	return names
}

func TestWindowsForFiltersScopeRangeAndOrder(t *testing.T) {
	setupNotifierTestDB(t)
	clearMaintenanceWindows(t)
	t.Cleanup(func() { clearMaintenanceWindows(t) })

	now := time.Now().UTC().Truncate(time.Second)
	seedNotifierWindows(t,
		models.MaintenanceWindow{Name: "past", Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour), Clients: models.StringArray{"node-a"}},
		models.MaintenanceWindow{Name: "later", Start: now.Add(-time.Hour), End: now.Add(2 * time.Hour), Clients: models.StringArray{"node-a"}},
		models.MaintenanceWindow{Name: "earlier", Start: now.Add(-2 * time.Hour), End: now.Add(-30 * time.Minute), Clients: models.StringArray{"node-a"}},
		models.MaintenanceWindow{Name: "other node", Start: now.Add(-time.Hour), End: now.Add(time.Hour), Clients: models.StringArray{"node-b"}},
		models.MaintenanceWindow{Name: "fleetwide", Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
	)

	// Only node-a's windows and the fleet-wide one, ordered by start and then id.
	got := windowNames(WindowsFor("node-a", now.Add(-90*time.Minute), now.Add(90*time.Minute)))
	want := []string{"earlier", "later", "fleetwide"}
	if len(got) != len(want) {
		t.Fatalf("node-a windows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node-a windows = %v, want %v", got, want)
		}
	}

	// Scope: node-b sees its own window and the fleet-wide one, not node-a's.
	got = windowNames(WindowsFor("node-b", now.Add(-90*time.Minute), now.Add(90*time.Minute)))
	want = []string{"other node", "fleetwide"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("node-b windows = %v, want %v", got, want)
	}

	// Range: a window that ended before the range starts does not intersect it.
	got = windowNames(WindowsFor("node-a", now.Add(-5*time.Hour), now.Add(-150*time.Minute)))
	if len(got) != 1 || got[0] != "past" {
		t.Fatalf("windows in an earlier range = %v, want [past]", got)
	}

	// Half-open: an empty range intersects nothing.
	if got := WindowsFor("node-a", now, now); len(got) != 0 {
		t.Fatalf("an empty range returned %v, want none", windowNames(got))
	}
	if got := WindowsFor("", now.Add(-time.Hour), now.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("an empty client id returned %v, want none", windowNames(got))
	}
}

// The helper must not become a database read per node: it is built on the notifier's
// cached list. The proof is that a row removed behind the cache's back is still served
// until Invalidate, which is the same freshness the notifier itself has.
func TestWindowsForReusesTheCachedWindowList(t *testing.T) {
	setupNotifierTestDB(t)
	clearMaintenanceWindows(t)
	t.Cleanup(func() { clearMaintenanceWindows(t) })

	now := time.Now().UTC().Truncate(time.Second)
	window := models.MaintenanceWindow{
		Name: "reboot", Start: now.Add(-time.Hour), End: now.Add(time.Hour),
		Clients: models.StringArray{"node-a"},
	}
	if err := dbcore.GetDBInstance().Create(&window).Error; err != nil {
		t.Fatalf("seed window: %v", err)
	}
	Invalidate()

	start, end := now.Add(-30*time.Minute), now.Add(30*time.Minute)
	if got := WindowsFor("node-a", start, end); len(got) != 1 {
		t.Fatalf("first read = %v, want the seeded window", windowNames(got))
	}

	if err := dbcore.GetDBInstance().Delete(&models.MaintenanceWindow{}, window.ID).Error; err != nil {
		t.Fatalf("delete window behind the cache: %v", err)
	}
	if got := WindowsFor("node-a", start, end); len(got) != 1 || got[0].Name != "reboot" {
		t.Fatalf("second read = %v, want the cached window: the helper must reuse load()", windowNames(got))
	}

	Invalidate()
	if got := WindowsFor("node-a", start, end); len(got) != 0 {
		t.Fatalf("after invalidation = %v, want none", windowNames(got))
	}
}
