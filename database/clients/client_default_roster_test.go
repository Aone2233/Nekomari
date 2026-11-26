package clients

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestClientCreationOrdersDefaultRosterAndDeletion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan string, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, _, err := createClientWith(db, "ordered", func(_ context.Context, db *gorm.DB, client *models.Client) (bool, error) {
			if err := db.Create(client).Error; err != nil {
				return false, err
			}
			entered <- client.UUID
			<-release
			return false, nil
		}, func() error { t.Error("unchanged assignment reloaded"); return nil })
		done <- err
	}()
	id := <-entered
	for name, acquire := range map[string]func(context.Context, func() error) error{
		"roster": pingguard.WithDefaultRoster,
		"delete": func(ctx context.Context, fn func() error) error { return clientguard.With(ctx, id, fn) },
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		err := acquire(ctx, func() error { t.Errorf("%s crossed creation fence", name); return nil })
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s guard error=%v", name, err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var got models.Client
	if err := db.Take(&got, "uuid = ?", id).Error; err != nil || got.Name != "ordered" {
		t.Fatalf("client creation did not commit: %+v err=%v", got, err)
	}
	if err := pingguard.WithDefaultRoster(t.Context(), func() error {
		return clientguard.With(t.Context(), id, func() error { return nil })
	}); err != nil {
		t.Fatalf("creation fences did not release: %v", err)
	}
}
