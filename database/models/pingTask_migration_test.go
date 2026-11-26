package models

import (
	"fmt"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPingFamilyMigrationPreservesLegacyRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE ping_tasks (id integer primary key autoincrement, name text not null,
		clients text, all_clients numeric not null default false, type text not null default 'icmp',
		target text not null, reference text not null default '', interval integer not null default 60,
		weight integer not null default 0)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ping_tasks (name,clients,type,target,reference,interval,weight) VALUES (?,?,?,?,?,?,?)`,
		"legacy", `["node-1"]`, "tcp", "example.com:80", "gateway", 60, 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&PingTask{}); err != nil {
		t.Fatal(err)
	}
	var row PingTask
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TargetFamily != "" || row.PinnedGeneration != "" || row.Name != "legacy" || row.Target != "example.com:80" ||
		row.Reference != "gateway" || len(row.Clients) != 1 || row.Clients[0] != "node-1" {
		t.Fatalf("legacy row changed during migration: %+v", row)
	}
}
