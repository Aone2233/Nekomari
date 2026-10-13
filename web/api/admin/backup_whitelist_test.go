package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/plugin"
)

// runtimeDataPaths 是运行时会在 ./data 下写入的每一处，连同它的处置意见：
// reason 为空表示必须被 backupWhitelist 覆盖，非空表示有意不备份并说明原因。
//
// 这张表存在的理由是一次真实事故：白名单里的 "plguin-data/" 把
// plugin.StorageDir（"./data/plugin-data"）拼错了，而 copyWhitelistedFilesFrom
// 对不存在的项静默跳过 —— 备份成功、插件数据全丢，没有任何测试会失败。
// 新增一处运行时数据时必须在表里表态，否则这个用例会红。
var runtimeDataPaths = []struct {
	path   string // 相对 ./data
	reason string // 非空 = 有意不按普通文件备份
}{
	{path: "favicon.ico"},
	{path: "font.ttf"},
	{path: "theme/"},
	{path: "plugin/"},
	{path: "plugin-data/"},
	{path: "komari.db", reason: "由 DownloadBackup 用 VACUUM INTO 做一致性快照，不能按普通文件复制"},
	{path: "metrics.db", reason: "同上；WAL 模式下普通复制会丢掉最近的 rollup"},
	{path: "backup/", reason: "里面本来就是备份归档，备份的备份没有意义"},
	{path: "backup.zip", reason: "恢复上传的临时归档，不是需要持久化的数据"},
	{path: "komari-backup-markup", reason: "恢复流程的标记目录"},
	{path: "GeoLite2-Country.mmdb", reason: "可重新下载的 GeoIP 库，且会随更新替换"},
}

func TestBackupWhitelistMatchesTheRuntimeDataLayout(t *testing.T) {
	whitelisted := make(map[string]bool, len(backupWhitelist))
	for _, entry := range backupWhitelist {
		whitelisted[entry] = true
	}

	for _, item := range runtimeDataPaths {
		covered := whitelisted[item.path]
		switch {
		case item.reason == "" && !covered:
			t.Errorf("./data/%s is written by the runtime but is not in backupWhitelist: "+
				"either whitelist it or add it to runtimeDataPaths with a reason", item.path)
		case item.reason != "" && covered:
			t.Errorf("./data/%s is listed as exempt (%s) but is also whitelisted; pick one",
				item.path, item.reason)
		}
	}
}

// TestBackupWhitelistMatchesPluginRuntimeDirectories 把白名单与运行时【自己的】
// 目录常量对齐。白名单里写的是字符串，而目录由 internal/plugin 定义，两者之间
// 此前没有任何联系 —— 这正是 plugin-data 被写成 plguin-data 而不被发现的原因。
func TestBackupWhitelistMatchesPluginRuntimeDirectories(t *testing.T) {
	whitelisted := make(map[string]bool, len(backupWhitelist))
	for _, entry := range backupWhitelist {
		whitelisted[entry] = true
	}

	for _, dir := range []string{plugin.DataDir, plugin.StorageDir} {
		// filepath.Clean 会把 "./data/plugin" 规范化成 "data/plugin"（Windows 上还会
		// 换成反斜杠），所以先 Clean 再剥前缀。
		cleaned := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(dir)), "./")
		rel := strings.TrimPrefix(cleaned, "data/")
		if rel == cleaned {
			t.Fatalf("cannot derive a ./data-relative path from %q; update this test", dir)
		}
		// 白名单里目录项带尾斜杠（copyWhitelistedFilesFrom 靠 os.Stat 判断类型，
		// 但名单的写法是带斜杠的），所以比对时统一成带斜杠的形式。
		if !strings.HasSuffix(rel, "/") {
			rel += "/"
		}
		if !whitelisted[rel] {
			t.Errorf("the plugin runtime writes %s but backupWhitelist has no %q entry "+
				"(a typo here is silent: missing entries are skipped)", dir, rel)
		}
	}
}

// TestPluginStorageSurvivesTheBackupPath 走一遍真实路径：在 ./data/plugin-data 下
// 写一个插件的状态文件，跑白名单复制，断言它出现在归档内容里。
func TestPluginStorageSurvivesTheBackupPath(t *testing.T) {
	t.Chdir(t.TempDir())

	storageFile := filepath.Join(".", "data", "plugin-data", "demo", "state.json")
	if err := os.MkdirAll(filepath.Dir(storageFile), 0o755); err != nil {
		t.Fatalf("create plugin storage dir: %v", err)
	}
	if err := os.WriteFile(storageFile, []byte(`{"counter":7}`), 0o600); err != nil {
		t.Fatalf("write plugin state: %v", err)
	}

	contentDir := t.TempDir()
	if err := copyWhitelistedFilesFrom(filepath.Join(".", "data"), contentDir); err != nil {
		t.Fatalf("copyWhitelistedFilesFrom: %v", err)
	}

	archived := filepath.Join(contentDir, "plugin-data", "demo", "state.json")
	body, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("plugin storage was not archived: %v", err)
	}
	if string(body) != `{"counter":7}` {
		t.Fatalf("archived plugin state = %q", body)
	}
}
