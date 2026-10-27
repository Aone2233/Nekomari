//go:build linux

package server

import (
	"context"
	"os"
	"time"

	"github.com/Aone2233/nekomari/internal/backupwatch"
	"github.com/Aone2233/nekomari/internal/scheduler"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/utils/messageSender"
)

func registerBackupHealthWatch() {
	path := os.Getenv("NEKOMARI_BACKUP_HEALTH_FILE")
	if path == "" {
		return
	}
	watch := new(backupwatch.Watch)
	if err := scheduler.AddContextFunc("backup:offhostHealth", "@every 1m", true, func(context.Context) {
		reportBackupHealth(watch.Check(path, time.Now().UTC(), sendBackupHealthNotice))
	}); err != nil {
		logger.Warn("backup-health", "could not register backup health watcher")
	}
}

func sendBackupHealthNotice(message, title string) error {
	return messageSender.SendOperationalNotice(message, title)
}

func reportBackupHealth(result backupwatch.Result) {
	// Never print raw provider/transport errors: they can contain webhook secrets.
	if result.Changed {
		if result.Reason == "ok" {
			logger.Info("backup-health", "off-host backup checks are fresh", "status", result.Reason)
		} else {
			logger.Warn("backup-health", "off-host backup needs attention", "status", result.Reason)
		}
	}
	if result.Sent {
		logger.Info("backup-health", "backup health notification sent", "status", result.Reason)
	}
	if result.SendFailed {
		logger.Warn("backup-health", "backup health notification unavailable; will retry")
	}
	if result.StateFailed {
		logger.Warn("backup-health", "cannot safely persist backup alert state; inspect local health directory")
	}
}
