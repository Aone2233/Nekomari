package notifier

import (
	"testing"

	"github.com/Aone2233/nekomari/database/models"
)

func TestInvalidLoadScheduleDoesNotRemoveExistingJobs(t *testing.T) {
	manager := &LoadNotificationService{tasks: make(map[int][]models.LoadNotification)}
	valid := []models.LoadNotification{{Id: 1, Interval: 240}}
	if err := manager.Reload(valid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Reload(nil) })
	for _, interval := range []int{0, -1, int(^uint(0) >> 1)} {
		if err := manager.Reload([]models.LoadNotification{{Id: 2, Interval: interval}}); err == nil {
			t.Fatalf("invalid interval %d was accepted", interval)
		}
		if got := manager.tasks[240]; len(got) != 1 || got[0].Id != 1 {
			t.Fatalf("invalid interval %d replaced the previous schedule: %+v", interval, manager.tasks)
		}
	}
}
