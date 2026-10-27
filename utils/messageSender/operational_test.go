package messageSender

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/utils/messageSender/factory"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type operationalTestSender struct {
	name    string
	err     error
	calls   int
	entered chan struct{}
	release chan struct{}
}

func (s *operationalTestSender) GetName() string                         { return s.name }
func (s *operationalTestSender) GetConfiguration() factory.Configuration { return &struct{}{} }
func (s *operationalTestSender) Init() error                             { return nil }
func (s *operationalTestSender) Destroy() error                          { return nil }
func (s *operationalTestSender) SendTextMessage(string, string) error {
	s.calls++
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	return s.err
}

func TestOperationalNoticeAvailabilityAndErrorRedaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settings.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config.SetDb(db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	previous := currentProvider
	t.Cleanup(func() { currentProvider = previous })
	sender := &operationalTestSender{name: "test"}
	currentProvider = sender
	setEnabled := func(raw string) {
		t.Helper()
		if err := db.Save(&config.ConfigItem{Key: config.NotificationEnabledKey, Value: raw}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"false", `"wrong-type"`} {
		setEnabled(raw)
		if err := SendOperationalNotice("test", "test"); err == nil || sender.calls != 0 {
			t.Fatal("disabled or invalid configuration is not successful delivery")
		}
	}
	setEnabled("true")
	currentProvider = nil
	if err := SendOperationalNotice("test", "test"); err == nil {
		t.Fatal("nil provider acknowledged")
	}
	currentProvider = &operationalTestSender{name: "empty"}
	if err := SendOperationalNotice("test", "test"); err == nil {
		t.Fatal("empty provider acknowledged")
	}
	currentProvider = sender
	sender.err = errors.New("https://test.invalid/private-secret")
	if err := SendOperationalNotice("test", "test"); err == nil || strings.Contains(err.Error(), "private-secret") || sender.calls != 1 {
		t.Fatal("provider error must be sanitized and attempted once")
	}
	sender.err = nil
	if err := SendOperationalNotice("test", "test"); err != nil || sender.calls != 2 {
		t.Fatal(err)
	}
	// A reload/destroy cannot overtake an in-flight operational send.
	sender.entered, sender.release = make(chan struct{}), make(chan struct{})
	var group sync.WaitGroup
	group.Go(func() {
		if err := SendOperationalNotice("test", "test"); err != nil {
			t.Error(err)
		}
	})
	<-sender.entered
	if mu.TryLock() {
		mu.Unlock()
		t.Error("provider lifetime lock not held during delivery")
	}
	close(sender.release)
	group.Wait()
}
