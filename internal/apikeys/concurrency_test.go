package apikeys

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestConcurrentSQLiteCreationCannotExceedActiveLimit(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "keys.db") + "?_journal_mode=WAL&_busy_timeout=1000"
	connections := make([]*gorm.DB, 2)
	for index := range connections {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		connections[index] = db
	}
	db := connections[0]
	if err := db.AutoMigrate(&models.APIKey{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index := 0; index < MaxActiveKeys-1; index++ {
		row := models.APIKey{ID: fmt.Sprint(index), Name: "seed", TokenDigest: digest(fmt.Sprint(index)),
			Scopes: []string{rpc.ScopeNodesRead}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := 0; index < 12; index++ {
		workers.Go(func() {
			<-start
			row, token, err := Create(ctx, connections[index%len(connections)], "concurrent", []string{rpc.ScopeNodesRead}, 1, "test")
			if err != nil && (row.ID != "" || token != "") {
				t.Error("failed concurrent create returned credential material")
			}
			if err == nil {
				if _, err := Authenticate(ctx, db, "Bearer "+token); err != nil {
					t.Error("successful create did not commit its credential")
				}
			}
		})
	}
	close(start)
	workers.Wait()
	var count int64
	if err := db.Model(&models.APIKey{}).Where("revoked_at IS NULL AND expires_at > ?", now).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != MaxActiveKeys {
		t.Fatalf("concurrent create count = %d, want %d (SQLite lock errors must not overissue)", count, MaxActiveKeys)
	}
}
