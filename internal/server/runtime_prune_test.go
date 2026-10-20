package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
)

// 升级归档每日清理的日志映射：成功记 Info（删了多少、现在剩多少），失败记 Warn 且不 panic。
//
// 与 db:integrity 的用例同一个思路：注册那一行（scheduler.AddFunc）没有单测 —— 调度器的
// job 表没有 introspection，而 registerScheduledWork 还注册了依赖数据库与配置的其它任务，
// 单独把它拉进测试会把整条启动链拖进来 —— 所以覆盖边界是它注册的那个函数。日志捕获复用
// runtime_integrity_test.go 的 captureServerLogs。

func TestReportUpgradeArchivePruneLogLevels(t *testing.T) {
	cases := []struct {
		name      string
		run       func() (dbcore.UpgradeArchivePruneResult, error)
		wantLevel string
		wantText  string
	}{
		{
			name: "a successful sweep reports the counts at Info",
			run: func() (dbcore.UpgradeArchivePruneResult, error) {
				return dbcore.UpgradeArchivePruneResult{
					Dir:       filepath.Join("data", "backup"),
					Removed:   2,
					Remaining: 3,
					Keep:      3,
				}, nil
			},
			wantLevel: "[INFO/SERVER]",
			wantText:  "removed 2 old archive(s), 3 kept",
		},
		{
			name: "a failed sweep is a warning, not an outage",
			run: func() (dbcore.UpgradeArchivePruneResult, error) {
				return dbcore.UpgradeArchivePruneResult{}, errors.New(
					"remove old upgrade archive upgrade-20260101-000000.zip: access denied")
			},
			wantLevel: "[WARN/SERVER]",
			wantText:  "Could not prune old upgrade archives",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			logs := captureServerLogs(t)
			reportUpgradeArchivePrune(testCase.run)
			output := logs.String()
			if !strings.Contains(output, testCase.wantLevel) {
				t.Errorf("the sweep logged at the wrong level, want %s:\n%s", testCase.wantLevel, output)
			}
			if !strings.Contains(output, testCase.wantText) {
				t.Errorf("the log line does not contain %q:\n%s", testCase.wantText, output)
			}
		})
	}
}

// 真实的入口在没有 backup 目录时也必须正常返回：零清理是一条 Info，不是 panic，也不该
// 顺手把目录建出来 —— 长期不升级、从未升级过的面板本来就没有可清理的东西。
func TestPruneUpgradeArchivesCheckWithoutABackupDirectory(t *testing.T) {
	t.Chdir(t.TempDir())

	logs := captureServerLogs(t)
	pruneUpgradeArchivesCheck()

	output := logs.String()
	if !strings.Contains(output, "[INFO/SERVER]") {
		t.Errorf("a sweep with nothing to prune must be logged at Info:\n%s", output)
	}
	if !strings.Contains(output, "removed 0 old archive(s)") {
		t.Errorf("the log line does not report the zero sweep:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join("data", "backup")); !os.IsNotExist(err) {
		t.Errorf("the daily sweep created data/backup (stat error %v): cleanup must not create state", err)
	}
}
