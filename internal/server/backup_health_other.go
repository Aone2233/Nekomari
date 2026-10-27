//go:build !linux

package server

import (
	"os"

	logger "github.com/Aone2233/nekomari/utils/log"
)

func registerBackupHealthWatch() {
	if os.Getenv("NEKOMARI_BACKUP_HEALTH_FILE") != "" {
		logger.Warn("backup-health", "off-host backup health monitoring requires Linux")
	}
}
