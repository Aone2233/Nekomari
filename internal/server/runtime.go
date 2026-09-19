package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/Aone2233/nekomari/database"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/auditlog"
	d_notification "github.com/Aone2233/nekomari/database/notification"
	"github.com/Aone2233/nekomari/database/tasks"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/lifecycle"
	"github.com/Aone2233/nekomari/internal/metricstore"
	"github.com/Aone2233/nekomari/internal/plugin"
	"github.com/Aone2233/nekomari/internal/scheduler"
	"github.com/Aone2233/nekomari/utils/geoip"
	logger "github.com/Aone2233/nekomari/utils/log"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/Aone2233/nekomari/utils/notifier"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/oauth"
	recoveryweb "github.com/Aone2233/nekomari/web/recovery"
	"github.com/Aone2233/nekomari/web/router"
	"github.com/Aone2233/nekomari/web/security"
)

// ErrRestartRequested is returned after a clean shutdown when a configuration
// change requires the next startup to enter a restricted guide.
var ErrRestartRequested = errors.New("server restart requested")

const (
	// Give in-flight HTTP requests time to finish before the listener closes.
	httpShutdownTimeout = 10 * time.Second
	// Cap how long a client may take to send request headers, so a half-open
	// connection cannot pin a worker indefinitely. WebSocket connections are not
	// affected: this bounds only the initial header read.
	httpReadHeaderTimeout = 10 * time.Second
	// Keep an independent budget for report flushing and store teardown. Reusing
	// the HTTP deadline here can skip queued metric writes after a slow request.
	resourceCleanupTimeout = 30 * time.Second
)

// StartBackground starts scheduled work after all stores are ready.
func (a *App) StartBackground() error {
	registerScheduledWork()
	a.addCleanup("scheduler", func(context.Context) error {
		scheduler.StopAll()
		return nil
	})
	return nil
}

func (a *App) registerReloadHandlers(cors *security.CorsController) {
	a.reload.Register("oauth-provider", func(event config.ConfigEvent) {
		if ok, providerName := config.IsChangedT[string](event, config.OAuthProviderKey); ok {
			if providerName == "" || providerName == "none" {
				providerName = "github"
			}
			oidcProvider, err := database.GetOidcConfigByName(providerName)
			if err != nil {
				logger.Errorf("server", "Failed to get OIDC provider config: %v", err)
				return
			}
			logger.Infof("server", "Using %s as OIDC provider", oidcProvider.Name)
			if err := oauth.LoadProvider(oidcProvider.Name, oidcProvider.Addition); err != nil {
				auditlog.EventLog("error", fmt.Sprintf("Failed to load OIDC provider: %v", err))
			}
		}
	})
	a.reload.Register("geoip-provider", func(event config.ConfigEvent) {
		if event.IsChanged(config.GeoIpProviderKey) {
			go geoip.InitGeoIp()
		}
	})
	a.reload.Register("message-sender", func(event config.ConfigEvent) {
		if event.IsChanged(config.NotificationMethodKey) {
			go messageSender.Initialize()
		}
	})
	a.reload.Register("cors", func(event config.ConfigEvent) { cors.Update(event) })
}

// trustedProxyCIDRs 是允许其转发头（X-Forwarded-For / X-Real-IP）被采信的直连来源。
//
// Gin 默认信任【所有】代理，于是任何能直连面板的人都能用 X-Forwarded-For 伪造
// ClientIP —— 而它被用于会话记录、审计日志与访客限流。这里只信任回环与私有网段：
// 参考部署里 nginx 在同机回环上，Docker / 局域网代理也落在私有网段，而公网直连
// 的来源不被采信，伪造头会被忽略。
//
// 参考部署（nginx 反代）无需额外配置；若代理来自这些网段之外，需要把它加进来。
var trustedProxyCIDRs = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
}

func configureTrustedProxies(engine *gin.Engine) {
	_ = engine.SetTrustedProxies(trustedProxyCIDRs)
}

// BuildRouter constructs the normal application router and starts reloads.
func (a *App) BuildRouter() error {
	r := gin.New()
	configureTrustedProxies(r)
	r.Use(logger.GinLogger(), logger.GinRecovery())
	cors := security.NewCorsController(a.settings.CorsOriginCheckEnabled, a.settings.CorsAllowedOrigins)
	r.Use(cors.Middleware(), api.IdentityMiddleware(), api.PrivateSiteMiddleware(), noStoreAPIResponses())

	// The recovery UI belongs only to its temporary restricted listener.
	r.GET(recoveryweb.PagePath, func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/")
	})
	router.Register(r)

	// Plugins are loaded after the router exists so server.route can bind
	// routes; a failed plugin only disables itself and is logged.
	plugin.Init(r)
	if err := plugin.LoadAll(); err != nil {
		logger.ErrorArgs("server", "Failed to load some plugins:", err)
	}
	a.addCleanup("plugins", func(context.Context) error { return plugin.CloseAll() })

	a.registerReloadHandlers(cors)
	a.reload.Start()
	a.engine = r
	return nil
}

// Run starts the normal HTTP server and blocks until shutdown or fatal error.
func (a *App) Run() error {
	// The HTML injector runs outside the hook chain so it sees the final
	// response: plugin hooks can still rewrite the body, then the registered
	// head/body fragments are embedded into every text/html page.
	a.server = &http.Server{
		Addr:              a.listenAddr,
		Handler:           plugin.HTMLInjectHandler(plugin.WrapHandler(a.engine)),
		ReadHeaderTimeout: httpReadHeaderTimeout,
	}
	serverErr := make(chan error, 1)
	logger.Infof("server", "Starting server on %s ...", a.listenAddr)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)
	select {
	case err := <-serverErr:
		a.onFatal(err)
		return fmt.Errorf("listen: %w", err)
	case reason := <-lifecycle.RestartRequests():
		logger.Infof("server", "Restarting service for %s", reason)
		if err := a.Shutdown(); err != nil {
			logger.Errorf("server", "Cleanup before restart failed: %v", err)
		}
		return fmt.Errorf("%w: %s", ErrRestartRequested, reason)
	case <-quit:
		return a.Shutdown()
	}
}

// Shutdown stops HTTP first, then releases registered resources in LIFO order.
func (a *App) Shutdown() error {
	if a.dbReady {
		auditlog.Log("", "", "server is shutting down", "info")
	}
	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancelHTTP()
	if a.server != nil {
		if err := a.server.Shutdown(httpCtx); err != nil {
			logger.Infof("server", "HTTP server forced to shutdown: %v", err)
		}
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), resourceCleanupTimeout)
	defer cancelCleanup()
	return a.runCleanups(cleanupCtx)
}

func (a *App) onFatal(err error) {
	if a.dbReady {
		auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), resourceCleanupTimeout)
	defer cancel()
	if cleanupErr := a.runCleanups(ctx); cleanupErr != nil {
		logger.Errorf("server", "Cleanup after fatal server error failed: %v", cleanupErr)
	}
}

func (a *App) runCleanups(ctx context.Context) error {
	var cleanupErrors []error
	for i := len(a.cleanups) - 1; i >= 0; i-- {
		cleanup := a.cleanups[i]
		if err := cleanup.fn(ctx); err != nil {
			logger.Errorf("server", "cleanup %q failed: %v", cleanup.name, err)
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup %q: %w", cleanup.name, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func registerScheduledWork() {
	if err := tasks.ReloadPingSchedule(); err != nil {
		logger.ErrorArgs("server", "Failed to reload ping schedule:", err)
	}
	if err := d_notification.ReloadLoadNotificationSchedule(); err != nil {
		logger.ErrorArgs("server", "Failed to reload load notification schedule:", err)
	}
	if err := scheduler.AddFunc("records:cleanup", "@every 30m", cleanupScheduledData); err != nil {
		logger.ErrorArgs("server", "Failed to add cleanup scheduled task:", err)
	}
	if err := scheduler.AddContextFunc("metrics:compact", "@every 5m", true, compactMetricStore); err != nil {
		logger.ErrorArgs("server", "Failed to add metric compact scheduled task:", err)
	}
	if err := scheduler.AddContextFunc("metrics:retention", "@every 1h", true, cleanupMetricStore); err != nil {
		logger.ErrorArgs("server", "Failed to add metric retention scheduled task:", err)
	}
	if err := scheduler.AddFunc("notifier:traffic", "@every 1m", notifier.CheckTraffic); err != nil {
		logger.ErrorArgs("server", "Failed to add traffic notification task:", err)
	}
	if err := scheduler.AddFunc("notifier:expire", "0 0 9 * * *", notifier.CheckExpireScheduledWork); err != nil {
		logger.ErrorArgs("server", "Failed to add expire notification task:", err)
	}
}

const taskResultRetentionDays = 30

func cleanupScheduledData() {
	before := time.Now().UTC().Add(-24 * time.Hour * taskResultRetentionDays)
	if err := tasks.ClearTaskResultsByTimeBefore(before); err != nil {
		logger.Errorf("server", "Failed to clean expired task results: %v", err)
	}
	auditlog.RemoveOldLogs()
	accounts.RemoveExpiredSessions()
}

func compactMetricStore(ctx context.Context) {
	compactCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	written, err := metricstore.Compact(compactCtx, time.Now().UTC())
	if errors.Is(err, metricstore.ErrCompactInProgress) {
		return
	}
	if err != nil {
		logger.Errorf("server", "Failed to compact metric store after writing %d rollup buckets: %v", written, err)
		return
	}
	if written > 0 {
		logger.Infof("server", "Metric store compacted %d rollup buckets", written)
	}
}

func cleanupMetricStore(ctx context.Context) {
	cleanupCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	deleted, err := metricstore.CleanupExpired(cleanupCtx, time.Now().UTC())
	if errors.Is(err, metricstore.ErrCompactInProgress) {
		return
	}
	if err != nil {
		logger.Errorf("server", "Failed to clean expired metric data after deleting %d rows: %v", deleted, err)
		return
	}
	if deleted > 0 {
		logger.Infof("server", "Metric retention cleanup deleted %d rows", deleted)
	}
}
