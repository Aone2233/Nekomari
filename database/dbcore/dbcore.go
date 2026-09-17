package dbcore

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/internal/migrations"
	"github.com/Aone2233/nekomari/internal/sqlitetune"
	logger "github.com/Aone2233/nekomari/utils/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// zipDirectoryExcluding 将 srcDir 打包为 dstZip，exclude 是绝对路径集合需要排除
func zipDirectoryExcluding(srcDir, dstZip string, exclude map[string]struct{}) error {
	// 规范化排除路径为绝对路径
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}

	out, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	absSrc, _ := filepath.Abs(srcDir)
	walkErr := filepath.Walk(absSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// 排除 backup.zip 本身
		if _, ok := normExclude[path]; ok {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// 计算 zip 内相对路径
		rel, err := filepath.Rel(absSrc, path)
		if err != nil {
			return err
		}
		// 根目录跳过
		if rel == "." {
			return nil
		}
		// 替换为正斜杠
		zipName := filepath.ToSlash(rel)

		if info.IsDir() {
			_, err := zw.Create(zipName + "/")
			return err
		}
		// 普通文件
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(zipName)
		if err != nil {
			fh.Close()
			return err
		}
		if _, err := io.Copy(w, fh); err != nil {
			fh.Close()
			return err
		}
		fh.Close()
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	return zw.Close()
}

// removeAllInDirExcept 删除 dir 下除 exclude 指定绝对路径外的所有文件和文件夹
func removeAllInDirExcept(dir string, exclude map[string]struct{}) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(absDir, e.Name())
		if _, ok := normExclude[full]; ok {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	return nil
}

// unzipToDir 将 zipPath 解压到 dstDir，包含路径遍历保护
func unzipToDir(zipPath, dstDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
	absDst, _ := filepath.Abs(dstDir)

	for _, f := range zr.File {
		// 构造目标路径并做路径遍历保护
		cleanName := filepath.Clean(f.Name)
		targetPath := filepath.Join(absDst, cleanName)
		if !strings.HasPrefix(targetPath, absDst+string(os.PathSeparator)) && targetPath != absDst {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(targetPath)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

var (
	instance *gorm.DB
	once     sync.Once
	initErr  error
)

// SystemVersionKey 是记录“上次启动所用版本标识”的配置键（存于 configs 表）。
// 取代旧的 ./data/.komari-version 文件：版本标识随配置库一起备份/恢复，
// 也避免额外的裸文件依赖。
const SystemVersionKey = "system_version"

const (
	mainSQLiteBusyTimeout       = 5 * time.Second
	mainSQLiteCacheSizeKB       = 8 * 1024
	mainSQLiteWALAutoCheckpoint = 256
	mainSQLiteJournalSizeLimit  = 1 << 20
)

// versionID 是当前构建的版本标识，由 SetVersionID 在 Initialize 前注入。
var versionID string

// dbFileExistedAtStartup 记录本次进程启动、打开数据库之前 komari.db 是否已存在，
// 用于区分“全新安装”与“从旧版升级（无版本标记）”。在 doInitialize 打开数据库
// 之前采集。
var dbFileExistedAtStartup bool

// SetVersionID 设置当前构建的版本标识（通常为 CurrentVersion+"-"+VersionHash），
// 用于版本升级检测与自动备份。应在 Initialize() 之前调用；为空则跳过升级备份。
func SetVersionID(id string) {
	versionID = id
}

// resolveDatabaseFile 返回当前使用的 SQLite 数据库文件路径。
func resolveDatabaseFile() string {
	dbFile := flags.DatabaseFile
	if dbFile == "" {
		dbFile = "./data/komari.db"
	}
	return dbFile
}

// backupOnVersionUpgrade 在检测到版本升级时，把当前 ./data 打包到
// ./data/backup/upgrade-{time}.zip，便于升级（含 metrics 迁移）异常时回滚。
//
// 版本标识存放于配置库（configs 表，键 system_version），因此本函数必须在
// config.SetDb 之后、一次性 metrics 迁移（InitStores）之前调用。
//
// 触发规则：
//   - versionID 为空：跳过（未注入版本，如部分测试场景）。
//   - 配置中无版本且启动前无数据库文件：全新安装，仅写版本，不备份。
//   - 配置中无版本但启动前已有数据库文件：从无版本标记的旧稳定版升级，备份。
//   - 配置中版本与当前不同：版本升级，备份。
//   - 配置中版本与当前一致：无需备份。
//
// 备份失败不阻止启动，但打印明确错误；备份成功（或无需备份）后写入/更新版本。
func backupOnVersionUpgrade() {
	if versionID == "" {
		return
	}

	prevVersion, readErr := config.GetAs[string](SystemVersionKey)
	prevVersion = strings.TrimSpace(prevVersion)
	versionRecorded := readErr == nil && prevVersion != ""

	// 版本未变化，无需备份。
	if versionRecorded && prevVersion == versionID {
		return
	}

	// 全新安装：配置中无版本且启动前无数据库文件，直接写版本不备份。
	if !versionRecorded && !dbFileExistedAtStartup {
		writeVersionMarker()
		return
	}

	// 需要备份（升级或从旧稳定版首次带版本标记启动）。
	// 先做一次 WAL checkpoint，确保 komari.db 主文件包含最新数据，
	// 避免备份出的库缺少仍留在 -wal 中的写入。
	if instance != nil {
		instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	}

	backupDir := filepath.Join(".", "data", "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		logger.Errorf("dbcore", "[upgrade-backup] failed to create backup dir: %v", err)
		return
	}
	tsName := time.Now().UTC().Format("20060102-150405")
	bakPath := filepath.Join(backupDir, fmt.Sprintf("upgrade-%s.zip", tsName))
	backupZipPath := filepath.Join(".", "data", "backup.zip")
	if zipErr := zipDirectoryExcluding("./data", bakPath, map[string]struct{}{backupZipPath: {}, backupDir: {}}); zipErr != nil {
		logger.Errorf("dbcore", "[upgrade-backup] failed to backup ./data before upgrade (from %q to %q): %v", prevVersion, versionID, zipErr)
		return
	}
	logger.Infof("dbcore", "[upgrade-backup] ./data backed up to %s before upgrade (from %q to %q)", bakPath, prevVersion, versionID)

	writeVersionMarker()
}

// writeVersionMarker 将当前 versionID 写入配置库。
func writeVersionMarker() {
	if err := config.Set(SystemVersionKey, versionID); err != nil {
		logger.Errorf("dbcore", "[upgrade-backup] failed to persist version marker: %v", err)
	}
}

// warnIfDataNotPersistent 在数据目录不会跨容器重建保留时发出明确警告。
//
// 为什么需要这个检查：Dockerfile 声明了 VOLUME ["/app/data"]，所以不带 -v 运行
// 时 Docker 会创建一个【匿名卷】。用户按官方方式更新（拉新镜像 → 删除并重建容器）
// 时，新容器拿到的是【另一个】匿名卷，于是面板以空数据目录启动、重新显示安装向导。
// 从用户视角看，这就是「一升级设置就被初始化了」——而数据其实还在，只是成了
// 一个没人引用的孤儿卷。
//
// 实测确认过这个行为：不带 -v 完成安装后重建容器，sitename 从 KEEP-ME 变回空、
// 安装向导重新出现；用命名卷则设置完好。
//
// 判断依据是 /proc/self/mountinfo 里 /app/data 那一行的【第 4 段】（挂载在文件
// 系统内的根路径），而不是 " - " 之后的 source —— source 是设备名（如 /dev/sda1），
// 三种情况完全一样，拿它判断会让最危险的匿名卷也报「正常」：
//
//	id parent maj:min ROOT MOUNTPOINT opts - fstype SOURCE superopts
//	1553 1544 8:1 /var/lib/docker/volumes/<64位十六进制>/_data /app/data ... - ext4 /dev/sda1 ...
//	               ^^^ 这一段才区分得开
//
// 三种 ROOT：
//   - 匿名卷：/var/lib/docker/volumes/<64位十六进制>/_data   ← 危险
//   - 命名卷：/var/lib/docker/volumes/<名字>/_data           ← 安全
//   - 绑定挂载：宿主路径（如 /opt/nekomari/data）             ← 安全
//
// 只在容器里检查：直接在宿主机跑二进制时 ./data 就是普通目录，本来就能持久化，
// 对它报警是误报。容器判定用 /.dockerenv 与 /run/.containerenv。
//
// 只警告不阻止启动——数据通常仍可恢复，硬失败反而更糟。
func warnIfDataNotPersistent() {
	if !runningInContainer() {
		return
	}

	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return // 非 Linux，无法判断
	}

	dataDir, err := filepath.Abs("./data")
	if err != nil {
		return
	}

	var root string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[4] != dataDir {
			continue
		}
		root = fields[3] // 挂载在文件系统内的根路径，即卷/绑定来源
		break
	}

	anonVolume := regexp.MustCompile(`^/var/lib/docker/volumes/[0-9a-f]{64}/_data$`)

	switch {
	case root == "":
		logger.Warnf("dbcore",
			"data directory %s is NOT a mount point inside this container. Everything "+
				"in it lives in the container's writable layer and is LOST as soon as the "+
				"container is removed -- including on a routine image update. Mount a "+
				"named volume or a host directory at %s.", dataDir, dataDir)
	case anonVolume.MatchString(root):
		logger.Warnf("dbcore",
			"data directory %s is on an ANONYMOUS docker volume (%s). Recreating the "+
				"container -- which is what an image update does -- attaches a DIFFERENT "+
				"anonymous volume, so the panel will come up with an empty data "+
				"directory and show the install wizard again. The data is not deleted, "+
				"it is orphaned. Re-run with a named volume or a bind mount, e.g. "+
				"-v nekomari-data:/app/data, then copy the old volume's contents across.",
			dataDir, root)
	default:
		logger.Infof("dbcore",
			"data directory %s persists across container recreation (%s)", dataDir, root)
	}
}

// runningInContainer 判断当前进程是否在容器里。
//
// Docker 会创建 /.dockerenv，Podman 会创建 /run/.containerenv；两者都没有时再看
// PID 1 —— 容器里的 PID 1 通常是应用自身，而宿主机上一般是 init/systemd。
// 判断不出来的情况一律当作“不在容器里”，宁可不报也不误报。
func runningInContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}

	raw, err := os.ReadFile("/proc/1/cmdline")
	if err != nil {
		return false
	}
	cmd := strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", " "))
	switch {
	case cmd == "", strings.HasPrefix(cmd, "/sbin/init"),
		strings.HasPrefix(cmd, "/lib/systemd"), strings.HasPrefix(cmd, "/usr/lib/systemd"),
		strings.HasPrefix(cmd, "init"):
		return false
	}
	return true
}

func buildSQLiteDSN(databaseFile string) string {
	if databaseFile == "" {
		databaseFile = "./data/komari.db"
	}

	params := fmt.Sprintf("_busy_timeout=%d&_txlock=immediate", mainSQLiteBusyTimeout.Milliseconds())
	separator := "?"
	if strings.Contains(databaseFile, "?") {
		separator = "&"
	}

	if strings.HasPrefix(databaseFile, "file:") {
		return databaseFile + separator + params
	}

	if databaseFile == ":memory:" {
		return "file::memory:?cache=shared&" + params
	}

	return "file:" + filepath.ToSlash(databaseFile) + separator + params
}

func mainSQLiteOptions() sqlitetune.Options {
	return sqlitetune.Options{
		BusyTimeout:           mainSQLiteBusyTimeout,
		CacheSizeKB:           mainSQLiteCacheSizeKB,
		MMapSizeBytes:         0,
		TempStoreMemory:       false,
		CacheSpill:            true,
		WALAutoCheckpoint:     mainSQLiteWALAutoCheckpoint,
		JournalSizeLimitBytes: mainSQLiteJournalSizeLimit,
		Synchronous:           sqlitetune.SynchronousNormal,
	}
}

// Initialize 显式初始化数据库连接与表结构，仅执行一次。
// 与 GetDBInstance 不同，Initialize 返回错误而非直接退出进程，
// 便于启动生命周期统一处理错误、以及在测试/CLI 命令中做隔离。
func Initialize() error {
	once.Do(func() {
		initErr = doInitialize()
	})
	return initErr
}

// GetDBInstance 返回全局数据库实例。
// 为兼容既有大量调用点，这里保留“出错即退出”的语义；
// 需要错误处理的启动流程应优先调用 Initialize()。
func GetDBInstance() *gorm.DB {
	if err := Initialize(); err != nil {
		logger.Fatalf("dbcore", "Failed to initialize database: %v", err)
	}
	return instance
}

// Close 关闭底层数据库连接，供关闭流程调用。
func Close() error {
	if instance == nil {
		return nil
	}
	sqlDB, err := instance.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func doInitialize() error {
	var err error

	// 在数据库初始化前执行：如果存在 ./data/backup.zip，则进行恢复逻辑
	func() {
		backupZipPath := filepath.Join(".", "data", "backup.zip")
		if _, statErr := os.Stat(backupZipPath); statErr == nil {
			// 4. 将当前数据快照保存到 ./data/backup/，并保留已有归档。
			backupDir := filepath.Join(".", "data", "backup")
			if err := os.MkdirAll(backupDir, 0755); err != nil {
				logger.Errorf("dbcore", "[restore] failed to create backup dir: %v", err)
			} else {
				tsName := time.Now().UTC().Format("20060102-150405")
				bakPath := filepath.Join(backupDir, fmt.Sprintf("pre-restore-%s.zip", tsName))
				if zipErr := zipDirectoryExcluding("./data", bakPath, map[string]struct{}{backupZipPath: {}, backupDir: {}}); zipErr != nil {
					logger.Errorf("dbcore", "[restore] failed to zip current data: %v", zipErr)
				} else {
					logger.Infof("dbcore", "[restore] current data zipped to %s", bakPath)
				}
			}

			// 5. 删除数据文件，但保留归档目录和待恢复的 backup.zip。
			if delErr := removeAllInDirExcept("./data", map[string]struct{}{backupZipPath: {}, backupDir: {}}); delErr != nil {
				logger.Errorf("dbcore", "[restore] failed to cleanup data dir: %v", delErr)
			}

			// 6. 解压 ./data/backup.zip 到 ./data
			if unzipErr := unzipToDir(backupZipPath, "./data"); unzipErr != nil {
				logger.Errorf("dbcore", "[restore] failed to unzip backup into data: %v", unzipErr)
			} else {
				logger.Infof("dbcore", "[restore] backup.zip extracted to ./data")
			}

			// 7. 删除 ./data/backup.zip
			if rmErr := os.Remove(backupZipPath); rmErr != nil {
				logger.Errorf("dbcore", "[restore] failed to remove backup.zip: %v", rmErr)
			} else {
				logger.Infof("dbcore", "[restore] backup.zip removed")
			}
			// 8. 删除标记
			if rmErr := os.Remove("./data/komari-backup-markup"); rmErr != nil {
				logger.Errorf("dbcore", "[restore] failed to remove komari-backup-markup: %v", rmErr)
			} else {
				logger.Infof("dbcore", "[restore] komari-backup-markup removed")
			}
		}
	}()

	// 记录“打开数据库之前”komari.db 是否已存在，用于区分全新安装与旧版升级。
	// 必须在（可能的）恢复逻辑之后、gorm.Open 之前采集：恢复会解压出旧库，
	// gorm.Open 会创建空库。
	if _, statErr := os.Stat(resolveDatabaseFile()); statErr == nil {
		dbFileExistedAtStartup = true
	}

	// 数据目录不会跨容器重建保留时给出明确警告：这是「一升级设置就被初始化」
	// 最常见的原因，而日志是用户唯一能看到线索的地方。
	warnIfDataNotPersistent()

	logConfig := &gorm.Config{
		Logger:  logger.NewGormLogger(),
		NowFunc: func() time.Time { return time.Now().UTC() },
	}

	// 根据数据库类型选择不同的连接方式
	switch flags.ApplyDatabaseTypeNormalization() {
	case flags.DatabaseTypeSQLite:
		// _txlock=immediate lets writes acquire their lock before reads can
		// turn into a lock-upgrade conflict. sqlitetune applies the remaining
		// per-connection PRAGMAs whenever database/sql opens a connection.
		dsn := buildSQLiteDSN(flags.DatabaseFile)
		sqlDB, dbErr := sqlitetune.Open(dsn, mainSQLiteOptions())
		if dbErr != nil {
			return fmt.Errorf("open SQLite connection pool: %w", dbErr)
		}
		// SQLite has one writer. Keeping exactly one durable connection also
		// keeps connection-local cache and WAL limits stable for the main DB.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)
		sqlDB.SetConnMaxIdleTime(0)

		instance, err = gorm.Open(sqlite.New(sqlite.Config{Conn: sqlDB}), logConfig)
		if err != nil {
			_ = sqlDB.Close()
			return fmt.Errorf("failed to connect to SQLite3 database: %w", err)
		}
		if err := instance.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
			logger.Errorf("dbcore", "Failed to checkpoint SQLite WAL at startup: %v", err)
		}
	default:
		return fmt.Errorf("unsupported database type: %s (supported: %s)", flags.DatabaseType, flags.SupportedDatabaseTypes())
	}
	if err := migrations.Run(migrations.Context{DB: instance}); err != nil {
		return fmt.Errorf("failed to run startup migrations: %w", err)
	}
	config.SetDb(instance)

	// 配置库就绪后、执行后续 AutoMigrate 之前：
	// 基于配置中的版本标记检测升级并自动备份 ./data，便于回滚。
	backupOnVersionUpgrade()

	// 自动迁移模型
	//
	// 注意：负载/GPU/ping 历史监控数据运行期全部走 metric store（默认 SQLite
	// ./data/metrics.db，或配置的 MySQL/PostgreSQL）。旧的 records /
	// records_long_term / gpu_records / ping_records 表不再建表、不再写入。
	// 若升级时旧表仍存在，管理员可通过升级向导显式导入并清理。
	// models.Record / models.PingRecord / models.GPURecord 结构体仍作为
	// metric store 的读写 DTO 和旧表导入 DTO 保留在 models 包中。
	// models.TrafficReportNotification 同样仅为旧数据库兼容保留，不再建表。

	err = instance.AutoMigrate(
		&models.User{},
		&models.Client{},
		&models.Log{},
		&models.Clipboard{},
		&models.LoadNotification{},
		&models.OfflineNotification{},
		&models.PingTask{},
		&models.OidcProvider{},
		&models.MessageSenderProvider{},
		&models.ThemeConfiguration{},
		&models.PluginConfiguration{},
	)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}
	if err := instance.AutoMigrate(
		&models.Session{},
	); err != nil {
		logger.Errorf("dbcore", "Failed to create Session table, it may already exist: %v", err)
	}
	if err := instance.AutoMigrate(
		&models.Task{},
		&models.TaskResult{},
	); err != nil {
		logger.Errorf("dbcore", "Failed to create Task and TaskResult table, it may already exist: %v", err)
	}

	return nil
}
