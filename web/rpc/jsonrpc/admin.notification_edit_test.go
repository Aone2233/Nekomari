package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/database/notification"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestLoadNotificationZeroFloorPartialEditAndRollback(t *testing.T) {
	db := dbcore.GetDBInstance()
	ruleIDs := []uint{}
	t.Cleanup(func() {
		if len(ruleIDs) > 0 {
			_ = db.Where("id IN ?", ruleIDs).Delete(&models.LoadNotification{}).Error
		}
		_ = notification.ReloadLoadNotificationSchedule()
	})
	call := func(method string, params any) *rpc.JsonRpcResponse {
		return Dispatch(context.Background(), &rpc.ContextMeta{Principal: rpc.NewUserPrincipal("admin")}, rpc.NewRequest(1, method, params))
	}
	created := call("admin:addLoadNotification", map[string]any{
		"name": "zero-floor-test", "clients": []string{"node-a"}, "metric": "cpu", "mode": "baseline",
		"threshold": 0, "ratio": .8, "interval": 240, "baseline_days": 7, "multiplier": 3,
	})
	if created.Error != nil {
		t.Fatal(created.Error)
	}
	id := created.Result.(map[string]any)["task_id"].(uint)
	ruleIDs = append(ruleIDs, id)
	load := func() models.LoadNotification {
		t.Helper()
		var rule models.LoadNotification
		if err := db.First(&rule, id).Error; err != nil {
			t.Fatal(err)
		}
		return rule
	}
	if rule := load(); rule.Threshold != 0 || rule.Ratio != .8 || rule.Mode != "baseline" {
		t.Fatalf("created rule = %+v", rule)
	}
	last := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := db.Model(&models.LoadNotification{}).Where("id = ?", id).Update("last_notified", last).Error; err != nil {
		t.Fatal(err)
	}
	edit := func(items ...any) *rpc.JsonRpcResponse {
		return call("admin:editLoadNotification", map[string]any{"notifications": items})
	}
	if resp := edit(map[string]any{"id": id, "threshold": 20}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp := edit(map[string]any{"id": id, "name": "updated", "threshold": 0, "clients": []string{}, "tasks": []string{}}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	rule := load()
	if rule.Threshold != 0 || rule.Name != "updated" || len(rule.Clients) != 0 || len(rule.Tasks) != 0 || rule.Ratio != .8 || rule.Interval != 240 || rule.LastNotified == nil || !rule.LastNotified.Equal(last) {
		t.Fatalf("zero/empty partial edit = %+v", rule)
	}
	if resp := edit(map[string]any{"id": id, "name": "should-roll-back"}, map[string]any{"id": uint(999999999), "name": "missing"}); resp.Error == nil {
		t.Fatal("missing second rule succeeded")
	}
	if rule = load(); rule.Name != "updated" {
		t.Fatalf("failed batch partially committed: %+v", rule)
	}
	if resp := edit(nil); resp.Error == nil {
		t.Fatal("nil rule edit succeeded")
	}
	changedLast := last.Add(-time.Hour)
	if resp := edit(map[string]any{"id": id, "last_notified": changedLast}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if got := load().LastNotified; got == nil || !got.Equal(changedLast) {
		t.Fatalf("explicit last_notified edit = %v", got)
	}
}

func TestLoadNotificationDefaultsAndInvalidEdits(t *testing.T) {
	db := dbcore.GetDBInstance()
	var ids []uint
	t.Cleanup(func() {
		if len(ids) > 0 {
			_ = db.Where("id IN ?", ids).Delete(&models.LoadNotification{}).Error
		}
		_ = notification.ReloadLoadNotificationSchedule()
	})
	call := func(method string, params any) *rpc.JsonRpcResponse {
		return Dispatch(context.Background(), &rpc.ContextMeta{Principal: rpc.NewUserPrincipal("admin")}, rpc.NewRequest(1, method, params))
	}
	create := func(params map[string]any) uint {
		t.Helper()
		resp := call("admin:addLoadNotification", params)
		if resp.Error != nil {
			t.Fatal(resp.Error)
		}
		id := resp.Result.(map[string]any)["task_id"].(uint)
		ids = append(ids, id)
		return id
	}
	baseline := create(map[string]any{"name": "default-floor", "clients": []string{"node-a"}, "metric": "cpu", "mode": "baseline", "ratio": .8, "interval": 240})
	fixed := create(map[string]any{"name": "fixed", "clients": []string{"node-a"}, "metric": "cpu", "threshold": 80, "ratio": .8, "interval": 240})
	load := func(id uint) models.LoadNotification {
		t.Helper()
		var rule models.LoadNotification
		if err := db.First(&rule, id).Error; err != nil {
			t.Fatal(err)
		}
		return rule
	}
	if rule := load(baseline); rule.Threshold != 80 {
		t.Fatalf("omitted baseline threshold = %v, want 80", rule.Threshold)
	}
	for _, params := range []map[string]any{
		{"name": "missing-fixed", "clients": []string{"node-a"}, "metric": "cpu", "ratio": .8, "interval": 240},
		{"name": "zero-fixed", "clients": []string{"node-a"}, "metric": "cpu", "threshold": 0, "ratio": .8, "interval": 240},
		{"name": "malformed", "clients": []string{"node-a"}, "metric": "cpu", "mode": "baseline", "threshold": "not a number", "ratio": .8, "interval": 240},
	} {
		if resp := call("admin:addLoadNotification", params); resp.Error == nil || resp.Error.Code != rpc.InvalidParams {
			t.Fatalf("invalid create %#v: %+v", params, resp)
		}
	}
	edit := func(items ...any) *rpc.JsonRpcResponse {
		return call("admin:editLoadNotification", map[string]any{"notifications": items})
	}
	for _, update := range []map[string]any{
		{"id": fixed, "interval": 0}, {"id": fixed, "interval": -1}, {"id": fixed, "interval": 241},
		{"id": fixed, "ratio": 0}, {"id": fixed, "ratio": 1.1}, {"id": fixed, "mode": ""},
		{"id": fixed, "mode": "unknown"}, {"id": fixed, "threshold": 0}, {"id": fixed, "metric": ""},
	} {
		if resp := edit(map[string]any{"id": baseline, "name": "must-rollback"}, update); resp.Error == nil {
			t.Fatalf("invalid batch edit succeeded: %#v", update)
		}
		if got := load(baseline); got.Name != "default-floor" {
			t.Fatalf("batch was partially committed: %+v", got)
		}
		if got := load(fixed); got.Interval != 240 || got.Ratio != .8 || got.Mode != "fixed" || got.Threshold != 80 || got.Metric != "cpu" {
			t.Fatalf("invalid edit changed stored rule: %+v", got)
		}
	}
	if resp := edit(map[string]any{"id": baseline, "threshold": 0, "mode": "fixed"}); resp.Error == nil {
		t.Fatal("fixed zero in a single update succeeded")
	}
	if resp := edit(map[string]any{"id": baseline, "threshold": 0}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp := edit(map[string]any{"id": baseline, "mode": "fixed"}); resp.Error == nil {
		t.Fatal("switching a zero floor to fixed succeeded")
	}
	if got := load(baseline); got.Threshold != 0 || got.Mode != "baseline" {
		t.Fatalf("failed mode switch changed stored rule: %+v", got)
	}
	if resp := edit(map[string]any{"id": baseline, "mode": "fixed", "threshold": 25}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	// A pre-existing bad schedule must not let a different edit commit and then
	// lose existing scheduled jobs when reload fails.
	if err := db.Model(&models.LoadNotification{}).Where("id = ?", fixed).Update("interval", 0).Error; err != nil {
		t.Fatal(err)
	}
	if resp := edit(map[string]any{"id": baseline, "name": "must-not-commit"}); resp.Error == nil {
		t.Fatal("edit succeeded despite an invalid legacy schedule")
	}
	if got := load(baseline); got.Name != "default-floor" {
		t.Fatalf("bad schedule caused a partial commit: %+v", got)
	}
	if resp := call("admin:addLoadNotification", map[string]any{"name": "must-not-create", "clients": []string{"node-a"}, "metric": "cpu", "mode": "baseline", "threshold": 0, "ratio": .8, "interval": 240}); resp.Error == nil {
		t.Fatal("create succeeded despite an invalid legacy schedule")
	}
	var count int64
	if err := db.Model(&models.LoadNotification{}).Where("name = ?", "must-not-create").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("bad schedule caused a partial create: %d %v", count, err)
	}
	if resp := edit(map[string]any{"id": fixed, "interval": 240}); resp.Error != nil {
		t.Fatalf("repairing invalid interval failed: %v", resp.Error)
	}
	// Unrelated updates do not silently normalize legacy invalid settings.
	if err := db.Model(&models.LoadNotification{}).Where("id = ?", fixed).Updates(map[string]any{"mode": "legacy", "ratio": 0}).Error; err != nil {
		t.Fatal(err)
	}
	if resp := edit(map[string]any{"id": fixed, "clients": []string{}}); resp.Error != nil {
		t.Fatalf("legacy rule partial edit: %v", resp.Error)
	}
	if got := load(fixed); got.Mode != "legacy" || got.Ratio != 0 || len(got.Clients) != 0 {
		t.Fatalf("legacy rule was normalized: %+v", got)
	}
}
