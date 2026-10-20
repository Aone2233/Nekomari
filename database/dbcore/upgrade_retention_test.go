package dbcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 升级归档的排除清单与保留策略。
//
// 两件事都发生在「版本变化 → 面板还没开始监听」的窗口里：v1.6.15 上线时这个窗口花了
// 约 90 秒、写出 1.6 GB，因为 2.4 GB 的 db-maintenance/ 与换主题留下的备份目录也被
// Deflate 了一遍；而 data/backup 里攒下了 5.5 GB 的旧 upgrade-*.zip。这里的用例把
// 「该排除的排除、该保留的保留、别的一律不许动」钉住。

// pruneUpgradeArchives 只处理 upgrade-*.zip，按名字时间序保留最新 keep 份。
func TestPruneUpgradeArchivesKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()

	archives := []string{
		"upgrade-20260101-000000.zip",
		"upgrade-20260102-000000.zip",
		"upgrade-20260103-000000.zip",
		"upgrade-20260104-000000.zip",
		"upgrade-20260105-000000.zip",
	}
	// 同一个目录里还住着别的东西：恢复前的快照、打包中途的临时文件、以及普通文件。
	// 剪枝一份都不能碰 —— pre-restore 快照是另一条恢复路径的安全网。
	bystanders := []string{
		"pre-restore-20251231-235959.zip",
		"upgrade-20260106-000000.zip.partial",
		"backup.zip",
		"notes.txt",
	}
	for _, name := range append(append([]string{}, archives...), bystanders...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("archive"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := pruneUpgradeArchives(dir, 3); err != nil {
		t.Fatalf("pruneUpgradeArchives: %v", err)
	}

	for _, removed := range []string{"upgrade-20260101-000000.zip", "upgrade-20260102-000000.zip"} {
		if _, err := os.Stat(filepath.Join(dir, removed)); !os.IsNotExist(err) {
			t.Errorf("%s was not pruned (stat error %v)", removed, err)
		}
	}
	for _, kept := range []string{
		"upgrade-20260103-000000.zip",
		"upgrade-20260104-000000.zip",
		"upgrade-20260105-000000.zip",
	} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s is among the newest three but was removed: %v", kept, err)
		}
	}
	for _, untouchable := range bystanders {
		if _, err := os.Stat(filepath.Join(dir, untouchable)); err != nil {
			t.Errorf("%s must not be pruned: %v", untouchable, err)
		}
	}
}

// 不到上限时什么都不删；keep 的下界是 1 —— 零值入参绝不能把备份删光。
func TestPruneUpgradeArchivesLimits(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "upgrade-20260101-000000.zip")
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	if err := pruneUpgradeArchives(dir, 3); err != nil {
		t.Fatalf("pruneUpgradeArchives with keep=3: %v", err)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("the only archive was removed although keep=3: %v", err)
	}

	for _, keep := range []int{0, -1} {
		if err := pruneUpgradeArchives(dir, keep); err == nil {
			t.Errorf("pruneUpgradeArchives(keep=%d) must be rejected", keep)
		}
	}
	if _, err := os.Stat(archive); err != nil {
		t.Errorf("a rejected retention deleted the archive: %v", err)
	}
}

// 备份目录不存在时没有可剪的东西，不是错误。
func TestPruneUpgradeArchivesOnAMissingDirectory(t *testing.T) {
	if err := pruneUpgradeArchives(filepath.Join(t.TempDir(), "absent"), 3); err != nil {
		t.Fatalf("a missing backup directory has nothing to prune, want nil, got %v", err)
	}
}

// 排除清单必须覆盖维护/换主题期间的临时目录（含只能按 glob 命中的带时间戳目录），
// 也不能把回滚真正需要的东西排除掉。
func TestUpgradeArchiveExclusionsCoverTemporaryDirectories(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, dir := range []string{
		filepath.Join("data", "db-maintenance"),
		filepath.Join("data", "theme-backups"),
		filepath.Join("data", "theme-assets-backup-20260101-000000"),
		filepath.Join("data", ".uploading"),
		filepath.Join("data", "theme"),
		filepath.Join("data", "plugin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	exclude := upgradeArchiveExclusions(filepath.Join("data", "backup"))
	normalized := make(map[string]struct{}, len(exclude))
	for path := range exclude {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("abs %s: %v", path, err)
		}
		normalized[absolute] = struct{}{}
	}

	mustExclude := []string{
		"data/backup.zip",
		"data/backup",
		"data/db-maintenance",
		"data/theme-backups",
		"data/theme-assets-backup-20260101-000000",
		"data/.uploading",
		"data/metrics.db",
		"data/metrics.db-wal",
		"data/metrics.db-shm",
	}
	for _, relative := range mustExclude {
		absolute, err := filepath.Abs(filepath.FromSlash(relative))
		if err != nil {
			t.Fatalf("abs %s: %v", relative, err)
		}
		if _, ok := normalized[absolute]; !ok {
			t.Errorf("%s is not excluded from the upgrade archive", relative)
		}
	}

	// 回滚只需要面板库、配置、当前主题与插件：这些不能被排除。
	for _, relative := range []string{"data/komari.db", "data/komari.db-wal", "data/theme", "data/plugin"} {
		absolute, err := filepath.Abs(filepath.FromSlash(relative))
		if err != nil {
			t.Fatalf("abs %s: %v", relative, err)
		}
		if _, ok := normalized[absolute]; ok {
			t.Errorf("%s is excluded, but a rollback needs it", relative)
		}
	}
}

// 端到端：一次真实的版本升级不许把临时目录打进归档，也不许删掉它们；同时只保留最新
// 三份 upgrade-*.zip，pre-restore 快照一律不动。
func TestUpgradeArchiveSkipsTemporaryDirectoriesAndPrunesOldArchives(t *testing.T) {
	dir := prepareUpgradeTestWorkspace(t)
	dataDir := filepath.Join(dir, "data")

	createUpgradeFixtureDatabase(t, filepath.Join(dataDir, "komari.db"),
		`CREATE TABLE configs (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO configs (key, value) VALUES ('system_version', '"v-old"')`,
		legacyClientInfoTable,
	)

	// 维护/换主题期间的临时内容：够大才像真实场景，但这里只验证「在不在归档里」。
	temporary := map[string]string{
		filepath.Join("db-maintenance", "komari-pre-maintenance.db"):             strings.Repeat("maintenance", 2048),
		filepath.Join("theme-backups", "sao-pre-deploy-20260101", "theme.json"):  `{"name":"sao"}`,
		filepath.Join("theme-assets-backup-20260101-000000", "assets", "bg.png"): strings.Repeat("asset", 1024),
		filepath.Join(".uploading", ".komari-upload-1.json"):                     `{"path":"x"}`,
	}
	for relative, content := range temporary {
		path := filepath.Join(dataDir, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create parent of %s: %v", relative, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}

	// 升级前已经存在的四份归档：加上本次新写的那份，剪枝后只应剩最新的三份。
	backupDir := filepath.Join(dataDir, "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("create backup dir: %v", err)
	}
	oldArchives := []string{
		"upgrade-20260101-000000.zip",
		"upgrade-20260102-000000.zip",
		"upgrade-20260103-000000.zip",
		"upgrade-20260104-000000.zip",
	}
	for _, name := range oldArchives {
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("old archive"), 0o600); err != nil {
			t.Fatalf("write old archive %s: %v", name, err)
		}
	}
	const preRestore = "pre-restore-20251231-235959.zip"
	if err := os.WriteFile(filepath.Join(backupDir, preRestore), []byte("pre-restore"), 0o600); err != nil {
		t.Fatalf("write pre-restore archive: %v", err)
	}

	SetVersionID("v-new")
	if err := Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	archives := upgradeArchives(t)
	if len(archives) != upgradeArchiveRetention {
		t.Fatalf("upgrade archives = %v, want %d (retention)", archives, upgradeArchiveRetention)
	}
	// 留下的必须是「最新的三份」：01-03、01-04 与本次新写的那份。
	for _, name := range []string{"upgrade-20260103-000000.zip", "upgrade-20260104-000000.zip"} {
		if _, err := os.Stat(filepath.Join(backupDir, name)); err != nil {
			t.Errorf("%s is among the newest three but was pruned: %v", name, err)
		}
	}
	for _, name := range oldArchives[:2] {
		if _, err := os.Stat(filepath.Join(backupDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s is older than the retention and should have been pruned (stat error %v)", name, err)
		}
	}
	newest := archives[len(archives)-1]
	for _, name := range oldArchives {
		if filepath.Base(newest) == name {
			t.Fatalf("the archive written by this upgrade was pruned: retention kept %s instead", name)
		}
	}
	if _, err := os.Stat(filepath.Join(backupDir, preRestore)); err != nil {
		t.Errorf("the pre-restore snapshot was removed by the upgrade pruning: %v", err)
	}

	// 归档里不能出现临时目录的条目，但它们必须还在磁盘上（排除 ≠ 删除）。
	entries := archiveEntryNames(t, newest)
	for _, entry := range entries {
		for _, prefix := range []string{"db-maintenance/", "theme-backups/", "theme-assets-backup-", ".uploading/"} {
			if strings.HasPrefix(entry, prefix) {
				t.Errorf("archive entry %q comes from an excluded temporary directory", entry)
			}
		}
	}
	requireContains(t, entries, "komari.db")
	for relative := range temporary {
		if _, err := os.Stat(filepath.Join(dataDir, relative)); err != nil {
			t.Errorf("%s was removed: excluding a directory from the archive must not delete it: %v", relative, err)
		}
	}
}

// 每日清理：对默认目录 ./data/backup 应用同一个保留策略，并报告删了多少、剩多少。
//
// 与升级时的剪枝共用 pruneUpgradeArchives，所以这里钉的是导出入口本身：目录路径、
// 保留数、结果计数，以及「只碰文件系统」这条 —— 长期不升级的面板不该因为一次清理
// 去打开数据库。
func TestRunUpgradeArchivePruneTrimsTheDefaultDirectory(t *testing.T) {
	t.Chdir(t.TempDir())

	backupDir := filepath.Join(".", "data", "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("create backup dir: %v", err)
	}
	archives := []string{
		"upgrade-20260101-000000.zip",
		"upgrade-20260102-000000.zip",
		"upgrade-20260103-000000.zip",
		"upgrade-20260104-000000.zip",
		"upgrade-20260105-000000.zip",
	}
	bystanders := []string{
		"pre-restore-20251231-235959.zip",
		"upgrade-20260106-000000.zip.partial",
	}
	for _, name := range append(append([]string{}, archives...), bystanders...) {
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("archive"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	result, err := RunUpgradeArchivePrune()
	if err != nil {
		t.Fatalf("RunUpgradeArchivePrune: %v", err)
	}
	if result.Dir != backupDir {
		t.Errorf("result.Dir = %q, want %q (the directory backupOnVersionUpgrade uses)", result.Dir, backupDir)
	}
	if result.Keep != upgradeArchiveRetention {
		t.Errorf("result.Keep = %d, want %d", result.Keep, upgradeArchiveRetention)
	}
	if result.Removed != 2 || result.Remaining != 3 {
		t.Errorf("result = %+v, want 2 removed and 3 remaining", result)
	}
	for _, name := range archives[:2] {
		if _, err := os.Stat(filepath.Join(backupDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s is older than the retention but was kept (stat error %v)", name, err)
		}
	}
	for _, name := range append(append([]string{}, archives[2:]...), bystanders...) {
		if _, err := os.Stat(filepath.Join(backupDir, name)); err != nil {
			t.Errorf("%s was removed: only upgrade-*.zip beyond the retention may be deleted: %v", name, err)
		}
	}
	// 这个入口只碰文件系统：数据库文件不该被它创建或打开。
	if _, err := os.Stat(filepath.Join(".", "data", "komari.db")); !os.IsNotExist(err) {
		t.Errorf("RunUpgradeArchivePrune touched the database file (stat error %v), it must not initialise the database", err)
	}
}

// 从未升级过的面板没有 backup 目录：零结果、无错误、不创建目录 —— 这是正常状态，
// 不是需要告警的失败。
func TestRunUpgradeArchivePruneWithoutABackupDirectory(t *testing.T) {
	t.Chdir(t.TempDir())

	result, err := RunUpgradeArchivePrune()
	if err != nil {
		t.Fatalf("a missing backup directory must not be an error: %v", err)
	}
	if result.Removed != 0 || result.Remaining != 0 {
		t.Errorf("result = %+v, want a zero sweep", result)
	}
	if result.Keep != upgradeArchiveRetention {
		t.Errorf("result.Keep = %d, want %d", result.Keep, upgradeArchiveRetention)
	}
	if _, err := os.Stat(filepath.Join(".", "data", "backup")); !os.IsNotExist(err) {
		t.Errorf("the sweep created ./data/backup (stat error %v): cleanup must not create state", err)
	}
}

// 删不掉时如实返回错误与磁盘上的真实数量，而不是假装清理成功。
//
// 用「最旧的两个条目是非空目录」制造失败：os.Remove 删不掉非空目录，这在 Windows 与
// Unix 上都成立，不依赖权限位（测试可能以 root 跑）。
func TestRunUpgradeArchivePruneReportsRemovalFailures(t *testing.T) {
	t.Chdir(t.TempDir())

	backupDir := filepath.Join(".", "data", "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("create backup dir: %v", err)
	}
	stuck := []string{
		"upgrade-20260101-000000.zip",
		"upgrade-20260102-000000.zip",
	}
	for _, name := range stuck {
		directory := filepath.Join(backupDir, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(directory, "still-here.txt"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write inside %s: %v", name, err)
		}
	}
	for _, name := range []string{
		"upgrade-20260103-000000.zip",
		"upgrade-20260104-000000.zip",
		"upgrade-20260105-000000.zip",
	} {
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("archive"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	result, err := RunUpgradeArchivePrune()
	if err == nil {
		t.Fatal("a removal failure was reported as a clean sweep")
	}
	if result.Removed != 0 || result.Remaining != 5 {
		t.Errorf("result = %+v, want 0 removed and the 5 entries still counted", result)
	}
	for _, name := range stuck {
		if _, statErr := os.Stat(filepath.Join(backupDir, name)); statErr != nil {
			t.Errorf("%s was deleted although removing it should have failed: %v", name, statErr)
		}
	}
}
