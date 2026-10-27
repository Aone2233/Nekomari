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

	"github.com/Aone2233/nekomari/database"
	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/database/dbcore"
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
	agentruntime "github.com/Aone2233/nekomari/web/agent"
	"github.com/Aone2233/nekomari/web/api"
	"github.com/Aone2233/nekomari/web/oauth"
	recoveryweb "github.com/Aone2233/nekomari/web/recovery"
	"github.com/Aone2233/nekomari/web/router"
	"github.com/Aone2233/nekomari/web/security"
	"github.com/gin-gonic/gin"
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
	// Retire an idle keep-alive connection. Without this the server never closes one: Go's
	// IdleTimeout defaults to 0, which falls back to ReadTimeout - also 0 here - so a client or a
	// proxy's pooled upstream connection keeps a goroutine and its read/write buffers for the life
	// of the process. 120 s is longer than the only long poll the panel has (the agent's 25 s pull)
	// and does not apply to WebSockets, whose connection is hijacked before this timeout matters.
	httpIdleTimeout = 120 * time.Second
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
		reloadGeoIP(event, geoip.InitGeoIp)
	})
	a.reload.Register("message-sender", func(event config.ConfigEvent) {
		if event.IsChanged(config.NotificationMethodKey) {
			go messageSender.Initialize()
		}
	})
	a.reload.Register("cors", func(event config.ConfigEvent) { cors.Update(event) })
}

func reloadGeoIP(event config.ConfigEvent, initialize func()) {
	if event.IsChanged(config.GeoIpProviderKey) || event.IsChanged(config.GeoIpEnabledKey) {
		go initialize()
	}
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
	r.Use(cors.Middleware(), api.ControlBodyLimit(), api.IdentityMiddleware(), api.PrivateSiteMiddleware(), noStoreAPIResponses())

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

// newHTTPServer is the one place the panel's HTTP timeouts are chosen, so the guided modes and the
// normal server cannot drift apart. WriteTimeout is deliberately absent: it would cut file downloads
// and terminal streams, which have their own deadlines.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}

// Run starts the normal HTTP server and blocks until shutdown or fatal error.
func (a *App) Run() error {
	// The HTML injector runs outside the hook chain so it sees the final
	// response: plugin hooks can still rewrite the body, then the registered
	// head/body fragments are embedded into every text/html page.
	a.server = newHTTPServer(a.listenAddr, plugin.HTMLInjectHandler(plugin.WrapHandler(a.engine)))
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
	// The traffic forecast: warns when a node is *projected* to reach its allowance, rather than once it
	// has used a percentage of it. A node at 60% on day five of a cycle is a problem and one at 60% on day
	// twenty-nine is not, and the used percentage alone cannot tell the two apart. It runs on the same
	// minute as the used-percentage check so the two cannot disagree about which scan is which.
	if err := scheduler.AddFunc("notifier:trafficForecast", "@every 1m", notifier.CheckTrafficForecast); err != nil {
		logger.ErrorArgs("server", "Failed to add traffic forecast task:", err)
	}
	// Deferred offline alerts: a maintenance window suppresses an alert but must not swallow it,
	// so the ones it deferred are reconsidered once their window has closed. On the existing
	// scheduler rather than a goroutine of its own, so there is one place that owns periodic work
	// and one place to look when something is not running.
	if err := scheduler.AddFunc("notifier:sweepDeferred", "@every 1m", notifier.SweepDeferredAlerts); err != nil {
		logger.ErrorArgs("server", "Failed to add deferred alert task:", err)
	}
	if err := scheduler.AddFunc("agent:sweepEventQueues", "@every 1m", agentruntime.SweepV2EventQueues); err != nil {
		logger.ErrorArgs("server", "Failed to add agent event queue cleanup task:", err)
	}
	// 主库完整性日检。启动时那次检查只回答「这个实例带着什么状态启动」；跑上几周之后，
	// 库被外部改过、索引被人 DROP、磁盘出现坏页，启动那次都看不到。每日一次足够发现它们，
	// 而 AddFunc 不立即执行 —— 第一次在注册 24 小时后，不会与启动检查在同一分钟里各写一份。
	// 用固定间隔而不是固定墙钟时刻：面板可能在任何时刻启动，固定时刻只会让第一次检查等更久，
	// 而这个任务不需要与任何外部事件对齐。
	if err := scheduler.AddFunc("db:integrity", scheduler.Every(mainDatabaseIntegrityInterval), mainDatabaseIntegrityCheck); err != nil {
		logger.ErrorArgs("server", "Failed to add main database integrity scheduled task:", err)
	}
	// 升级归档的每日清理。v1.6.19 的保留策略只在「刚写完一份新归档」时生效，长期不升级的
	// 面板永远走不到那条路径，data/backup 里的旧归档就会一直留着。与 db:integrity 并列：
	// 同样每日一次、同样不立即执行（第一次在注册 24 小时后），也同样是纯文件操作、不碰库。
	if err := scheduler.AddFunc("backup:pruneUpgradeArchives", scheduler.Every(upgradeArchivePruneInterval), pruneUpgradeArchivesCheck); err != nil {
		logger.ErrorArgs("server", "Failed to add upgrade archive prune scheduled task:", err)
	}
}

// upgradeArchivePruneInterval 是升级归档清理的周期。
//
// 24 小时：归档只在版本变化时新增，清理本身是幂等的文件删除（几毫秒），频率再高也只是
// 重复扫同一个目录。每日一次既能让长期不升级的面板也把旧归档收敛到保留数，又不会给日志
// 每天多加一行以上的东西。
const upgradeArchivePruneInterval = 24 * time.Hour

// pruneUpgradeArchivesCheck 是注册到调度器的清理入口。
func pruneUpgradeArchivesCheck() {
	reportUpgradeArchivePrune(dbcore.RunUpgradeArchivePrune)
}

// reportUpgradeArchivePrune 把一次清理结果写成日志：成功记 Info（删了多少、现在剩多少），
// 失败记 Warn 并且什么都不做 —— 清理失败不该影响服务，下一个周期会再试。
//
// run 作为参数只为测试：这里要钉的是「结果怎么落到日志级别与数字上」，磁盘行为由
// database/dbcore 的用例覆盖。注册那一行没有单测（调度器 job 表没有 introspection，而
// registerScheduledWork 还注册了依赖数据库与配置的其它任务），覆盖边界是它注册的函数。
func reportUpgradeArchivePrune(run func() (dbcore.UpgradeArchivePruneResult, error)) {
	result, err := run()
	if err != nil {
		logger.Warnf("server", "Could not prune old upgrade archives: %v", err)
		return
	}
	logger.Infof("server",
		"Upgrade archive retention: removed %d old archive(s), %d kept in %s (keep %d)",
		result.Removed, result.Remaining, result.Dir, result.Keep)
}

// mainDatabaseIntegrityInterval 是主库完整性检查的周期。
//
// 24 小时：quick_check 要读完整个库文件，而主库只有一条连接（dbcore 里 SetMaxOpenConns(1)），
// 检查期间其它查询会排在它后面 —— 每天一次是「能发现运行期变化」与「检查本身不是负载」
// 之间的折中。启动时另有一次，两者互补而不是重复：那次看的是启动状态，这次看的是运行状态。
const mainDatabaseIntegrityInterval = 24 * time.Hour

// mainDatabaseIntegrityCheck 是注册到调度器的日检入口。
func mainDatabaseIntegrityCheck() {
	reportMainDatabaseIntegrity(dbcore.RunMainDatabaseQuickCheck)
}

// reportMainDatabaseIntegrity 把一次检查结果写成日志。
//
// run 作为参数传入只是为了测试：这里真正容易错的是「什么算 ok、什么算问题、用哪个级别」，
// 而那不需要真实数据库就能验证（真实检查由 database/dbcore 的用例覆盖）。注册那一步
// （scheduler.AddFunc）没有对应的单元测试：调度器的 job 表没有 introspection，而
// registerScheduledWork 同时注册了依赖数据库与配置的其它任务，单独把它拉进测试只会把
// 整条启动链拖进来。这里覆盖的是它注册的那个函数。
func reportMainDatabaseIntegrity(run func() (dbcore.MainDatabaseIntegrityReport, error)) {
	report, err := run()
	if err != nil {
		// 日检失败（库还没就绪、查询被打断）不等于服务有问题：这是低频检查，下一个周期
		// 会再试一次，所以只记 Warn，不影响任何请求路径。
		logger.Warnf("server", "Daily main database integrity check could not run: %v", err)
		return
	}
	if report.QuickCheckErr != nil {
		logger.Errorf("server",
			"Daily main database integrity check found a structural problem: %v — the panel keeps running; back up the database, run PRAGMA integrity_check, and restore it if the problem is real",
			report.QuickCheckErr)
	}
	if report.IndexCheckErr != nil {
		logger.Warnf("server", "Daily main database integrity check could not verify the authentication indexes: %v", report.IndexCheckErr)
	}
	for _, problem := range report.MissingIndexes {
		logger.Errorf("server",
			"Daily main database integrity check: the authentication path is missing an index: %s. Queries still return the right rows but fall back to a full table scan; rebuild the indexes (REINDEX) after taking a backup",
			problem)
	}
	if report.OK() {
		logger.Infof("server", "Daily main database integrity check passed (PRAGMA quick_check ok, authentication indexes present)")
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
