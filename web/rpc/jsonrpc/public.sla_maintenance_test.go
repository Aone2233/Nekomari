package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/pkg/metric"
	"github.com/Aone2233/nekomari/utils/notifier"
)

// The SLA report discounts maintenance windows, and this is the wiring half: the
// handler reads each node's windows (through the notifier's cached list), clips them to
// the report window, and hands them to internal/sla.

func TestSlaMaintenanceExclusionsClipAndScope(t *testing.T) {
	clearWindows(t)
	t.Cleanup(func() { clearWindows(t) })

	now := time.Now().UTC().Truncate(time.Second)
	start, end := now.Add(-time.Hour), now.Add(time.Hour)

	db := dbcore.GetDBInstance()
	for _, window := range []models.MaintenanceWindow{
		// The report window is inside this one, so it must be clipped to [start, end).
		{Name: "fleet", Start: now.Add(-2 * time.Hour), End: now.Add(2 * time.Hour)},
		// Scoped to one node, and already inside the report window.
		{Name: "one node", Start: now.Add(-30 * time.Minute), End: now.Add(30 * time.Minute), Clients: models.StringArray{"sla-maint-a"}},
		// Another node's window must not leak into node-a's exclusions.
		{Name: "other", Start: now.Add(-time.Minute), End: now.Add(time.Minute), Clients: models.StringArray{"sla-maint-b"}},
	} {
		if err := db.Create(&window).Error; err != nil {
			t.Fatalf("seed window %q: %v", window.Name, err)
		}
	}
	notifier.InvalidateMaintenanceWindows()

	got := slaMaintenanceExclusions([]string{"sla-maint-a", "sla-maint-b"}, start, end)
	if len(got) != 2 {
		t.Fatalf("exclusions for %v, want both nodes", got)
	}

	nodeA := got["sla-maint-a"]
	if len(nodeA) < 2 {
		t.Fatalf("node-a intervals = %+v, want the fleet window and its own", nodeA)
	}
	for _, interval := range nodeA {
		if interval.Start.Before(start) || interval.End.After(end) {
			t.Fatalf("node-a interval %+v is not clipped to [%s, %s)", interval, start, end)
		}
	}

	nodeB := got["sla-maint-b"]
	if len(nodeB) != 2 {
		t.Fatalf("node-b intervals = %+v, want the fleet window and its own", nodeB)
	}
	for _, interval := range nodeB {
		if interval.Start.Before(start) || interval.End.After(end) {
			t.Fatalf("node-b interval %+v is not clipped to [%s, %s)", interval, start, end)
		}
	}

	// No windows at all: no entities are excluded, so a deployment that never schedules
	// maintenance passes an empty map and gets the old report.
	clearWindows(t)
	if got := slaMaintenanceExclusions([]string{"sla-maint-a"}, start, end); len(got) != 0 {
		t.Fatalf("exclusions without windows = %v, want empty", got)
	}
}

// TestPublicSlaReportExcludesMaintenanceWindows is the end-to-end check: a report built
// by the handler loses the maintenance buckets from availability and coverage, while a
// second node with identical data but no window keeps its report.
func TestPublicSlaReportExcludesMaintenanceWindows(t *testing.T) {
	clearWindows(t)
	t.Cleanup(func() { clearWindows(t) })

	now := time.Now().UTC().Truncate(time.Minute)
	stamp := time.Now().UnixNano()
	visible := fmt.Sprintf("sla-maint-visible-%d", stamp)
	other := fmt.Sprintf("sla-maint-other-%d", stamp)

	db := dbcore.GetDBInstance()
	for _, client := range []models.Client{
		{UUID: visible, Name: visible, Token: "test-only-token-" + visible},
		{UUID: other, Name: other, Token: "test-only-token-" + other},
	} {
		if err := db.Create(&client).Error; err != nil {
			t.Fatalf("create client %s: %v", client.UUID, err)
		}
	}
	dbcache.InvalidateAll()
	t.Cleanup(func() {
		db.Where("uuid IN ?", []string{visible, other}).Delete(&models.Client{})
		dbcache.InvalidateAll()
	})

	ctx := context.Background()
	store, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer func() { _ = store.Close() }()
	for _, name := range []string{metricstore.MetricPingLoss, metricstore.MetricPingLatency} {
		if err := store.CreateMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge, RetentionDays: 30}); err != nil {
			t.Fatalf("create metric %s: %v", name, err)
		}
	}

	// One full-loss bucket and one slow-latency bucket in the current minute for both
	// nodes: identical data, so the only difference is the maintenance window.
	for _, entityID := range []string{visible, other} {
		if err := store.Write(ctx, metric.Point{
			MetricName: metricstore.MetricPingLoss, EntityID: entityID, Timestamp: now, Value: 1,
		}); err != nil {
			t.Fatalf("write loss for %s: %v", entityID, err)
		}
		if err := store.Write(ctx, metric.Point{
			MetricName: metricstore.MetricPingLatency, EntityID: entityID, Timestamp: now, Value: 500,
		}); err != nil {
			t.Fatalf("write latency for %s: %v", entityID, err)
		}
	}

	start, end := now.Add(-2*time.Minute), now.Add(time.Minute)
	if err := db.Create(&models.MaintenanceWindow{
		Name: "upgrade", Start: start, End: end, Clients: models.StringArray{visible},
	}).Error; err != nil {
		t.Fatalf("seed window: %v", err)
	}
	notifier.InvalidateMaintenanceWindows()

	result, rpcErr := publicGetSlaReportWithStore(guestContext(), publicSlaReportParams{
		EntityIDs: []string{visible, other}, Start: &start, End: &end,
	}, store)
	if rpcErr != nil {
		t.Fatalf("publicGetSlaReportWithStore: %+v", rpcErr)
	}
	report, ok := result.(publicSlaReportResponse)
	if !ok {
		t.Fatalf("result type = %T, want publicSlaReportResponse", result)
	}
	if report.Count != 2 {
		t.Fatalf("nodes = %+v, want both", report.Nodes)
	}

	var visibleNode, otherNode *int
	for i := range report.Nodes {
		switch report.Nodes[i].EntityID {
		case visible:
			visibleNode = &i
		case other:
			otherNode = &i
		}
	}
	if visibleNode == nil || otherNode == nil {
		t.Fatalf("report nodes = %+v, want both entities", report.Nodes)
	}

	maintained := report.Nodes[*visibleNode]
	if maintained.MaintenanceExcludedSeconds <= 0 {
		t.Fatalf("maintained node excluded = %d, want positive", maintained.MaintenanceExcludedSeconds)
	}
	if maintained.HasReport {
		t.Fatalf("maintained node = %+v, want no report: its only data was maintenance", maintained)
	}
	if len(maintained.Tasks) != 1 || maintained.Tasks[0].Loss.HasData {
		t.Fatalf("maintained node tasks = %+v, want no availability figure", maintained.Tasks)
	}

	plain := report.Nodes[*otherNode]
	if !plain.HasReport {
		t.Fatalf("the node without a window lost its report: %+v", plain)
	}
	if len(plain.Tasks) != 1 || !plain.Tasks[0].Loss.HasData || plain.Tasks[0].Loss.Fraction != 0 {
		t.Fatalf("plain node tasks = %+v, want a 0%% availability figure from its loss bucket", plain.Tasks)
	}
	if plain.MaintenanceExcludedSeconds != 0 {
		t.Fatalf("plain node excluded = %d, want 0", plain.MaintenanceExcludedSeconds)
	}

	encoded, err := json.Marshal(maintained)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "maintenance_excluded_seconds") {
		t.Fatalf("maintained node must report the excluded seconds: %s", encoded)
	}
	encoded, err = json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "maintenance_excluded_seconds") {
		t.Fatalf("plain node must omit the excluded field: %s", encoded)
	}
}
