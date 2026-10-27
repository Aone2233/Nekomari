//go:build linux

package backupwatch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (string, time.Time) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, "ovh.json"), time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
}

func writeReport(t *testing.T, path string, now time.Time, status string, age time.Duration) {
	t.Helper()
	report := Report{Version: 1, CheckedAt: now, ReceivedAt: now, Status: status}
	if status == "ok" {
		created := now.Add(-age)
		report.BackupAt = &created
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAssessFreshnessBoundaries(t *testing.T) {
	path, now := fixture(t)
	for _, test := range []struct {
		name   string
		status string
		age    time.Duration
		delay  time.Duration
		want   string
	}{
		{"fresh", "ok", time.Hour, 0, "ok"},
		{"backup boundary", "ok", MaxBackupAge, 0, "ok"},
		{"backup stale", "ok", MaxBackupAge + time.Second, 0, "backup_stale"},
		{"heartbeat boundary", "ok", time.Hour, MaxReportAge, "ok"},
		{"heartbeat stale", "ok", time.Hour, MaxReportAge + time.Second, "report_stale"},
		{"missing archive", "missing", 0, 0, "backup_missing"},
		{"bad archive", "invalid", 0, 0, "backup_invalid"},
		{"future report", "ok", time.Hour, -ClockSkew - time.Second, "report_invalid"},
		{"future backup", "ok", -ClockSkew - time.Second, 0, "report_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeReport(t, path, now, test.status, test.age)
			if got := assess(path, now.Add(test.delay)); got != test.want {
				t.Fatalf("reason = %s, want %s", got, test.want)
			}
		})
	}
}

func TestMalformedAndUnsafeReports(t *testing.T) {
	path, now := fixture(t)
	if got := assess(path, now); got != "report_missing" {
		t.Fatal(got)
	}
	for _, raw := range []string{"null", "{}", `{"version":1,"secret":"do-not-log"}`, strings.Repeat(" ", 4097), "{} {}"} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if got := assess(path, now); got != "report_invalid" {
			t.Fatalf("invalid payload accepted: %s", got)
		}
	}
	writeReport(t, path, now, "ok", time.Hour)
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if got := assess(path, now); got != "report_invalid" {
		t.Fatal(got)
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got := assess(link, now); got != "report_invalid" {
		t.Fatal(got)
	}
}

func TestIncidentDedupeRecoveryAndRestart(t *testing.T) {
	path, now := fixture(t)
	var messages []string
	send := func(message, title string) error { messages = append(messages, title+message); return nil }
	watch := new(Watch)
	writeReport(t, path, now, "ok", time.Hour)
	if result := watch.Check(path, now, send); result.Reason != "ok" || result.Sent || result.StateFailed {
		t.Fatal(result)
	}
	writeReport(t, path, now, "ok", 27*time.Hour)
	if result := watch.Check(path, now, send); !result.Sent || result.SendFailed || result.StateFailed {
		t.Fatal(result)
	}
	watch = new(Watch)
	if result := watch.Check(path, now.Add(time.Minute), send); result.Sent || result.StateFailed {
		t.Fatal(result)
	}
	writeReport(t, path, now.Add(2*time.Minute), "ok", 0)
	if result := watch.Check(path, now.Add(2*time.Minute), send); !result.Sent || result.Reason != "ok" {
		t.Fatal(result)
	}
	if result := watch.Check(path, now.Add(3*time.Minute), send); result.Sent {
		t.Fatal(result)
	}
	if len(messages) != 2 || !strings.Contains(messages[0], "backup_stale") || !strings.Contains(messages[1], "recovered") {
		t.Fatal(messages)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(path), "alert-state.json"))
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("state must remain private", err)
	}
}

func TestNotificationFailureRetryAndNoFalseRecovery(t *testing.T) {
	path, now := fixture(t)
	watch := new(Watch)
	calls := 0
	fail := func(string, string) error { calls++; return errors.New("https://test.invalid/private-token") }
	writeReport(t, path, now, "invalid", 0)
	if result := watch.Check(path, now, fail); !result.SendFailed || result.Sent {
		t.Fatal(result)
	}
	if result := new(Watch).Check(path, now.Add(time.Minute), fail); result.SendFailed || result.Sent {
		t.Fatal(result)
	}
	if result := watch.Check(path, now.Add(retryDelay), fail); !result.SendFailed || calls != 2 {
		t.Fatal(result, calls)
	}
	state, err := os.ReadFile(filepath.Join(filepath.Dir(path), "alert-state.json"))
	if err != nil || strings.Contains(string(state), "private-token") {
		t.Fatal("provider error leaked", err)
	}
	writeReport(t, path, now, "ok", 0)
	if result := watch.Check(path, now, fail); result.Sent || result.SendFailed {
		t.Fatal("do not notify recovery when no incident delivery succeeded", result)
	}
}

func TestCorruptStateRefusesRepeatedNotification(t *testing.T) {
	path, now := fixture(t)
	writeReport(t, path, now, "missing", 0)
	state := filepath.Join(filepath.Dir(path), "alert-state.json")
	if err := os.WriteFile(state, []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	result := new(Watch).Check(path, now, func(string, string) error { t.Fatal("unsafe state must not send"); return nil })
	if !result.StateFailed || result.Sent {
		t.Fatal(result)
	}
}

func TestConcurrentChecksDoNotQueueNotifications(t *testing.T) {
	path, now := fixture(t)
	writeReport(t, path, now, "missing", 0)
	entered, release := make(chan struct{}), make(chan struct{})
	watch := new(Watch)
	done := make(chan Result, 1)
	go func() {
		done <- watch.Check(path, now, func(string, string) error { close(entered); <-release; return nil })
	}()
	<-entered
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() {
			result := watch.Check(path, now, func(string, string) error { t.Error("duplicate send"); return nil })
			if result != (Result{}) {
				t.Error(result)
			}
		})
	}
	group.Wait()
	close(release)
	if result := <-done; !result.Sent {
		t.Fatal(result)
	}
}
