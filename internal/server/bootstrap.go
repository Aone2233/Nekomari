package server

import (
	"context"
	"fmt"
	"os"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/web/upload"
	"github.com/gin-gonic/gin"
)

// Bootstrap initializes the data directory, primary database, and settings.
func (a *App) Bootstrap() error {
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create theme directory: %w", err)
	}
	if err := os.MkdirAll("./data/plugin", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}
	if err := os.MkdirAll("./data/plugin-data", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create plugin storage directory: %w", err)
	}

	dbcore.SetVersionID(utils.CurrentVersion + "-" + utils.VersionHash)
	if err := dbcore.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	a.dbReady = true
	a.addCleanup("database", func(context.Context) error { return dbcore.Close() })

	gin.SetMode(gin.ReleaseMode)
	settings, err := config.GetManyAs[config.Settings]()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	a.settings = settings
	uploadCtx, stopUploads := context.WithCancel(context.Background())
	uploadDone := make(chan struct{})
	go func() { defer close(uploadDone); upload.DefaultStore.RunCleanup(uploadCtx) }()
	a.addCleanup("upload-cleanup", func(ctx context.Context) error {
		stopUploads()
		select {
		case <-uploadDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	return nil
}
