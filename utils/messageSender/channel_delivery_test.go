package messageSender

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
)

type recordingChannel struct {
	got chan Notification
}

func (r *recordingChannel) Send(_ context.Context, notification Notification, _ map[string]any) error {
	r.got <- notification
	return nil
}

func TestCallNotificationChannelDispatchesAndUnregisters(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := dbcore.Initialize(); err != nil {
		t.Skipf("isolated database unavailable: %v", err)
	}
	t.Cleanup(func() { _ = dbcore.Close() })

	recorder := &recordingChannel{got: make(chan Notification, 1)}
	if err := RegisterNotificationChannel("fake", "plugin:test:fake", models.Configuration{Type: models.ThemeConfigurationManaged}, recorder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { UnregisterNotificationChannel("fake", recorder) })

	found, err := CallNotificationChannel(context.Background(), "plugin:test:fake", Notification{Title: "title", Message: "body"})
	if err != nil || !found {
		t.Fatalf("call found=%v err=%v", found, err)
	}
	select {
	case got := <-recorder.got:
		if got.Title != "title" || got.Message != "body" {
			t.Fatalf("notification = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not invoked")
	}

	UnregisterNotificationChannel("fake", recorder)
	found, err = CallNotificationChannel(context.Background(), "plugin:test:fake", Notification{})
	if found || err != nil {
		t.Fatalf("unregistered channel found=%v err=%v", found, err)
	}
}
