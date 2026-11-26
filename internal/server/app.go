package server

import (
	"context"
	"net/http"
	"sync"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/gin-gonic/gin"
)

// cleanupFunc is a cleanup action run during shutdown.
type cleanupFunc struct {
	name            string
	fn              func(ctx context.Context) error
	critical        bool
	requireAllClean bool
}

// App owns the server lifecycle. Individual phases live in focused files:
// bootstrap, metric store, providers, guides, router, and runtime.
type App struct {
	listenAddr              string
	settings                *config.Settings
	engine                  *gin.Engine
	server                  *http.Server
	reload                  *ReloadManager
	dbReady                 bool
	oauthReady              bool
	metricStoreCleanupAdded bool
	guideWorker             interface{ Quiesce(context.Context) error }

	cleanups     []cleanupFunc
	shutdownOnce sync.Once
	shutdownErr  error
	handoffMu    sync.Mutex
	handoffErr   error
}

// Options configures the process-wide application runtime.
type Options struct {
	ListenAddr string
}

// New constructs an empty application. Initialization happens in the
// explicit lifecycle phases called by the command entrypoint.
func New(options Options) *App {
	return &App{listenAddr: options.ListenAddr, reload: NewReloadManager()}
}

// addCleanup registers a cleanup action. Shutdown executes actions in LIFO
// order, mirroring resource initialization order.
func (a *App) addCleanup(name string, fn func(ctx context.Context) error) {
	a.cleanups = append(a.cleanups, cleanupFunc{name: name, fn: fn})
}

// A failed critical cleanup prevents later dependent resources from closing.
// The process may still be terminated externally; this is not crash recovery.
func (a *App) addCriticalCleanup(name string, fn func(ctx context.Context) error) {
	a.cleanups = append(a.cleanups, cleanupFunc{name: name, fn: fn, critical: true})
}

// The final handoff must not execute if even a noncritical cleanup failed.
func (a *App) addCleanHandoff(name string, fn func(ctx context.Context) error) {
	a.cleanups = append(a.cleanups, cleanupFunc{name: name, fn: fn, critical: true, requireAllClean: true})
}

// Guide drain failure is permanent even if a later shutdown can close its
// remaining resources. A restore cannot cross the earlier failed boundary.
func (a *App) failHandoff(err error) {
	a.handoffMu.Lock()
	defer a.handoffMu.Unlock()
	if a.handoffErr == nil {
		a.handoffErr = err
	}
}

func (a *App) handoffFailure() error {
	a.handoffMu.Lock()
	defer a.handoffMu.Unlock()
	return a.handoffErr
}
