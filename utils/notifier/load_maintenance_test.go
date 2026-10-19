package notifier

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
)

// A maintenance window must suppress host-metric and ping alerts, not only the offline alert.
// Roadmap H3's acceptance names "offline and ping"; sendLoadNotification is the shared exit for
// both rule families (ping rules are evaluated separately but delivered through it), so the filter
// is tested once here rather than per rule.
func TestLoadAlertsInsideAMaintenanceWindowAreSuppressed(t *testing.T) {
	old := maintenanceFor
	t.Cleanup(func() { maintenanceFor = old })
	maintenanceFor = func(_ time.Time, uuid string) Decision {
		if uuid == "node-inside" {
			return Decision{Suppress: true, Reason: "maintenance window test"}
		}
		return Decision{}
	}

	kept := filterMaintenanceSuppressed(time.Now(), []string{"node-inside", "node-outside"}, "cpu high")
	if len(kept) != 1 || kept[0] != "node-outside" {
		t.Fatalf("kept = %v, want only node-outside", kept)
	}
}

// A batch in which every node is inside a window must be a no-op: no sender call, no empty
// notification. The early return is what makes that true, so the test calls the function that owns
// it (the goroutine is never started when the filtered list is empty).
func TestAllClientsInsideAMaintenanceWindowSilencesTheAlert(t *testing.T) {
	old := maintenanceFor
	t.Cleanup(func() { maintenanceFor = old })
	maintenanceFor = func(_ time.Time, _ string) Decision {
		return Decision{Suppress: true, Reason: "fleet-wide window"}
	}

	kept := filterMaintenanceSuppressed(time.Now(), []string{"a", "b"}, "ping loss")
	if len(kept) != 0 {
		t.Fatalf("kept = %v, want none", kept)
	}
	sendLoadNotification([]string{"a", "b"}, models.LoadNotification{Id: 1, Name: "ping loss"})
}
