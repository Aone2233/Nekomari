package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func cloneConfigDocument(t *testing.T, source configxport.Document) configxport.Document {
	t.Helper()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := configxport.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func isolatedImportDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.Client{}, &models.PingTask{}, &models.PingTaskDeletion{}, &models.Log{},
		&models.MaintenanceWindow{}, &models.OfflineNotification{}, &config.ConfigItem{}); err != nil {
		t.Fatal(err)
	}
	if err := models.EnsureConfigIdentities(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDraftConfigImportCrossPanelIdentityAndRoundTrip(t *testing.T) {
	source, target := isolatedImportDB(t), isolatedImportDB(t)
	node := models.Client{UUID: uuid.NewString(), Token: "source-agent-token", Name: "source node"}
	if err := source.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	generation, err := pingguard.NewGeneration()
	if err != nil {
		t.Fatal(err)
	}
	pinned := models.PingTask{Name: "IPv6", Type: "auto", Target: "example.test:80", Interval: 60,
		TargetFamily: "ipv6", PinnedGeneration: generation, Clients: models.StringArray{node.UUID}}
	if err := source.Create(&pinned).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	window := models.MaintenanceWindow{Name: "source window", Start: now, End: now.Add(time.Hour), Clients: models.StringArray{node.UUID}}
	if err := source.Create(&window).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&models.OfflineNotification{Client: node.UUID, Enable: true, GracePeriod: 99}).Error; err != nil {
		t.Fatal(err)
	}
	// The destination deliberately assigns the same numeric IDs to unrelated
	// records; its omitted default-on task must still receive the new node.
	unrelated := models.PingTask{Name: "destination task", Type: "tcp", Target: "127.0.0.1:80", Interval: 60, DefaultOn: true}
	defaultPinned := models.PingTask{Name: "destination pinned", Type: "tcp", Target: "example.test:80", Interval: 60,
		DefaultOn: true, TargetFamily: "ipv6", PinnedGeneration: generation}
	unrelatedWindow := models.MaintenanceWindow{Name: "destination window", Start: now, End: now.Add(time.Hour)}
	if err := target.Create(&unrelated).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Create(&unrelatedWindow).Error; err != nil {
		t.Fatal(err)
	}
	if unrelated.Id != pinned.Id || unrelatedWindow.ID != window.ID {
		t.Fatal("fixture did not create numeric ID collisions")
	}
	if err := target.Create(&defaultPinned).Error; err != nil {
		t.Fatal(err)
	}
	doc, err := configxport.Export(exportStore{db: source}, false, "test")
	if err != nil || doc.SchemaVersion != configxport.IdentitySchemaVersion {
		t.Fatalf("export=%+v err=%v", doc, err)
	}
	reloads := 0
	reload := func() error { reloads++; return nil }
	preview, err := draftImportConfig(t.Context(), target, cloneConfigDocument(t, doc), true, reload)
	if err != nil || preview.Creates != 4 || preview.Updates != 2 || reloads != 0 {
		t.Fatalf("preview=%+v reloads=%d err=%v", preview, reloads, err)
	}
	effects := make(map[string]configxport.Change)
	for _, change := range preview.Changes {
		if change.Effect == "default_on_assignment" {
			effects[change.ID] = change
		}
	}
	if len(effects) != 2 || !reflect.DeepEqual(effects[unrelated.ConfigUID].Fields, []string{"clients"}) ||
		!reflect.DeepEqual(effects[defaultPinned.ConfigUID].Fields, []string{"clients"}) {
		t.Fatalf("preview omitted automatic task updates: %+v", effects)
	}
	var before models.PingTask
	if err := target.Take(&before, defaultPinned.Id).Error; err != nil || before.AppliesToClient(node.UUID) || before.PinnedGeneration != generation {
		t.Fatalf("preview changed pinned assignment: %+v err=%v", before, err)
	}
	var count int64
	if err := target.Model(&models.Client{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("preview persisted nodes: count=%d err=%v", count, err)
	}
	bad := cloneConfigDocument(t, doc)
	bad.NotificationConfigs[0]["grace_period"] = -1
	if _, err := draftImportConfig(t.Context(), target, bad, false, reload); err == nil {
		t.Fatal("invalid trailing record committed")
	}
	if err := target.Model(&models.Client{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed import partially committed: count=%d err=%v", count, err)
	}
	applied, err := draftImportConfig(t.Context(), target, cloneConfigDocument(t, doc), false, reload)
	if err != nil || !reflect.DeepEqual(applied, preview) || reloads != 1 {
		t.Fatalf("apply=%+v reloads=%d err=%v", applied, reloads, err)
	}
	var gotNode models.Client
	var gotPinned, gotUnrelated models.PingTask
	var gotWindow, gotUnrelatedWindow models.MaintenanceWindow
	if err := target.Where("uuid = ?", node.UUID).Take(&gotNode).Error; err != nil {
		t.Fatal(err)
	}
	if gotNode.Token == "" || gotNode.Token == node.Token {
		t.Fatal("credential-free import did not generate a distinct usable credential")
	}
	if err := target.Where("config_uid = ?", pinned.ConfigUID).Take(&gotPinned).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Take(&gotUnrelated, unrelated.Id).Error; err != nil {
		t.Fatal(err)
	}
	if gotPinned.Id == unrelated.Id || gotPinned.TargetFamily != "ipv6" || gotPinned.PinnedGeneration == generation || !pingguard.ValidGeneration(gotPinned.PinnedGeneration) || !gotPinned.AppliesToClient(node.UUID) || gotUnrelated.Name != unrelated.Name || !gotUnrelated.AppliesToClient(node.UUID) {
		t.Fatalf("task identities or assignments incorrect: pinned=%+v unrelated=%+v", gotPinned, gotUnrelated)
	}
	var gotDefaultPinned models.PingTask
	if err := target.Take(&gotDefaultPinned, defaultPinned.Id).Error; err != nil || !gotDefaultPinned.AppliesToClient(node.UUID) || gotDefaultPinned.PinnedGeneration == generation || !pingguard.ValidGeneration(gotDefaultPinned.PinnedGeneration) {
		t.Fatalf("automatic pinned assignment was not committed: %+v err=%v", gotDefaultPinned, err)
	}
	if err := target.Where("config_uid = ?", window.ConfigUID).Take(&gotWindow).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Take(&gotUnrelatedWindow, unrelatedWindow.ID).Error; err != nil || gotWindow.ID == unrelatedWindow.ID || gotUnrelatedWindow.Name != unrelatedWindow.Name {
		t.Fatalf("window identity collision: new=%+v old=%+v err=%v", gotWindow, gotUnrelatedWindow, err)
	}
	second, err := draftImportConfig(t.Context(), target, cloneConfigDocument(t, doc), false, reload)
	if err != nil || second.Creates != 0 || second.Updates != 0 || reloads != 1 {
		t.Fatalf("repeat=%+v reloads=%d err=%v", second, reloads, err)
	}
	if err := target.Delete(&gotPinned).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Create(&models.PingTaskDeletion{TaskID: gotPinned.Id, ConfigUID: gotPinned.ConfigUID}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := draftImportConfig(t.Context(), target, cloneConfigDocument(t, doc), false, reload); err == nil {
		t.Fatal("old export recreated a deleted task identity")
	}
}

func TestDraftConfigImportPreservesExplicitZeroValues(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	taskID := uuid.NewString()
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion,
		Clients:             []map[string]any{{"uuid": id, "name": "node", "currency": "", "traffic_limit_type": ""}},
		PingTasks:           []map[string]any{{"config_uid": taskID, "name": "zero weight", "type": "tcp", "target": "127.0.0.1:80", "interval": 60, "weight": 0, "clients": []string{id}}},
		NotificationConfigs: []map[string]any{{"client": id, "enable": false, "grace_period": 0}},
	}
	reload := func() error { return nil }
	plan, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), false, reload)
	if err != nil || plan.Creates != 3 {
		t.Fatalf("import=%+v err=%v", plan, err)
	}
	var node models.Client
	var task models.PingTask
	var notification models.OfflineNotification
	if err := db.Where("uuid = ?", id).Take(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("config_uid = ?", taskID).Take(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("client = ?", id).Take(&notification).Error; err != nil {
		t.Fatal(err)
	}
	if node.Currency != "" || node.TrafficLimitType != "" || task.Weight != 0 || notification.GracePeriod != 0 || notification.Enable {
		t.Fatalf("zero values changed: node=%+v task=%+v notification=%+v", node, task, notification)
	}
	second, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), false, reload)
	if err != nil || second.Creates != 0 || second.Updates != 0 {
		t.Fatalf("zero-value repeat=%+v err=%v", second, err)
	}
}

func TestDraftConfigImportDoesNotDropExplicitEmptyFamilyOnCreate(t *testing.T) {
	db := isolatedImportDB(t)
	uid := uuid.NewString()
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion,
		PingTasks: []map[string]any{{"config_uid": uid, "name": "legacy", "type": "tcp", "target": "127.0.0.1:80",
			"interval": 60, "clients": []string{}, "target_family": ""}}}
	reloads := 0
	reload := func() error { reloads++; return nil }
	for _, preview := range []bool{true, false} {
		plan, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, reload)
		if err != nil || plan.Creates != 1 {
			t.Fatalf("preview=%v plan=%+v err=%v", preview, plan, err)
		}
	}
	for _, preview := range []bool{true, false} {
		plan, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, reload)
		if err != nil || plan.Creates != 0 || plan.Updates != 0 || reloads != 1 {
			t.Fatalf("repeat preview=%v plan=%+v reloads=%d err=%v", preview, plan, reloads, err)
		}
	}
}

func TestDraftConfigImportRejectsNewNodeExcludedFromImportedDefaultTask(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(fmt.Sprintf("pinned=%v", pinned), func(t *testing.T) {
			db := isolatedImportDB(t)
			task := models.PingTask{Name: "default task", Type: "tcp", Target: "example.test:80", Interval: 60,
				DefaultOn: true, Clients: models.StringArray{}}
			if !pinned {
				task.Target = "127.0.0.1:80"
			}
			if pinned {
				task.TargetFamily = "ipv6"
				var err error
				task.PinnedGeneration, err = pingguard.NewGeneration()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			record := map[string]any{"config_uid": task.ConfigUID, "clients": []string{}}
			if pinned {
				record["target_family"] = "ipv6"
			}
			id := uuid.NewString()
			doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion,
				Clients: []map[string]any{{"uuid": id, "name": "new node"}}, PingTasks: []map[string]any{record}}
			for _, preview := range []bool{true, false} {
				_, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, func() error { return nil })
				if err == nil {
					t.Fatal("excluded new node bypassed default-on semantics")
				}
				var count int64
				var stored models.PingTask
				if err := db.Model(&models.Client{}).Where("uuid = ?", id).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("failed import created node: count=%d err=%v", count, err)
				}
				if err := db.Take(&stored, task.Id).Error; err != nil || stored.AppliesToClient(id) || stored.PinnedGeneration != task.PinnedGeneration {
					t.Fatalf("failed import changed task: %+v err=%v", stored, err)
				}
			}
		})
	}
}

func TestDraftConfigImportNewNodeAndDefaultTaskInEitherCreationOrder(t *testing.T) {
	for _, createTask := range []bool{false, true} {
		t.Run(fmt.Sprintf("create-task=%v", createTask), func(t *testing.T) {
			db := isolatedImportDB(t)
			task := models.PingTask{Name: "default task", Type: "tcp", Target: "127.0.0.1:80", Interval: 60,
				DefaultOn: true, Clients: models.StringArray{}}
			if createTask {
				task.ConfigUID = uuid.NewString()
			} else if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			record := map[string]any{"config_uid": task.ConfigUID, "clients": []string{id}}
			if createTask {
				for key, value := range map[string]any{"name": task.Name, "type": task.Type, "target": task.Target,
					"interval": task.Interval, "default_on": true} {
					record[key] = value
				}
			}
			doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion,
				Clients: []map[string]any{{"uuid": id, "name": "new node"}}, PingTasks: []map[string]any{record}}
			var previewPlan configxport.ImportPlan
			reloads := 0
			for _, preview := range []bool{true, false} {
				plan, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, func() error { reloads++; return nil })
				if err != nil {
					t.Fatalf("preview=%v plan=%+v err=%v", preview, plan, err)
				}
				if preview {
					previewPlan = plan
				} else if !reflect.DeepEqual(plan, previewPlan) {
					t.Fatalf("preview and apply differ: %+v %+v", previewPlan, plan)
				}
			}
			var stored models.PingTask
			if err := db.Where("config_uid = ?", task.ConfigUID).Take(&stored).Error; err != nil || !stored.DefaultOn || !stored.AppliesToClient(id) || reloads != 1 {
				t.Fatalf("new node was not assigned: task=%+v reloads=%d err=%v", stored, reloads, err)
			}
			plan, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), false, func() error { reloads++; return nil })
			if err != nil || plan.Creates != 0 || plan.Updates != 0 || reloads != 1 {
				t.Fatalf("repeat plan=%+v reloads=%d err=%v", plan, reloads, err)
			}
		})
	}
}

func TestDraftConfigImportRejectsEnablingDefaultOnWhileExcludingNewNode(t *testing.T) {
	for _, createTask := range []bool{false, true} {
		t.Run(fmt.Sprintf("create-task=%v", createTask), func(t *testing.T) {
			db := isolatedImportDB(t)
			task := models.PingTask{Name: "task", Type: "tcp", Target: "127.0.0.1:80", Interval: 60, Clients: models.StringArray{}}
			if createTask {
				task.ConfigUID = uuid.NewString()
			} else if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			record := map[string]any{"config_uid": task.ConfigUID, "clients": []string{}, "default_on": true}
			if createTask {
				for key, value := range map[string]any{"name": task.Name, "type": task.Type, "target": task.Target, "interval": task.Interval} {
					record[key] = value
				}
			}
			doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion,
				Clients: []map[string]any{{"uuid": id, "name": "new node"}}, PingTasks: []map[string]any{record}}
			for _, preview := range []bool{true, false} {
				if _, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, func() error { return nil }); err == nil {
					t.Fatal("default-on task excluded a node created in the same import")
				}
				var count int64
				if err := db.Model(&models.Client{}).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("partial node create: count=%d err=%v", count, err)
				}
			}
		})
	}
}

func TestDraftConfigImportTransactionalPreviewAndPinnedFence(t *testing.T) {
	db := dbcore.GetDBInstance()
	const settingKey = config.DescriptionKey
	var previous config.ConfigItem
	err := db.Where("key = ?", settingKey).Take(&previous).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal(err)
	}
	hadPrevious := err == nil
	node := models.Client{UUID: uuid.NewString(), Token: "draft-transaction-token", Name: "before", Group: "old"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Delete(&node).Error
		if hadPrevious {
			_ = db.Save(&previous).Error
		} else {
			_ = db.Delete(&config.ConfigItem{}, "key = ?", settingKey).Error
		}
	})
	generation, err := pingguard.NewGeneration()
	if err != nil {
		t.Fatal(err)
	}
	task := models.PingTask{Name: "before", Target: "host.test:80", Type: "tcp", Interval: 60,
		TargetFamily: "ipv6", PinnedGeneration: generation, Clients: models.StringArray{node.UUID}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	if err := config.Set(settingKey, "before"); err != nil {
		t.Fatal(err)
	}
	_ = db.Where("client = ?", node.UUID).Delete(&models.OfflineNotification{}).Error
	if err := db.Create(&models.OfflineNotification{Client: node.UUID, GracePeriod: 180}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Where("client = ?", node.UUID).Delete(&models.OfflineNotification{}).Error })
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion,
		Clients:             []map[string]any{{"uuid": node.UUID, "name": "after", "group": "new"}},
		PingTasks:           []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "name": "after", "clients": []string{node.UUID}, "default_on": true, "target_family": "ipv6"}},
		NotificationConfigs: []map[string]any{{"client": node.UUID, "enable": true, "grace_period": 120}},
		Settings:            map[string]any{settingKey: "after"},
	}
	var events atomic.Int32
	config.Subscribe(func(e config.ConfigEvent) {
		if e.IsChanged(settingKey) {
			events.Add(1)
		}
	})
	reloads := 0
	reload := func() error { reloads++; return nil }
	assertStored := func(name, group, expectedSetting, expectedGeneration string, enabled bool) {
		t.Helper()
		var gotNode models.Client
		var gotTask models.PingTask
		var gotNotification models.OfflineNotification
		if err := db.Where("uuid = ?", node.UUID).Take(&gotNode).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Take(&gotTask, task.Id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Where("client = ?", node.UUID).Take(&gotNotification).Error; err != nil {
			t.Fatal(err)
		}
		value, err := config.GetAs[string](settingKey)
		if err != nil || gotNode.Name != name || gotNode.Group != group || gotTask.Name != name || gotTask.PinnedGeneration != expectedGeneration || value != expectedSetting || gotNotification.Enable != enabled {
			t.Fatalf("unexpected imported state node=%+v task=%+v setting=%q notification=%+v err=%v", gotNode, gotTask, value, gotNotification, err)
		}
	}
	before := events.Load()
	preview, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), true, reload)
	if err != nil || preview.Updates != 4 || reloads != 0 {
		t.Fatalf("preview=%+v reloads=%d err=%v", preview, reloads, err)
	}
	assertStored("before", "old", "before", generation, false)
	if events.Load() != before {
		t.Fatal("rolled-back preview published a configuration change")
	}
	bad := cloneConfigDocument(t, doc)
	bad.NotificationConfigs[0]["grace_period"] = -1
	if _, err := draftImportConfig(t.Context(), db, bad, false, reload); err == nil {
		t.Fatal("invalid final record was applied")
	}
	assertStored("before", "old", "before", generation, false)
	if reloads != 0 || events.Load() != before {
		t.Fatal("failed import reloaded or published")
	}
	applied, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), false, reload)
	if err != nil || applied.Updates != preview.Updates || reloads != 1 {
		t.Fatalf("apply=%+v reloads=%d err=%v", applied, reloads, err)
	}
	var saved models.PingTask
	if err := db.Take(&saved, task.Id).Error; err != nil || saved.PinnedGeneration == generation || !pingguard.ValidGeneration(saved.PinnedGeneration) || !saved.DefaultOn {
		t.Fatalf("generation or assignment not committed: %+v err=%v", saved, err)
	}
	assertStored("after", "new", "after", saved.PinnedGeneration, true)
	deadline := time.Now().Add(time.Second)
	for events.Load() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if events.Load() != before+1 {
		t.Fatal("committed settings notification was not published exactly once")
	}
	second, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), false, reload)
	if err != nil || second.Updates != 0 || second.Creates != 0 || reloads != 1 {
		t.Fatalf("second import=%+v reloads=%d err=%v", second, reloads, err)
	}
}

func TestDraftConfigImportRejectsUnsafeInputsBeforeCommit(t *testing.T) {
	db := dbcore.GetDBInstance()
	node := models.Client{UUID: uuid.NewString(), Token: "draft-reject-token", Name: "before"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&node).Error })
	generation, _ := pingguard.NewGeneration()
	task := models.PingTask{Name: "pinned", Target: "host.test:80", Type: "tcp", Interval: 60, TargetFamily: "ipv6", PinnedGeneration: generation, Clients: models.StringArray{node.UUID}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	base := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": node.UUID, "name": "after"}}}
	cases := map[string]func(*configxport.Document){
		"pinned target change": func(d *configxport.Document) {
			d.PingTasks = []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "clients": []string{node.UUID}, "target": "other.test:80", "target_family": "ipv6"}}
		},
		"missing assignment": func(d *configxport.Document) {
			d.PingTasks = []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "clients": []string{uuid.NewString()}, "target_family": "ipv6"}}
		},
		"unversioned pin": func(d *configxport.Document) {
			d.SchemaVersion = 1
			d.PingTasks = []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "clients": []string{node.UUID}, "target_family": "ipv6"}}
		},
		"internal generation": func(d *configxport.Document) {
			d.PingTasks = []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "clients": []string{node.UUID}, "pinned_generation": generation}}
		},
		"null family": func(d *configxport.Document) {
			d.PingTasks = []map[string]any{{"config_uid": task.ConfigUID, "clients": []string{node.UUID}, "target_family": nil}}
		},
		"incomplete new node": func(d *configxport.Document) {
			d.Clients = append(d.Clients, map[string]any{"uuid": uuid.NewString()})
		},
		"unknown task ID": func(d *configxport.Document) {
			d.PingTasks = []map[string]any{{"id": task.Id + 1000000, "config_uid": uuid.NewString(), "name": "new"}}
		},
		"stale token": func(d *configxport.Document) {
			d.SecretsIncluded = true
			d.Clients[0]["token"] = "old-token"
		},
		"unknown credential field": func(d *configxport.Document) { d.Clients[0]["Token"] = node.Token },
		"retired API key":          func(d *configxport.Document) { d.Settings = map[string]any{config.ApiKeyKey: "old"} },
		"undeclared secret setting": func(d *configxport.Document) {
			d.Settings = map[string]any{"draft_import_password": "not-declared"}
		},
		"invalid assignment type": func(d *configxport.Document) {
			d.PingTasks = []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "clients": []any{123}}}
		},
		"duplicate assignment": func(d *configxport.Document) {
			d.PingTasks = []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "clients": []string{node.UUID, node.UUID}}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			doc := cloneConfigDocument(t, base)
			mutate(&doc)
			for _, preview := range []bool{true, false} {
				if _, err := draftImportConfig(t.Context(), db, doc, preview, func() error { t.Error("reloaded invalid import"); return nil }); err == nil {
					t.Fatal("unsafe document accepted")
				}
			}
			var got models.Client
			var gotTask models.PingTask
			if err := db.Take(&got, "uuid = ?", node.UUID).Error; err != nil || got.Name != "before" || got.Token != node.Token {
				t.Fatalf("partial node update: %+v err=%v", got, err)
			}
			if err := db.Take(&gotTask, task.Id).Error; err != nil || gotTask.PinnedGeneration != generation || gotTask.Target != task.Target {
				t.Fatalf("partial task update: %+v err=%v", gotTask, err)
			}
		})
	}
}

func TestDraftConfigImportRejectsInvalidOmittedDefaultHostname(t *testing.T) {
	db := isolatedImportDB(t)
	task := models.PingTask{Name: "legacy default", Type: "tcp", Target: "example.test:80", Interval: 60,
		DefaultOn: true, Clients: models.StringArray{}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	node := uuid.NewString()
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion,
		Clients: []map[string]any{{"uuid": node, "name": "new node"}}}
	for _, preview := range []bool{true, false} {
		if _, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, func() error { return nil }); err == nil {
			t.Fatal("import extended a mixed-family default hostname task")
		}
		var count int64
		var got models.PingTask
		if err := db.Model(&models.Client{}).Where("uuid = ?", node).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("failed import created node: %d, %v", count, err)
		}
		if err := db.Take(&got, task.Id).Error; err != nil || got.AppliesToClient(node) {
			t.Fatalf("failed import changed task: %+v, %v", got, err)
		}
	}
}

func TestDraftConfigImportDoesNotWriteUnreviewedSettings(t *testing.T) {
	db := dbcore.GetDBInstance()
	const key = "draft_import_secret"
	if err := config.Set(key, "same-secret"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&config.ConfigItem{}, "key = ?", key).Error })
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, SecretsIncluded: true, Settings: map[string]any{key: "same-secret"}}
	for _, preview := range []bool{true, false} {
		plan, err := draftImportConfig(t.Context(), db, doc, preview, func() error {
			t.Error("unchanged settings must not reload the ping scheduler")
			return nil
		})
		if err != nil || plan.Creates != 0 || plan.Updates != 0 {
			t.Fatalf("preview=%v plan=%+v err=%v", preview, plan, err)
		}
		doc.Settings[key] = "must-not-write"
		if _, err := draftImportConfig(t.Context(), db, doc, preview, func() error { t.Error("unexpected reload"); return nil }); err == nil {
			t.Fatal("unreviewed setting change accepted")
		}
		value, err := config.GetAs[string](key)
		if err != nil || value != "same-secret" {
			t.Fatalf("unreviewed setting changed: value=%q err=%v", value, err)
		}
		doc.Settings[key] = "same-secret"
	}
}

func TestDraftConfigImportOnlyChangesReviewedPresentationSettings(t *testing.T) {
	db := isolatedImportDB(t)
	values := map[string]any{
		config.SitenameKey:                   "before",
		config.DescriptionKey:                "before description",
		config.DisablePasswordLoginKey:       false,
		config.CorsAllowedOriginsKey:         "https://panel.example",
		config.NotificationMethodKey:         "none",
		config.ExpireNotificationLeadDaysKey: json.Number("7"),
		metricstore.MetricDBDSNKey:           "test-secret-dsn",
		"private_setting":                    map[string]any{"nested": "test-secret-value"},
	}
	for key, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&config.ConfigItem{Key: key, Value: string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, SecretsIncluded: true,
		Settings: make(map[string]any)}
	for key, value := range values {
		doc.Settings[key] = value
	}
	doc.Settings[config.SitenameKey] = "after"
	doc.Settings[config.DescriptionKey] = "after description"
	for _, preview := range []bool{true, false} {
		plan, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, func() error {
			t.Error("setting-only import must not reload scheduler")
			return nil
		})
		if err != nil || plan.Updates != 2 {
			t.Fatalf("preview=%v plan=%+v err=%v", preview, plan, err)
		}
	}
	for _, key := range []string{config.SitenameKey, config.DescriptionKey} {
		var stored config.ConfigItem
		if err := db.Where("key = ?", key).Take(&stored).Error; err != nil {
			t.Fatal(err)
		}
		var value string
		if err := json.Unmarshal([]byte(stored.Value), &value); err != nil || value != doc.Settings[key] {
			t.Fatalf("setting %s not applied: %q err=%v", key, value, err)
		}
	}

	for key, changed := range map[string]any{
		config.DisablePasswordLoginKey:       true,
		config.CorsAllowedOriginsKey:         "*",
		config.NotificationMethodKey:         "unknown",
		config.ExpireNotificationLeadDaysKey: json.Number("-1"),
		metricstore.MetricDBDSNKey:           "other-secret-dsn",
		"private_setting":                    map[string]any{"nested": "other-secret-value"},
		"missing_sensitive_setting":          "new-secret-value",
	} {
		t.Run(key, func(t *testing.T) {
			bad := cloneConfigDocument(t, doc)
			bad.Settings[key] = changed
			id := uuid.NewString()
			bad.Clients = []map[string]any{{"uuid": id, "name": "must not persist"}}
			for _, preview := range []bool{true, false} {
				if _, err := draftImportConfig(t.Context(), db, bad, preview, func() error { return nil }); err == nil {
					t.Fatal("protected setting change accepted")
				}
				var count int64
				if err := db.Model(&models.Client{}).Where("uuid = ?", id).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("partial import: count=%d err=%v", count, err)
				}
			}
		})
	}
}

func TestDraftConfigImportValidatesUnchangedPresentationSettings(t *testing.T) {
	for name, value := range map[string]any{
		"wrong type": map[string]any{"unexpected": true},
		"too long":   strings.Repeat("x", 16*1024+1),
	} {
		t.Run(name, func(t *testing.T) {
			db := isolatedImportDB(t)
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&config.ConfigItem{Key: config.SitenameKey, Value: string(raw)}).Error; err != nil {
				t.Fatal(err)
			}
			doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, SecretsIncluded: true,
				Settings: map[string]any{config.SitenameKey: value}}
			for _, preview := range []bool{true, false} {
				if _, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, func() error { return nil }); err == nil {
					t.Fatal("invalid unchanged value bypassed validation")
				}
			}
		})
	}
}

func TestDraftConfigImportDoesNotInterpretMalformedStoredSettings(t *testing.T) {
	db := isolatedImportDB(t)
	if err := db.Create(&config.ConfigItem{Key: "private_setting", Value: `"secret" "extra"`}).Error; err != nil {
		t.Fatal(err)
	}
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, SecretsIncluded: true,
		Settings: map[string]any{"private_setting": "secret"}}
	if _, err := draftImportConfig(t.Context(), db, doc, false, func() error { return nil }); err == nil {
		t.Fatal("malformed stored JSON was treated as an unchanged setting")
	}
}

func TestDraftConfigImportRejectsMetricStoreChangesWithoutPartialCommit(t *testing.T) {
	for _, key := range []string{
		metricstore.MetricDBDriverKey, metricstore.MetricDBDSNKey,
		metricstore.MetricTablePrefixKey, metricstore.MetricMaxOpenConnsKey,
		metricstore.MetricMaxIdleConnsKey, metricstore.MetricRollupMinuteRetentionMinutesKey,
		metricstore.MetricRollupFiveMinuteRetentionMinutesKey, metricstore.MetricRollupHourRetentionHoursKey,
		metricstore.MigrationTargetKey,
	} {
		t.Run(key, func(t *testing.T) {
			db := isolatedImportDB(t)
			const before = `"existing"`
			if err := db.Create(&config.ConfigItem{Key: key, Value: before}).Error; err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			for _, preview := range []bool{true, false} {
				doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, SecretsIncluded: true,
					Clients: []map[string]any{{"uuid": id, "name": "must roll back"}}, Settings: map[string]any{key: "changed"}}
				if _, err := draftImportConfig(t.Context(), db, doc, preview, func() error { t.Error("unexpected reload"); return nil }); err == nil {
					t.Fatal("metric-store change accepted")
				}
				var count int64
				var item config.ConfigItem
				if err := db.Model(&models.Client{}).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("partial client commit: count=%d err=%v", count, err)
				}
				if err := db.Where("key = ?", key).Take(&item).Error; err != nil || item.Value != before {
					t.Fatalf("metric config changed: item=%+v err=%v", item, err)
				}
			}
		})
	}
}

func TestDraftConfigImportGuardsReferencedNodes(t *testing.T) {
	db := dbcore.GetDBInstance()
	node := models.Client{UUID: uuid.NewString(), Token: "draft-reference-guard-" + uuid.NewString(), Name: "node"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&node).Error })
	task := models.PingTask{Name: "task", Type: "tcp", Target: "127.0.0.1:80", Interval: 60, Clients: models.StringArray{node.UUID}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	start, end := time.Now().UTC(), time.Now().UTC().Add(time.Hour)
	window := models.MaintenanceWindow{Name: "before", Start: start, End: end, Clients: models.StringArray{node.UUID}}
	if err := db.Create(&window).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&window).Error })
	cases := map[string]configxport.Document{
		"task":         {SchemaVersion: configxport.IdentitySchemaVersion, PingTasks: []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "name": "after", "clients": []string{node.UUID}}}},
		"window":       {SchemaVersion: configxport.IdentitySchemaVersion, MaintenanceWindows: []map[string]any{{"id": window.ID, "config_uid": window.ConfigUID, "name": "after", "clients": []string{node.UUID}}}},
		"notification": {SchemaVersion: configxport.IdentitySchemaVersion, NotificationConfigs: []map[string]any{{"client": node.UUID, "enable": true}}},
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			err := clientguard.With(t.Context(), node.UUID, func() error {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
				defer cancel()
				_, err := draftImportConfig(ctx, db, doc, false, func() error { return nil })
				if !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("import crossed referenced node guard: %v", err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDraftConfigImportUpdatesExistingNodeAssignments(t *testing.T) {
	db := dbcore.GetDBInstance()
	first := models.Client{UUID: uuid.NewString(), Token: "{{SECRET_yh72cspq}}", Name: "first"}
	second := models.Client{UUID: uuid.NewString(), Token: "{{SECRET_6igzaqjr}}", Name: "second"}
	for _, node := range []*models.Client{&first, &second} {
		if err := db.Create(node).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Delete(node).Error })
	}
	generation, err := pingguard.NewGeneration()
	if err != nil {
		t.Fatal(err)
	}
	task := models.PingTask{Name: "assigned", Type: "tcp", Target: "example.test:80", Interval: 60,
		TargetFamily: "ipv6", PinnedGeneration: generation, Clients: models.StringArray{first.UUID}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, PingTasks: []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID,
		"target_family": "ipv6", "clients": []string{second.UUID}}}}
	reloads := 0
	reload := func() error { reloads++; return nil }
	for _, preview := range []bool{true, false} {
		plan, err := draftImportConfig(t.Context(), db, cloneConfigDocument(t, doc), preview, reload)
		if err != nil || plan.Updates != 1 {
			t.Fatalf("preview=%v plan=%+v err=%v", preview, plan, err)
		}
		var stored models.PingTask
		if err := db.Take(&stored, task.Id).Error; err != nil {
			t.Fatal(err)
		}
		if preview {
			if len(stored.Clients) != 1 || stored.Clients[0] != first.UUID || stored.PinnedGeneration != generation || reloads != 0 {
				t.Fatalf("preview changed task: %+v reloads=%d", stored, reloads)
			}
		} else if len(stored.Clients) != 1 || stored.Clients[0] != second.UUID || stored.PinnedGeneration == generation || reloads != 1 {
			t.Fatalf("assignment not committed: %+v reloads=%d", stored, reloads)
		}
	}
}

func TestDraftConfigImportWaitsForCredentialAndTaskGuards(t *testing.T) {
	db := dbcore.GetDBInstance()
	node := models.Client{UUID: uuid.NewString(), Token: "draft-guard-token", Name: "before"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&node).Error })
	task := models.PingTask{Name: "task", Type: "tcp", Target: "127.0.0.1:80", Interval: 60, Clients: models.StringArray{node.UUID}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&task).Error })
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": node.UUID, "name": "after"}}, PingTasks: []map[string]any{{"id": task.Id, "config_uid": task.ConfigUID, "clients": []string{node.UUID}, "name": "after"}}}
	for name, hold := range map[string]func(context.Context, func() error) error{
		"credential": func(ctx context.Context, fn func() error) error { return clientguard.With(ctx, node.UUID, fn) },
		"task":       func(ctx context.Context, fn func() error) error { return pingguard.With(ctx, task.Id, fn) },
	} {
		t.Run(name, func(t *testing.T) {
			err := hold(t.Context(), func() error {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
				defer cancel()
				_, err := draftImportConfig(ctx, db, doc, false, func() error { return nil })
				if !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("import crossed %s guard: %v", name, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
