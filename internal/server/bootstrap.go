package server

import (
	"context"
	"fmt"
	"os"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/utils"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/web/backup"
	"github.com/Aone2233/nekomari/web/public"
	"github.com/Aone2233/nekomari/web/upload"
	"github.com/gin-gonic/gin"
)

// prewarmDefaultThemeCacheIfBuiltIn starts the built-in theme's disk-cache warm-up when
// the selected theme is the embedded one.
//
// Separate from Bootstrap so the decision is testable without opening the panel's own
// database: Bootstrap's first steps are ./data recovery and dbcore.Initialize, and a test
// that ran those would be about the database rather than about this decision. theme is the
// configured theme key (config.ThemeKey / Settings.Theme).
func prewarmDefaultThemeCacheIfBuiltIn(theme string) {
	if theme != public.DefaultTheme {
		return
	}
	public.PrewarmDefaultThemeCache()
}

// Bootstrap initializes the data directory, primary database, and settings.
func (a *App) Bootstrap() error {
	// Calibrate the collector before anything large is allocated. The database,
	// the metric store's page caches and the plugin runtimes all come later, and
	// a soft limit installed after them would only constrain the next spike.
	applyMemoryLimit()

	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create theme directory: %w", err)
	}
	if err := os.MkdirAll("./data/plugin", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}
	if err := os.MkdirAll("./data/plugin-data", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create plugin storage directory: %w", err)
	}

	// Reclaim a backup staging file left by a process that died mid-upload
	// before anything else reads ./data. A restore pending in
	// dbcore.Initialize() below clears ./data itself, but only when a
	// backup.zip is actually waiting, so without this an abandoned staging file
	// would keep its bytes forever.
	if files, bytes, err := backup.CleanupStagedUploads(); err != nil {
		logger.Warn("bootstrap", "could not reclaim an abandoned backup staging file",
			"files", files, "error", err.Error())
	} else if files > 0 {
		logger.Info("bootstrap", "reclaimed an abandoned backup staging file",
			"files", files, "bytes", bytes)
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

	// Warm the embedded theme's disk cache while nothing is serving yet: extracting it is
	// ~1080 files / 33 MiB (measured at 300-370ms), and without this all of that lands on
	// the first visitor's request. See public.PrewarmDefaultThemeCache.
	//
	// Only when the selected theme *is* the embedded one. The disk cache holds the embedded
	// archive, which is exactly the default theme; an installed theme is served from
	// ./data/theme/<short> and never reads that cache, so warming for it would be 33 MiB of
	// writes nobody reads. settings.Theme is the same key the routers resolve (config.ThemeKey,
	// default "default") and it is already loaded above, so this adds neither a query nor a
	// write of a default row.
	//
	// Asynchronous and best-effort on purpose: Bootstrap must not wait for the write, and a
	// cache that cannot be built (read-only data directory, full disk) logs once from
	// web/public and falls back to the in-memory archive — no request is affected either way.
	prewarmDefaultThemeCacheIfBuiltIn(a.settings.Theme)

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
