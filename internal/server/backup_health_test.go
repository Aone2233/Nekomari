//go:build linux

package server

import (
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/backupwatch"
)

func TestBackupHealthLogsOnlyTransitionsAndSanitizedFailures(t *testing.T) {
	logs := captureServerLogs(t)
	reportBackupHealth(backupwatch.Result{Reason: "ok"})
	if logs.Len() != 0 {
		t.Fatal("unchanged health must stay quiet")
	}
	reportBackupHealth(backupwatch.Result{Reason: "backup_stale", Changed: true})
	reportBackupHealth(backupwatch.Result{Reason: "ok", Changed: true, Sent: true})
	reportBackupHealth(backupwatch.Result{SendFailed: true, StateFailed: true})
	for _, expected := range []string{"needs attention", "status=backup_stale", "checks are fresh", "notification sent", "will retry", "persist backup alert state"} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("missing operational log: %s", expected)
		}
	}
}
