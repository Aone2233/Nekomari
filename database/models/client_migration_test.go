package models

import (
	"fmt"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// An existing deployment upgrades in place: the rollout columns must appear on a
// clients table that predates them, because a missing column would make every
// rollout write fail at runtime rather than at boot.
func TestAutoMigrateAddsTheRolloutTargetColumns(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	// The pre-rollout shape: identity and version only.
	if err := db.Exec(`CREATE TABLE clients (
		uuid TEXT PRIMARY KEY,
		token TEXT NOT NULL,
		name TEXT,
		version TEXT
	)`).Error; err != nil {
		t.Fatalf("create legacy clients table: %v", err)
	}

	if err := db.AutoMigrate(&Client{}); err != nil {
		t.Fatalf("migrate client: %v", err)
	}

	var columns []struct {
		Name string
	}
	if err := db.Raw("PRAGMA table_info(clients)").Scan(&columns).Error; err != nil {
		t.Fatalf("read clients schema: %v", err)
	}
	have := make(map[string]bool, len(columns))
	for _, column := range columns {
		have[column.Name] = true
	}
	for _, want := range []string{"target_version", "target_version_pending_clear"} {
		if !have[want] {
			t.Fatalf("clients table is missing %q after AutoMigrate (columns: %v)", want, have)
		}
	}

	// The new marker must default to false on rows that predate it, otherwise an
	// upgrade would look like every node owes a clear.
	if err := db.Exec(`INSERT INTO clients (uuid, token, name) VALUES (?, ?, ?)`, "legacy", "token", "legacy").Error; err != nil {
		t.Fatalf("insert legacy client: %v", err)
	}
	var legacy Client
	if err := db.First(&legacy, "uuid = ?", "legacy").Error; err != nil {
		t.Fatalf("load legacy client: %v", err)
	}
	if legacy.TargetVersionPendingClear {
		t.Fatal("legacy row reports a pending rollout clear")
	}
	if legacy.TargetVersion != "" {
		t.Fatalf("legacy row target = %q, want empty", legacy.TargetVersion)
	}
}
