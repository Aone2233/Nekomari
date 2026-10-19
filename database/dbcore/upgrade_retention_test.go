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
