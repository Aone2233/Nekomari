package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/panellock"
	"github.com/Aone2233/nekomari/internal/restorearchive"
)

func resolveOfflineDatabasePath() string {
	if flags.DatabaseFile == "" {
		return filepath.Join("data", "komari.db")
	}
	return flags.DatabaseFile
}

// Administrative commands must not run database initialization, including a
// pending restore, alongside a cooperating server or trigger a restore as a
// side effect of changing an account.
func withOfflineDatabase(action func() error) (err error) {
	lease, err := panellock.Acquire("./data")
	if err != nil {
		return fmt.Errorf("stop the panel before running this command: %w", err)
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	if _, statErr := os.Stat(resolveOfflineDatabasePath()); statErr != nil {
		return fmt.Errorf("account database is unavailable: %w", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join("data", "backup.zip")); statErr == nil {
		return errors.New("a restore is pending; resolve it before running an account command")
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("check pending restore: %w", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join("data", restorearchive.StateFileName)); statErr == nil {
		return errors.New("a restore journal exists; inspect it before running an offline database command")
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("check restore journal: %w", statErr)
	}
	if err := dbcore.Initialize(); err != nil {
		return fmt.Errorf("initialize offline database: %w", err)
	}
	defer func() { err = errors.Join(err, dbcore.Close()) }()
	return action()
}
