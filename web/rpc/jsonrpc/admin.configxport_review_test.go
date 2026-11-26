package jsonrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/configxport"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReviewedDraftImportRejectsOversizedDocumentAndRecordCount(t *testing.T) {
	db := isolatedImportDB(t)
	doc := `{"schema_version":3,"source_version":"` + strings.Repeat("x", maxConfigImportDocumentBytes) + `"}`
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "admin:planConfigImport", "params": map[string]any{"document_json": doc}})
	if err != nil {
		t.Fatal(err)
	}
	var req rpc.JsonRpcRequest
	if err := json.Unmarshal(request, &req); err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := reviewedDraftConfigImportRPC(t.Context(), &req, db, true, func() error { return nil }); rpcErr == nil || !strings.Contains(rpcErr.Message, "size limit") {
		t.Fatalf("oversized document not rejected: %+v", rpcErr)
	}
	tooMany := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: make([]map[string]any, maxConfigImportRecords+1)}
	if _, err := reviewedDraftImportConfig(t.Context(), db, tooMany, true, "", func() error { return nil }); err == nil || !strings.Contains(err.Error(), "too many records") {
		t.Fatalf("record limit not enforced: %v", err)
	}
}

func TestReviewedDraftImportLegacyIdentityPolicy(t *testing.T) {
	for _, schema := range []int{configxport.SchemaVersion, configxport.PinnedSchemaVersion} {
		for _, entity := range []string{"ping_tasks", "maintenance_windows"} {
			t.Run(fmt.Sprintf("schema_%d_%s", schema, entity), func(t *testing.T) {
				db := isolatedImportDB(t)
				id := uuid.NewString()
				if err := db.Create(&models.Client{UUID: id, Token: "legacy-test-token", Name: "before"}).Error; err != nil {
					t.Fatal(err)
				}
				doc := configxport.Document{SchemaVersion: schema, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
				if entity == "ping_tasks" {
					doc.PingTasks = []map[string]any{{"id": 1}}
				} else {
					doc.MaintenanceWindows = []map[string]any{{"id": 1}}
				}
				for _, preview := range []bool{true, false} {
					if _, err := reviewedDraftImportConfig(t.Context(), db, doc, preview, map[bool]string{true: "", false: strings.Repeat("0", 64)}[preview], func() error { return nil }); !errors.Is(err, errConfigImportLegacyIdentity) {
						t.Fatalf("preview=%v: legacy numeric identity not refused: %v", preview, err)
					}
				}
				var node models.Client
				if err := db.Where("uuid = ?", id).Take(&node).Error; err != nil || node.Name != "before" {
					t.Fatalf("legacy document changed node: %+v, err=%v", node, err)
				}
			})
		}
		t.Run(fmt.Sprintf("schema_%d_portable_entities", schema), func(t *testing.T) {
			db := isolatedImportDB(t)
			id := uuid.NewString()
			if err := db.Create(&models.Client{UUID: id, Token: "legacy-test-token", Name: "before"}).Error; err != nil {
				t.Fatal(err)
			}
			doc := configxport.Document{SchemaVersion: schema, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
			reload := func() error { t.Error("client-only import reloaded ping schedule"); return nil }
			preview, err := reviewedDraftImportConfig(t.Context(), db, doc, true, "", reload)
			if err != nil || preview.SchemaVersion != schema || preview.Updates != 1 || len(preview.ReviewToken) != 64 {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			if !strings.Contains(strings.Join(preview.Warnings, "\n"), "numeric IDs are local") {
				t.Fatalf("legacy restriction not reported: %+v", preview.Warnings)
			}
			if _, err := reviewedDraftImportConfig(t.Context(), db, doc, false, preview.ReviewToken, reload); err != nil {
				t.Fatal(err)
			}
			var node models.Client
			if err := db.Where("uuid = ?", id).Take(&node).Error; err != nil || node.Name != "after" {
				t.Fatalf("portable import failed: %+v err=%v", node, err)
			}
		})
	}
}

func TestReviewedDraftImportReviewBindsOriginalLegacySchema(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "legacy-test-token", Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	doc := configxport.Document{SchemaVersion: configxport.SchemaVersion, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
	reload := func() error { return nil }
	preview, err := reviewedDraftImportConfig(t.Context(), db, doc, true, "", reload)
	if err != nil {
		t.Fatal(err)
	}
	doc.SchemaVersion = configxport.PinnedSchemaVersion
	if _, err := reviewedDraftImportConfig(t.Context(), db, doc, false, preview.ReviewToken, reload); !errors.Is(err, errConfigImportPreviewStale) {
		t.Fatalf("preview for another schema accepted: %v", err)
	}
}

func TestReviewedDraftImportRejectsStaleDocumentOrState(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *gorm.DB, *configxport.Document, string){
		"document": func(_ *testing.T, _ *gorm.DB, doc *configxport.Document, _ string) {
			doc.Clients[0]["name"] = "different"
		},
		"client configuration": func(t *testing.T, db *gorm.DB, _ *configxport.Document, id string) {
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Update("group", "changed").Error; err != nil {
				t.Fatal(err)
			}
		},
		"credential": func(t *testing.T, db *gorm.DB, _ *configxport.Document, id string) {
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Update("token", "replacement-token").Error; err != nil {
				t.Fatal(err)
			}
		},
		"reported family": func(t *testing.T, db *gorm.DB, _ *configxport.Document, id string) {
			if err := db.Model(&models.Client{}).Where("uuid = ?", id).Update("ipv6", "2001:db8::1").Error; err != nil {
				t.Fatal(err)
			}
		},
		"new default-on task": func(t *testing.T, db *gorm.DB, _ *configxport.Document, _ string) {
			if err := db.Create(&models.PingTask{Name: "new default", Target: "127.0.0.1", Type: "icmp", Interval: 60, DefaultOn: true}).Error; err != nil {
				t.Fatal(err)
			}
		},
		"reserved task": func(t *testing.T, db *gorm.DB, _ *configxport.Document, _ string) {
			if err := db.Create(&models.PingTaskDeletion{TaskID: 42}).Error; err != nil {
				t.Fatal(err)
			}
		},
		"private setting": func(t *testing.T, db *gorm.DB, _ *configxport.Document, _ string) {
			if err := db.Create(&config.ConfigItem{Key: "private", Value: `"changed"`}).Error; err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := isolatedImportDB(t)
			id := uuid.NewString()
			if err := db.Create(&models.Client{UUID: id, Token: "review-test-token", Name: "before"}).Error; err != nil {
				t.Fatal(err)
			}
			doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
			reloads := 0
			reload := func() error { reloads++; return nil }
			preview, err := reviewedDraftImportConfig(t.Context(), db, doc, true, "", reload)
			if err != nil || len(preview.ReviewToken) != 64 || strings.Contains(preview.ReviewToken, "review-test-token") {
				t.Fatalf("preview token=%q err=%v", preview.ReviewToken, err)
			}
			mutate(t, db, &doc, id)
			if _, err := reviewedDraftImportConfig(t.Context(), db, doc, false, preview.ReviewToken, reload); !errors.Is(err, errConfigImportPreviewStale) {
				t.Fatalf("stale preview accepted: %v", err)
			}
			var client models.Client
			if err := db.Where("uuid = ?", id).Take(&client).Error; err != nil || client.Name != "before" || reloads != 0 {
				t.Fatalf("stale import changed state: client=%+v reloads=%d err=%v", client, reloads, err)
			}
		})
	}
}

func TestReviewedDraftImportIgnoresTelemetryAndRequiresNewPreviewAfterCommit(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "review-token", Name: "before", IPv6: "2001:db8::1"}).Error; err != nil {
		t.Fatal(err)
	}
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
	reload := func() error { t.Error("client-only import reloaded ping schedule"); return nil }
	preview, err := reviewedDraftImportConfig(t.Context(), db, doc, true, "", reload)
	if err != nil || preview.Updates != 1 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	// Normal Agent heartbeats do not alter the configuration under review.
	if err := db.Model(&models.Client{}).Where("uuid = ?", id).Updates(map[string]any{"cpu_name": "heartbeat", "ipv6": "2001:db8::2"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", strings.Repeat("0", 64), preview.ReviewToken[:63]} {
		if _, err := reviewedDraftImportConfig(t.Context(), db, doc, false, token, reload); !errors.Is(err, errConfigImportPreviewStale) {
			t.Fatalf("invalid token accepted: %v", err)
		}
	}
	applied, err := reviewedDraftImportConfig(t.Context(), db, doc, false, preview.ReviewToken, reload)
	if err != nil || applied.Updates != preview.Updates || applied.ReviewToken != preview.ReviewToken {
		t.Fatalf("apply=%+v err=%v", applied, err)
	}
	if _, err := reviewedDraftImportConfig(t.Context(), db, doc, false, preview.ReviewToken, reload); !errors.Is(err, errConfigImportPreviewStale) {
		t.Fatalf("old preview accepted after commit: %v", err)
	}
	again, err := reviewedDraftImportConfig(t.Context(), db, doc, true, "", reload)
	if err != nil || again.Updates != 0 || again.ReviewToken == preview.ReviewToken {
		t.Fatalf("new preview=%+v err=%v", again, err)
	}
}

func TestReviewedDraftImportAuditCommitsAtomically(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "audit-test-token", Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
	entry := &models.Log{UUID: uuid.NewString(), IP: "192.0.2.1", Message: "import configuration", MsgType: "warn"}
	reload := func() error { t.Error("client-only import reloaded ping schedule"); return nil }
	preview, err := reviewedDraftImportConfigWithAudit(t.Context(), db, doc, true, "", reload, entry)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&models.Log{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("preview persisted audit: count=%d err=%v", count, err)
	}
	// A failure after all configuration writes still rolls them back with the
	// audit record, rather than committing an unaudited import.
	if err := db.Exec(`CREATE TRIGGER fail_config_audit BEFORE INSERT ON logs BEGIN SELECT RAISE(ABORT, 'audit failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reviewedDraftImportConfigWithAudit(t.Context(), db, doc, false, preview.ReviewToken, reload, entry); err == nil {
		t.Fatal("import committed despite audit failure")
	}
	var client models.Client
	if err := db.Where("uuid = ?", id).Take(&client).Error; err != nil || client.Name != "before" {
		t.Fatalf("failed audit left a configuration change: %+v err=%v", client, err)
	}
	if err := db.Exec(`DROP TRIGGER fail_config_audit`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reviewedDraftImportConfigWithAudit(t.Context(), db, doc, false, preview.ReviewToken, reload, entry); err != nil {
		t.Fatal(err)
	}
	var stored models.Log
	if err := db.Take(&stored).Error; err != nil || stored.UUID != entry.UUID || stored.IP != entry.IP || stored.Message != entry.Message || stored.MsgType != entry.MsgType || stored.Time.IsZero() {
		t.Fatalf("committed audit is wrong: %+v err=%v", stored, err)
	}
	if err := db.Where("uuid = ?", id).Take(&client).Error; err != nil || client.Name != "after" {
		t.Fatalf("audited import did not commit: %+v err=%v", client, err)
	}
}

func TestReviewedDraftImportConcurrentReviewAllowsOnlyOneCommit(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "legacy-test-token", Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
	audit := &models.Log{UUID: uuid.NewString(), Message: "import configuration", MsgType: "warn"}
	reload := func() error { return nil }
	preview, err := reviewedDraftImportConfigWithAudit(t.Context(), db, doc, true, "", reload, audit)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := reviewedDraftImportConfigWithAudit(t.Context(), db, doc, false, preview.ReviewToken, reload, audit)
			results <- err
		}()
	}
	close(start)
	committed, stale := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			committed++
		case errors.Is(err, errConfigImportPreviewStale):
			stale++
		default:
			t.Fatalf("unexpected concurrent import result: %v", err)
		}
	}
	var count int64
	if err := db.Model(&models.Log{}).Count(&count).Error; err != nil || committed != 1 || stale != 1 || count != 1 {
		t.Fatalf("commits=%d stale=%d audits=%d err=%v", committed, stale, count, err)
	}
}

func TestReviewedDraftImportSQLiteExternalWriterBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	dsn := "file:" + filepath.ToSlash(path) + "?_busy_timeout=5000&_txlock=immediate&_journal_mode=WAL"
	open := func() *gorm.DB {
		t.Helper()
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		pool, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		pool.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = pool.Close() })
		return db
	}
	panel, external := open(), open()
	if err := panel.AutoMigrate(&models.Client{}, &models.PingTask{}, &models.PingTaskDeletion{}, &models.Log{},
		&models.MaintenanceWindow{}, &models.OfflineNotification{}, &config.ConfigItem{}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := panel.Create(&models.Client{UUID: id, Token: "legacy-test-token", Name: "before"}).Error; err != nil {
		t.Fatal(err)
	}
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, Clients: []map[string]any{{"uuid": id, "name": "after"}}}
	audit := &models.Log{UUID: uuid.NewString(), Message: "import configuration", MsgType: "warn"}
	reload := func() error { return nil }
	preview, err := reviewedDraftImportConfigWithAudit(t.Context(), panel, doc, true, "", reload, audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := external.Model(&models.Client{}).Where("uuid = ?", id).Update("group", "external-before").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reviewedDraftImportConfigWithAudit(t.Context(), panel, doc, false, preview.ReviewToken, reload, audit); !errors.Is(err, errConfigImportPreviewStale) {
		t.Fatalf("external change before apply did not invalidate preview: %v", err)
	}
	preview, err = reviewedDraftImportConfigWithAudit(t.Context(), panel, doc, true, "", reload, audit)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	if err := panel.Callback().Update().Before("gorm:update").Register("test:pause-reviewed-import", func(tx *gorm.DB) {
		if tx.Statement.Table == "clients" {
			close(entered)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = panel.Callback().Update().Remove("test:pause-reviewed-import") })
	importDone := make(chan error, 1)
	go func() {
		_, err := reviewedDraftImportConfigWithAudit(t.Context(), panel, doc, false, preview.ReviewToken, reload, audit)
		importDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("reviewed import did not reach its write")
	}
	externalStarted := make(chan struct{})
	externalDone := make(chan error, 1)
	go func() {
		close(externalStarted)
		externalDone <- external.Model(&models.Client{}).Where("uuid = ?", id).Update("group", "external-after").Error
	}()
	<-externalStarted
	select {
	case err := <-externalDone:
		close(release)
		<-importDone
		t.Fatalf("external writer finished before reviewed transaction committed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-importDone; err != nil {
		t.Fatalf("reviewed import failed: %v", err)
	}
	if err := <-externalDone; err != nil {
		t.Fatalf("external writer did not resume: %v", err)
	}
	var node models.Client
	var count int64
	if err := panel.Where("uuid = ?", id).Take(&node).Error; err != nil || node.Name != "after" || node.Group != "external-after" {
		t.Fatalf("writes were not serialized: %+v err=%v", node, err)
	}
	if err := panel.Model(&models.Log{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("serialized import audit count=%d err=%v", count, err)
	}
}

func TestReviewedDraftImportAuditsCommittedReloadFailure(t *testing.T) {
	db := isolatedImportDB(t)
	id := uuid.NewString()
	if err := db.Create(&models.Client{UUID: id, Token: "reload-audit-token", Name: "node"}).Error; err != nil {
		t.Fatal(err)
	}
	task := models.PingTask{Name: "before", Target: "127.0.0.1:80", Type: "tcp", Interval: 60, Clients: models.StringArray{id}}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	doc := configxport.Document{SchemaVersion: configxport.IdentitySchemaVersion, PingTasks: []map[string]any{{"config_uid": task.ConfigUID, "name": "after", "clients": []string{id}}}}
	entry := &models.Log{UUID: uuid.NewString(), Message: "import configuration", MsgType: "warn"}
	reload := func() error { return errors.New("injected reload failure") }
	preview, err := reviewedDraftImportConfigWithAudit(t.Context(), db, doc, true, "", reload, entry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reviewedDraftImportConfigWithAudit(t.Context(), db, doc, false, preview.ReviewToken, reload, entry)
	var pending *configImportReloadError
	if !errors.As(err, &pending) {
		t.Fatalf("reload failure was not identified as committed: %v", err)
	}
	var storedTask models.PingTask
	var storedAudit models.Log
	if err := db.First(&storedTask, task.Id).Error; err != nil || storedTask.Name != "after" {
		t.Fatalf("committed task missing: %+v err=%v", storedTask, err)
	}
	if err := db.Take(&storedAudit).Error; err != nil || storedAudit.UUID != entry.UUID || storedAudit.Message != entry.Message {
		t.Fatalf("committed audit missing: %+v err=%v", storedAudit, err)
	}
}
