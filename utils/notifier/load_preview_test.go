package notifier

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/maintenance"
)

func emptyPreviewSource() loadPreviewSource {
	return loadPreviewSource{
		host:     func(context.Context, string, string, time.Time, time.Time) ([]models.Record, error) { return nil, nil },
		ping:     func(context.Context, string, int, time.Time, time.Time) ([]models.PingRecord, error) { return nil, nil },
		capacity: func(context.Context, string, string) (int64, error) { return 0, nil },
		index: func(context.Context, []uint) (map[uint]models.PingTask, error) {
			return map[uint]models.PingTask{}, nil
		},
		window: func(context.Context) ([]maintenance.Window, error) { return nil, nil },
	}
}

func TestPreviewLoadHostMatchesExistingMathWithoutTreatingUnknownAsHealthy(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	last := now.Add(-time.Minute)
	rule := models.LoadNotification{Id: 42, Clients: models.StringArray{"match", "normal", "empty", "error"}, Metric: "cpu", Threshold: 80, Ratio: .8, Interval: 15, LastNotified: &last}
	source := emptyPreviewSource()
	source.host = func(_ context.Context, uuid, _ string, start, end time.Time) ([]models.Record, error) {
		if !start.Equal(now.Add(-15*time.Minute)) || !end.Equal(now) {
			t.Fatalf("unexpected window %s..%s", start, end)
		}
		switch uuid {
		case "match":
			return []models.Record{{Cpu: 90}, {Cpu: 85}, {Cpu: 10}}, nil
		case "normal":
			return []models.Record{{Cpu: 10}, {Cpu: 20}, {Cpu: 30}}, nil
		case "error":
			return nil, errors.New("backend failure")
		default:
			return nil, nil
		}
	}
	source.window = func(context.Context) ([]maintenance.Window, error) {
		return []maintenance.Window{{Name: "work", Start: now.Add(-time.Minute), End: now.Add(time.Minute), Clients: []string{"match"}}}, nil
	}
	got, err := previewLoadNotification(context.Background(), rule, now, source)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StoredCooldownActive || !got.EvaluatedAt.Equal(now) || len(got.Targets) != 4 {
		t.Fatalf("result: %+v", got)
	}
	a := got.Targets[0]
	// Existing scheduler rounds down 3*0.8 to 2, rather than rounding up to 3.
	if a.Status != "matched" || !a.Maintenance || *a.Required != 2 || *a.Exceeding != 2 || a.Samples != 3 || *a.Threshold != 80 {
		t.Fatalf("match: %+v", a)
	}
	if got.Targets[1].Status != "not_matched" || got.Targets[1].Maintenance || got.Targets[2].Status != "no_data" || got.Targets[3].Status != "query_error" {
		t.Fatalf("targets: %+v", got.Targets)
	}
	if !last.Equal(now.Add(-time.Minute)) || !rule.LastNotified.Equal(last) {
		t.Fatal("preview changed last-notified")
	}
}

func TestPreviewLoadBaselineAndCapacity(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	source := emptyPreviewSource()
	source.host = func(_ context.Context, uuid, _ string, start, end time.Time) ([]models.Record, error) {
		if end.Equal(now) {
			return []models.Record{{Ram: 90}, {Ram: 80}}, nil
		}
		if !end.Equal(now.Add(-15*time.Minute)) || !start.Equal(end.Add(-7*24*time.Hour)) {
			t.Fatalf("baseline window %s..%s", start, end)
		}
		if uuid == "short" {
			return []models.Record{{Ram: 10}}, nil
		}
		return []models.Record{{Ram: 10}, {Ram: 10}, {Ram: 10}, {Ram: 10}, {Ram: 10}, {Ram: 10}, {Ram: 10}, {Ram: 10}}, nil
	}
	source.capacity = func(_ context.Context, uuid, _ string) (int64, error) {
		if uuid == "unknown" {
			return 0, nil
		}
		return 100, nil
	}
	rule := models.LoadNotification{Clients: models.StringArray{"ok", "short", "unknown"}, Metric: "ram", Threshold: 30, Ratio: 1, Interval: 15, Mode: "baseline", BaselineDays: 7, Multiplier: 3}
	got, err := previewLoadNotification(context.Background(), rule, now, source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Targets[0].Status != "matched" || *got.Targets[0].Threshold != 30 || got.Targets[1].Status != "baseline_unavailable" || got.Targets[2].Status != "capacity_unavailable" {
		t.Fatalf("targets: %+v", got.Targets)
	}
}

func TestPreviewPingTasksAndUnknowns(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	source := emptyPreviewSource()
	source.index = func(_ context.Context, ids []uint) (map[uint]models.PingTask, error) {
		return map[uint]models.PingTask{1: {Id: 1, Clients: models.StringArray{"a", "b"}}, 2: {Id: 2, Clients: models.StringArray{"b"}}}, nil
	}
	source.ping = func(_ context.Context, uuid string, id int, _, _ time.Time) ([]models.PingRecord, error) {
		if uuid != "a" || id != 1 {
			t.Fatalf("unexpected ping %s %d", uuid, id)
		}
		return []models.PingRecord{{Value: -1}, {Value: 10}}, nil
	}
	rule := models.LoadNotification{Clients: models.StringArray{"a"}, Tasks: models.StringArray{"1", "2", "3", "bad"}, Metric: PingAlertMetricLoss, Threshold: 50, Interval: 15}
	got, err := previewLoadNotification(context.Background(), rule, now, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 4 || got.Targets[0].Status != "matched" || *got.Targets[0].Value != 50 || got.Targets[0].Samples != 2 || got.Targets[1].Status != "no_targets" || got.Targets[2].Status != "task_missing" || got.Targets[3].Status != "invalid_task" {
		t.Fatalf("targets: %+v", got.Targets)
	}
	rule.Metric = PingAlertMetricLatency
	source.ping = func(context.Context, string, int, time.Time, time.Time) ([]models.PingRecord, error) {
		return []models.PingRecord{{Value: -1}}, nil
	}
	got, err = previewLoadNotification(context.Background(), rule, now, source)
	if err != nil || got.Targets[0].Status != "no_data" {
		t.Fatalf("lost latency: %+v, %v", got, err)
	}
}

func TestPreviewEnforcesBoundsAndCancellationBeforeQueries(t *testing.T) {
	now := time.Now()
	source := emptyPreviewSource()
	calls := 0
	source.host = func(context.Context, string, string, time.Time, time.Time) ([]models.Record, error) {
		calls++
		return nil, nil
	}
	rule := models.LoadNotification{Metric: "cpu", Ratio: .8, Interval: 15, Clients: make(models.StringArray, PreviewLoadLimit+1)}
	if _, err := previewLoadNotification(context.Background(), rule, now, source); !errors.Is(err, ErrPreviewLimit) {
		t.Fatal(err)
	}
	rule.Clients = models.StringArray{"a"}
	rule.Mode = "baseline"
	rule.BaselineDays = 32
	if _, err := previewLoadNotification(context.Background(), rule, now, source); !errors.Is(err, ErrPreviewLimit) {
		t.Fatal(err)
	}
	rule.Mode = "fixed"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := previewLoadNotification(ctx, rule, now, source); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	rule.Metric = PingAlertMetricLoss
	rule.Clients = nil
	rule.Tasks = models.StringArray{"1"}
	source.index = func(context.Context, []uint) (map[uint]models.PingTask, error) {
		clients := make(models.StringArray, PreviewLoadLimit+1)
		for i := range clients {
			clients[i] = fmt.Sprint(i)
		}
		return map[uint]models.PingTask{1: {Clients: clients}}, nil
	}
	if _, err := previewLoadNotification(context.Background(), rule, now, source); !errors.Is(err, ErrPreviewLimit) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("performed %d unbounded queries", calls)
	}
}

func TestPreviewUnsupportedAndEmptyTargetCannotQueryAllNodes(t *testing.T) {
	source := emptyPreviewSource()
	source.host = func(context.Context, string, string, time.Time, time.Time) ([]models.Record, error) {
		t.Fatal("unexpected metric query")
		return nil, nil
	}
	rule := models.LoadNotification{Metric: "backup_age", Clients: models.StringArray{"valid", ""}, Ratio: .8, Interval: 15}
	got, err := previewLoadNotification(context.Background(), rule, time.Now(), source)
	if err != nil || got.Targets[0].Status != "unsupported_metric" || got.Targets[1].Status != "invalid_target" {
		t.Fatalf("%+v %v", got, err)
	}
}
