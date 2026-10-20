package server

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
	logger "github.com/Aone2233/nekomari/utils/log"
)

// 主库完整性日检的日志级别：ok → Info，发现问题 → Error，无法运行 → Warn。
//
// 这一层不碰真实数据库：要验证的是「报告里有什么」到「写哪一级日志」的映射，而真实检查
// 由 database/dbcore 的用例覆盖（临时库、缺索引、坏连接都有）。
//
// 调度注册那一行（scheduler.AddFunc）没有对应的单元测试：调度器的 job 表没有
// introspection，而 registerScheduledWork 还注册了依赖数据库与配置的其它任务，把它单独
// 拉进测试会把整条启动链拖进来 —— 这里覆盖的是它注册的那个函数本身。

// captureServerLogs 把包级日志接到一个缓冲区。
//
// logger() 在没有调用 Setup 时用 slog.Default()，测试进程里正是这样；用例结束后还原，
// 免得把默认 handler 留给同包的其它用例。
func captureServerLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(logger.NewConsoleHandler(&buffer, slog.LevelDebug)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buffer
}

func TestReportMainDatabaseIntegrityLogLevels(t *testing.T) {
	cases := []struct {
		name      string
		run       func() (dbcore.MainDatabaseIntegrityReport, error)
		wantLevel string
		wantText  string
	}{
		{
			name: "a clean check is Info",
			run: func() (dbcore.MainDatabaseIntegrityReport, error) {
				return dbcore.MainDatabaseIntegrityReport{}, nil
			},
			wantLevel: "[INFO/SERVER]",
			wantText:  "check passed",
		},
		{
			name: "a structural problem is Error",
			run: func() (dbcore.MainDatabaseIntegrityReport, error) {
				return dbcore.MainDatabaseIntegrityReport{QuickCheckErr: errors.New("page 7 is never used")}, nil
			},
			wantLevel: "[ERROR/SERVER]",
			wantText:  "structural problem",
		},
		{
			name: "a missing authentication index is Error",
			run: func() (dbcore.MainDatabaseIntegrityReport, error) {
				return dbcore.MainDatabaseIntegrityReport{
					MissingIndexes: []string{"clients.token — agent authentication scans the clients table"},
				}, nil
			},
			wantLevel: "[ERROR/SERVER]",
			wantText:  "missing an index",
		},
		{
			name: "a check that could not run is Warn, not an outage",
			run: func() (dbcore.MainDatabaseIntegrityReport, error) {
				return dbcore.MainDatabaseIntegrityReport{}, errors.New("main database is not initialized")
			},
			wantLevel: "[WARN/SERVER]",
			wantText:  "could not run",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			logs := captureServerLogs(t)
			reportMainDatabaseIntegrity(testCase.run)
			output := logs.String()
			if !strings.Contains(output, testCase.wantLevel) {
				t.Errorf("the check logged at the wrong level, want %s:\n%s", testCase.wantLevel, output)
			}
			if !strings.Contains(output, testCase.wantText) {
				t.Errorf("the log line does not contain %q:\n%s", testCase.wantText, output)
			}
		})
	}
}

// 真实的入口在没有主库时也不能 panic：日检失败只是一条日志，不影响服务。
func TestMainDatabaseIntegrityCheckWithoutAMainDatabase(t *testing.T) {
	if dbcore.ReadyDBInstance() != nil {
		t.Skip("another test in this package initialised the main database")
	}
	logs := captureServerLogs(t)
	mainDatabaseIntegrityCheck()
	if !strings.Contains(logs.String(), "could not run") {
		t.Errorf("a missing main database must be logged, not ignored:\n%s", logs.String())
	}
}
