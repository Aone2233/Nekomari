package notifier

import (
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	messageevent "github.com/Aone2233/nekomari/database/models/messageEvent"
)

func TestMaintenanceEndingBeforeGraceDoesNotSendEarly(t *testing.T) {
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate()
	t.Cleanup(Invalidate)

	clientID := notifierTestNode(t, "window-shorter-than-grace")
	t.Cleanup(func() { clientStates.Delete(clientID) })
	if err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Where("client = ?", clientID).Update("grace_period", 600).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	windowEnd := now.Add(time.Minute)
	notifierTestWindow(t, "short window", now.Add(-time.Minute), windowEnd, clientID)
	Invalidate()
	updateOnlineState(clientID, 1001)
	OfflineNotification(clientID, 1001)

	state := getOrInitState(clientID)
	state.mu.Lock()
	deadline := state.pendingOfflineSince.Add(10 * time.Minute)
	state.mu.Unlock()

	sweepDeferredAlerts(windowEnd.Add(time.Second))
	if sent := testRecorder.sent(); len(sent) != 0 {
		t.Fatalf("sent %d alerts before the grace period ended", len(sent))
	}
	if queueLen() != 1 {
		t.Fatal("the still-offline node must remain queued until its grace period ends")
	}

	sweepDeferredAlerts(deadline)
	sent := testRecorder.sent()
	if len(sent) != 1 || sent[0].Event != messageevent.Offline {
		t.Fatalf("expected one offline alert after the grace period, got %+v", sent)
	}
}

func TestMaintenanceBeginningDuringGraceDefersAlert(t *testing.T) {
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate()
	t.Cleanup(Invalidate)

	clientID := notifierTestNode(t, "window-starts-during-grace")
	t.Cleanup(func() { clientStates.Delete(clientID) })
	updateOnlineState(clientID, 1002)
	OfflineNotification(clientID, 1002)

	now := time.Now().UTC()
	windowEnd := now.Add(time.Minute)
	notifierTestWindow(t, "started during grace", now.Add(-time.Second), windowEnd, clientID)
	Invalidate()

	deadline := time.Now().Add(3 * time.Second)
	for queueLen() == 0 && len(testRecorder.sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if sent := testRecorder.sent(); len(sent) != 0 {
		t.Fatalf("sent an offline alert inside the maintenance window: %+v", sent)
	}
	if queueLen() != 1 {
		t.Fatal("the offline alert was not deferred after the grace period")
	}

	sweepDeferredAlerts(windowEnd.Add(time.Second))
	sent := testRecorder.sent()
	if len(sent) != 1 || sent[0].Event != messageevent.Offline {
		t.Fatalf("expected one offline alert after maintenance, got %+v", sent)
	}
}

func TestReconnectDuringDeferredGraceRecordsConnection(t *testing.T) {
	clientID := uniqueNode("reconnect-during-deferred-grace")
	t.Cleanup(func() { clientStates.Delete(clientID) })
	updateOnlineState(clientID, 1003)
	state := getOrInitState(clientID)
	state.mu.Lock()
	state.pendingOfflineSince = time.Now().UTC()
	state.isConnExist = false
	state.mu.Unlock()

	if updateOnlineState(clientID, 1004) {
		t.Fatal("a reconnect before an offline alert must not send a recovery alert")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.isConnExist || !state.pendingOfflineSince.IsZero() || state.connectionID != 1004 {
		t.Fatalf("reconnect did not record the current connection: %+v", state)
	}
}

func TestReconnectCancelsDeferredOutageAndAllowsANewOne(t *testing.T) {
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate()
	t.Cleanup(Invalidate)

	clientID := notifierTestNode(t, "window-reconnect-new-outage")
	t.Cleanup(func() { clientStates.Delete(clientID) })
	if err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Where("client = ?", clientID).Update("grace_period", 600).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	notifierTestWindow(t, "short window", now.Add(-time.Minute), now.Add(time.Minute), clientID)
	Invalidate()

	updateOnlineState(clientID, 1005)
	OfflineNotification(clientID, 1005)
	OnlineNotification(clientID, 1006)
	if queueLen() != 0 || len(testRecorder.sent()) != 0 {
		t.Fatal("reconnect before grace expiry must cancel the old outage without notification")
	}
	OfflineNotification(clientID, 1006)
	state := getOrInitState(clientID)
	state.mu.Lock()
	deadline := state.pendingOfflineSince.Add(10 * time.Minute)
	state.mu.Unlock()
	if queueLen() != 1 {
		t.Fatal("the new outage must have its own deferred alert")
	}
	sweepDeferredAlerts(deadline.Add(-time.Nanosecond))
	if len(testRecorder.sent()) != 0 || queueLen() != 1 {
		t.Fatal("the new outage must also observe its full grace period")
	}
	sweepDeferredAlerts(deadline)
	sent := testRecorder.sent()
	if len(sent) != 1 || sent[0].Event != messageevent.Offline {
		t.Fatalf("expected one alert for the new outage, got %+v", sent)
	}
}

func TestOrdinaryGraceReconnectAndRecovery(t *testing.T) {
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate()
	t.Cleanup(Invalidate)

	clientID := notifierTestNode(t, "ordinary-grace-reconnect")
	t.Cleanup(func() { clientStates.Delete(clientID) })
	updateOnlineState(clientID, 1007)
	OfflineNotification(clientID, 1007)
	OnlineNotification(clientID, 1008)
	time.Sleep(1200 * time.Millisecond)
	if sent := testRecorder.sent(); len(sent) != 0 {
		t.Fatalf("a reconnect inside grace must not send either alert: %+v", sent)
	}

	OfflineNotification(clientID, 1008)
	deadline := time.Now().Add(3 * time.Second)
	for len(testRecorder.sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if sent := testRecorder.sent(); len(sent) != 1 || sent[0].Event != messageevent.Offline {
		t.Fatalf("expected one ordinary offline alert, got %+v", sent)
	}
	OnlineNotification(clientID, 1009)
	for !testRecorder.sawEvent(messageevent.Online) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !testRecorder.sawEvent(messageevent.Online) {
		t.Fatalf("a reconnect after the offline alert must send a recovery: %+v", testRecorder.sent())
	}
}
