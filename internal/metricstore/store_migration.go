package metricstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	logger "github.com/Aone2233/nekomari/utils/log"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/metric"
	"gorm.io/gorm"
)

// store_migration.go
//
// 面向 WebUI/API 的“存储后端迁移”支撑：把一个 metrics 源库（默认本地 SQLite
// ./data/metrics.db）中的全部指标数据搬运到当前正在运行的 metrics 目标库
// （通常是用户新配置并热重载后的 MySQL/PostgreSQL）。
//
// 此迁移仅能由管理员在 WebUI 中「切换数据库后」手动触发，异步执行并可查询进度 /
// 取消。服务启动和指标库热重载均不会复制历史数据。迁移保留已有目标数据；
// 同一桶内容或指标定义冲突时停止，不再盲目覆盖。跨批次不是原子操作。
//
// 典型流程：
//  1. 管理员在设置中把 metric_db_dsn 改为 MySQL/PostgreSQL，保存 → 后端连接测试
//     通过后热重载，当前 store 切到远端（此时远端为空）。
//  2. 管理员调用 startMetricMigration（不带 source 参数），系统以上一个 metrics
//     目标（默认 SQLite metrics.db）为源，把历史数据搬运到远端。
//  3. 完成后登记目标指纹，供下一次手动迁移默认选择源库。

// StoreMigrationProgress 描述一次 store-to-store 迁移的实时进度。
type StoreMigrationProgress struct {
	Status         string    `json:"status"`          // idle, running, completed, failed, canceled
	SourceDriver   string    `json:"source_driver"`   // 源库驱动
	SourceDSN      string    `json:"source_dsn"`      // 源库 DSN（脱敏后）
	TargetDriver   string    `json:"target_driver"`   // 目标库驱动
	TargetDSN      string    `json:"target_dsn"`      // 目标库 DSN（脱敏后）
	TotalMetrics   int       `json:"total_metrics"`   // 指标定义总数
	CurrentMetric  string    `json:"current_metric"`  // 当前正在搬运的指标名
	MetricsDone    int       `json:"metrics_done"`    // 已完成的指标数
	MigratedPoints int64     `json:"migrated_points"` // 已搬运的采样点数
	StartTime      time.Time `json:"start_time"`      // 开始时间
	EndTime        time.Time `json:"end_time"`        // 结束时间
	Error          string    `json:"error,omitempty"` // 错误信息
}

var (
	storeMigMu       sync.Mutex
	storeMigProgress StoreMigrationProgress
	storeMigCancel   context.CancelFunc
	storeMigDone     chan struct{}
	storeClosing     bool
)

// IsStoreMigrationRunning 报告是否有 store-to-store 迁移正在运行。
func IsStoreMigrationRunning() bool {
	storeMigMu.Lock()
	defer storeMigMu.Unlock()
	return storeMigCancel != nil
}

// GetStoreMigrationProgress 返回当前 store-to-store 迁移进度快照。
func GetStoreMigrationProgress() StoreMigrationProgress {
	storeMigMu.Lock()
	defer storeMigMu.Unlock()
	return storeMigProgress
}

// ResolveStoreMigrationSourceFingerprint 推断本次 store 迁移的源库指纹。
//
// 优先级：
//  1. 显式传入的 driver+dsn（driver 可留空，交由 DSN 推断）。
//  2. 已保存的上一个 metrics 目标指纹（MigrationTargetKey）。
//  3. 默认 SQLite（./data/metrics.db）。
func ResolveStoreMigrationSourceFingerprint(driver, dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn != "" {
		d := ResolveDriverFromConfig(driver, dsn)
		return fmt.Sprintf("%s|%s", d, dsn)
	}
	if saved, _ := config.GetAs[string](MigrationTargetKey, ""); saved != "" {
		return saved
	}
	return defaultSQLiteFingerprint()
}

// StartStoreMigration 异步启动一次 store-to-store 迁移：把源库（由 sourceDriver +
// sourceDSN 指定，留空则自动推断）的全部指标数据搬运到当前运行中的 metrics 目标库。
//
// 返回错误表示“未能启动”（例如已有迁移在跑、源与目标相同、目标未初始化等）；
// 迁移过程中的错误通过 GetStoreMigrationProgress().Status == "failed" 与 Error 暴露。
func StartStoreMigration(sourceDriver, sourceDSN string) error {
	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return fmt.Errorf("failed to load metric store config: %w", err)
	}
	targetFP := targetFingerprint(cfg)
	sourceFP := ResolveStoreMigrationSourceFingerprint(sourceDriver, sourceDSN)

	if sourceFP == targetFP {
		return fmt.Errorf("source and target metrics database are the same; nothing to migrate. "+
			"The inferred source is the last recorded target (config key %q), which equals the current "+
			"target once a migration has completed — pass the source driver and DSN explicitly to migrate "+
			"from an older database", MigrationTargetKey)
	}

	srcCfg, err := configFromFingerprint(sourceFP, cfg)
	if err != nil {
		return fmt.Errorf("resolve source metrics store: %w", err)
	}
	if !pingIdentityOperations.TryAcquire() {
		return ErrStoreBusy
	}
	identityLeaseHandedOff := false
	defer func() {
		if !identityLeaseHandedOff {
			pingIdentityOperations.Release()
		}
	}()
	// Drain observations accepted before the identity fence. Otherwise an old
	// queued ping can be written after the target has been reconciled.
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 15*time.Second)
	if err := reportAdmissions.Acquire(flushCtx); err != nil {
		cancelFlush()
		return fmt.Errorf("wait for report admission before metric migration: %w", err)
	}
	admissionsHeld := true
	defer func() {
		if admissionsHeld {
			reportAdmissions.Release()
		}
	}()
	err = FlushReportBatch(flushCtx)
	cancelFlush()
	if err != nil {
		return fmt.Errorf("flush reports before metric migration: %w", err)
	}
	if !storeOperations.TryAcquire() {
		return ErrStoreBusy
	}
	// Later reports may queue while migration holds the store lease. All prior
	// admissions have already drained, and deletions remain identity-fenced.
	reportAdmissions.Release()
	admissionsHeld = false
	exclusiveLeaseHandedOff := false
	defer func() {
		if !exclusiveLeaseHandedOff {
			storeOperations.Release()
		}
	}()

	storeMu.RLock()
	dst := store
	activeFingerprint := storeFingerprint
	storeMu.RUnlock()
	if dst == nil {
		return ErrStoreNotInitialized
	}
	if activeFingerprint != targetFP {
		return fmt.Errorf("active metric store does not match the configured migration target; wait for hot reload and retry")
	}

	storeMigMu.Lock()
	if storeClosing {
		storeMigMu.Unlock()
		return ErrStoreBusy
	}
	if storeMigCancel != nil {
		storeMigMu.Unlock()
		return fmt.Errorf("a store migration is already in progress")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	storeMigCancel = cancel
	storeMigDone = done
	storeMigProgress = StoreMigrationProgress{
		Status:       "running",
		SourceDriver: string(ResolveDriverFromConfig(srcCfg.Driver, srcCfg.DSN)),
		SourceDSN:    maskDSN(srcCfg.DSN),
		TargetDriver: string(ResolveDriverFromConfig(cfg.Driver, cfg.DSN)),
		TargetDSN:    maskDSN(cfg.DSN),
		StartTime:    time.Now().UTC(),
	}
	storeMigMu.Unlock()

	exclusiveLeaseHandedOff = true
	identityLeaseHandedOff = true
	go runStoreMigration(ctx, cancel, done, srcCfg, cfg, dst, targetFP)
	return nil
}

// runStoreMigration 执行实际的搬运逻辑（在独立 goroutine 中）。
func runStoreMigration(ctx context.Context, cancel context.CancelFunc, done chan struct{}, srcCfg *MetricStoreConfig, cfg *MetricStoreConfig, dst *metric.Store, targetFP string) {
	defer func() {
		cancel()
		storeOperations.Release()
		pingIdentityOperations.Release()

		storeMigMu.Lock()
		storeMigCancel = nil
		storeMigDone = nil
		close(done)
		storeMigMu.Unlock()
	}()

	if ResolveDriverFromConfig(srcCfg.Driver, srcCfg.DSN) == metric.DriverSQLite && dst.Driver() == metric.DriverSQLite {
		if err := metric.CheckDistinctSQLiteSourcePath(ctx, dst, srcCfg.DSN); err != nil {
			finishStoreMigration("failed", "cannot establish distinct file-backed SQLite source and destination; preserve both stores and check their paths")
			return
		}
	}
	src, err := openSourceStore(ctx, srcCfg)
	if err != nil {
		// Driver errors can contain the full DSN, its parameters or credentials.
		finishStoreMigration("failed", "failed to open source metrics store; verify its configuration and connectivity")
		return
	}
	defer src.Close()
	// Different SQLite DSNs can still name the same file via symlinks or
	// hard links. Refuse before reconciling or copying either store. This is
	// an identity check, not a lease against external writers or path changes.
	if src.Driver() == metric.DriverSQLite && dst.Driver() == metric.DriverSQLite {
		if err := metric.RequireDistinctSQLiteFiles(ctx, src, dst); err != nil {
			finishStoreMigration("failed", "cannot establish distinct file-backed SQLite source and destination; preserve both stores and check their paths")
			return
		}
	}

	observe := func(currentMetric string, metricIndex, totalMetrics int, addedPoints int64) {
		storeMigMu.Lock()
		storeMigProgress.CurrentMetric = currentMetric
		storeMigProgress.MetricsDone = metricIndex
		storeMigProgress.TotalMetrics = totalMetrics
		storeMigProgress.MigratedPoints += addedPoints
		storeMigMu.Unlock()
	}

	reserved, err := reservedPingTaskIDs(ctx, config.Database())
	if err == nil {
		err = cleanReservedPingHistory(ctx, dst, reserved)
	}
	if err != nil {
		finishStoreMigration("failed", "failed to prepare destination metrics store")
		return
	}
	total, err := migrateBetweenStores(ctx, src, dst, reserved, observe)
	if err == nil {
		// A task can be deleted while migration holds the store lease. Its
		// normal cleanup waits for that lease; refresh before publishing success.
		err = ReconcileReservedPingHistory(ctx, dst, config.Database())
	}
	if err != nil {
		switch {
		case errors.Is(err, metric.ErrCommitUncertain):
			finishStoreMigration("failed", "migration stopped: a database commit outcome is uncertain; preserve both stores and investigate before retrying")
			return
		case errors.Is(err, metric.ErrImportConflict):
			finishStoreMigration("failed", "migration stopped: an existing destination rollup differs from the source; preserve both stores and reconcile before retrying")
			return
		case errors.Is(err, errStoreMigrationDefinitionConflict):
			finishStoreMigration("failed", "migration stopped: a destination metric type, unit or retention differs from the source; preserve both stores and reconcile before retrying")
			return
		}
		if ctx.Err() != nil {
			finishStoreMigration("canceled", "")
			logger.Warnf("metricstore", "[store-migration] canceled after %d points", total)
			return
		}
		finishStoreMigration("failed", "failed to migrate metrics; verify both stores and retry")
		return
	}

	// 搬运成功：登记当前目标指纹，供下一次手动迁移推断源库。
	if err := saveStoreMigrationTarget(targetFP); err != nil {
		logger.Errorf("metricstore", "[store-migration] failed to persist migration target fingerprint")
	}
	finishStoreMigration("completed", "")
	logger.Infof("metricstore", "[store-migration] completed via API (%d points) target_driver=%s", total, ResolveDriverFromConfig(cfg.Driver, cfg.DSN))
}

func saveStoreMigrationTarget(target string) error {
	var event config.ConfigEvent
	err := config.Database().Transaction(func(tx *gorm.DB) error {
		var err error
		event, err = config.SetManyTx(tx, map[string]any{MigrationTargetKey: target})
		return err
	})
	if err == nil {
		config.PublishCommitted(event)
	}
	return err
}

// finishStoreMigration 统一收尾：设置终态状态、结束时间与错误信息。
func finishStoreMigration(status, safeMessage string) {
	storeMigMu.Lock()
	defer storeMigMu.Unlock()
	storeMigProgress.Status = status
	storeMigProgress.EndTime = time.Now().UTC()
	storeMigProgress.Error = safeMessage
	// 完成/取消时把当前指标标记为已全部处理，便于前端进度条归位。
	if status == "completed" && storeMigProgress.TotalMetrics > 0 {
		storeMigProgress.MetricsDone = storeMigProgress.TotalMetrics
	}
}

// CancelStoreMigration 请求取消正在运行的 store-to-store 迁移，并等待其退出。
// 已提交的批次会保留；重试时相同桶是幂等的，冲突或其他错误需要人工处理。
func CancelStoreMigration() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return stopStoreMigration(ctx, true)
}

func stopStoreMigration(ctx context.Context, requireRunning bool) error {
	storeMigMu.Lock()
	cancel := storeMigCancel
	done := storeMigDone
	storeMigMu.Unlock()

	if cancel == nil {
		if requireRunning {
			return fmt.Errorf("no store migration is currently running")
		}
		return nil
	}
	cancel()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for store migration to cancel: %w", ctx.Err())
	}
}

func stopStoreMigrationForClose(ctx context.Context) error {
	storeMigMu.Lock()
	storeClosing = true
	cancel := storeMigCancel
	done := storeMigDone
	storeMigMu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for store migration to cancel: %w", ctx.Err())
	}
}

// StopStoreMigrationForShutdown prevents a new store migration and waits for
// an active one to release its exclusive lease before report batches flush.
func StopStoreMigrationForShutdown(ctx context.Context) error {
	return stopStoreMigrationForClose(ctx)
}

func clearStoreClosing() {
	storeMigMu.Lock()
	storeClosing = false
	storeMigMu.Unlock()
}

func isStoreClosing() bool {
	storeMigMu.Lock()
	defer storeMigMu.Unlock()
	return storeClosing
}

// DSN syntaxes allow credentials in arbitrary parameters, including SQLite
// URIs. Never return any part of a connection string in migration status.
func maskDSN(dsn string) string {
	if strings.TrimSpace(dsn) == "" {
		return ""
	}
	return "[redacted]"
}
