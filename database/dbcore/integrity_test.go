package dbcore

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 主库完整性检查的用例：quick_check 的通过与失败、认证索引的存在性与缺失。
//
// 这些检查的价值全在「不误报也不漏报」：误报会让它在每次正常启动时刷一行告警，然后被
// 忽略（那正是这次要修的那类静默问题）；漏报则等于没写。

// openIntegrityTestDatabase 打开一个临时 SQLite 文件库，执行 statements 后返回句柄。
//
// 与生产一致地只留一条连接（主库就是 SetMaxOpenConns(1)）：这样「读着结果集又发下一条
// 查询」那类会让启动卡死的问题在测试里也会暴露，而不是只在生产上偶发。
func openIntegrityTestDatabase(t *testing.T, statements ...string) *gorm.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "main.db")
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(path)+"?_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	return db
}

// 正常迁移出来的库必须通过 quick_check。
func TestMainDatabaseQuickCheckPassesOnAFreshlyMigratedDatabase(t *testing.T) {
	db := openIntegrityTestDatabase(t)
	if err := db.AutoMigrate(&models.User{}, &models.Client{}, &models.Session{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if err := mainDatabaseQuickCheck(db); err != nil {
		t.Fatalf("PRAGMA quick_check on a freshly migrated database reported a problem: %v", err)
	}
}

// quick_check 的输出解析：只有 "ok"（大小写与空白不敏感）算通过。
func TestQuickCheckResultParsing(t *testing.T) {
	for _, passing := range []string{"ok", "OK", "  ok  "} {
		if err := quickCheckResultError(passing); err != nil {
			t.Errorf("quickCheckResultError(%q) = %v, want nil", passing, err)
		}
	}

	problem := "row 6 missing from index sqlite_autoindex_clients_2"
	err := quickCheckResultError(problem)
	if err == nil {
		t.Fatal("a quick_check problem was treated as a pass")
	}
	if !strings.Contains(err.Error(), problem) {
		t.Errorf("the error dropped the reported problem: %v", err)
	}
	if err := quickCheckResultError(""); err == nil {
		t.Error("an empty quick_check result was treated as a pass")
	}
}

// 连接已经不可用时必须报错，而不是把「查不了」当成「没问题」—— 那正是静默失败。
func TestMainDatabaseQuickCheckReportsAnUnusableDatabase(t *testing.T) {
	db := openIntegrityTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}
	if err := mainDatabaseQuickCheck(db); err == nil {
		t.Fatal("quick_check on a closed database must return an error, not pass silently")
	}
}

// AutoMigrate 建出来的 schema 必须已经覆盖认证路径依赖的索引。否则这条检查会在每个正常
// 启动时都报警，而一条永远在响的告警等于没有告警。
func TestAuthenticationIndexesExistOnTheMigratedSchema(t *testing.T) {
	db := openIntegrityTestDatabase(t)
	if err := db.AutoMigrate(&models.User{}, &models.Client{}, &models.Session{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	// 先确认表名与模型对得上：表名写错时 sqliteIndexedColumns 会把「表不存在」当成
	// 「不用检查」，下面的断言就会空过。
	for _, table := range []string{"users", "clients", "sessions"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("AutoMigrate did not create %q, so the index check would silently skip it", table)
		}
	}
	missing, err := missingAuthenticationIndexes(db)
	if err != nil {
		t.Fatalf("missingAuthenticationIndexes: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("a freshly migrated database is missing authentication indexes: %v", missing)
	}
}

// 缺索引的库必须被报告，报告里要有表/列，也要有「会退化成全表扫描」这条影响说明 ——
// 只报一个列名，运维无法判断要不要停机重建。
func TestMissingAuthenticationIndexesAreReported(t *testing.T) {
	db := openIntegrityTestDatabase(t,
		`CREATE TABLE users (uuid TEXT PRIMARY KEY, username TEXT NOT NULL)`,
		`CREATE TABLE clients (uuid TEXT PRIMARY KEY, token TEXT NOT NULL)`,
		// session 是主键：这个库在 sessions.session 上是有索引的，不能被误报。
		`CREATE TABLE sessions (session TEXT PRIMARY KEY, uuid TEXT)`,
	)

	missing, err := missingAuthenticationIndexes(db)
	if err != nil {
		t.Fatalf("missingAuthenticationIndexes: %v", err)
	}
	report := strings.Join(missing, "\n")
	for _, want := range []string{"users.username", "clients.token"} {
		if !strings.Contains(report, want) {
			t.Errorf("%q was not reported as missing: %v", want, missing)
		}
	}
	for _, hasIndex := range []string{"users.uuid", "sessions.session"} {
		if strings.Contains(report, hasIndex) {
			t.Errorf("%q has an index but was reported missing: %v", hasIndex, missing)
		}
	}
	for _, impact := range []string{"scans the clients table", "password login"} {
		if !strings.Contains(report, impact) {
			t.Errorf("the report does not say what the missing index costs (%q): %v", impact, missing)
		}
	}

	// 补上索引之后必须变成干净：检查本身不能是「永远报警」。
	for _, statement := range []string{
		`CREATE UNIQUE INDEX idx_users_username ON users(username)`,
		`CREATE UNIQUE INDEX idx_clients_token ON clients(token)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create index: %v", err)
		}
	}
	missing, err = missingAuthenticationIndexes(db)
	if err != nil {
		t.Fatalf("missingAuthenticationIndexes after creating the indexes: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("the check still reports missing indexes after they were created: %v", missing)
	}
}

// 表还不存在时不算「缺索引」：那是 AutoMigrate 的事，换个说法再报一遍只会误导。
func TestMissingAuthenticationIndexesSkipsAbsentTables(t *testing.T) {
	db := openIntegrityTestDatabase(t)
	missing, err := missingAuthenticationIndexes(db)
	if err != nil {
		t.Fatalf("missingAuthenticationIndexes: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("an empty database has no tables, so no index may be reported missing: %v", missing)
	}
}

// verifyMainDatabaseIntegrity 的三种结论：健康库通过、缺索引只告警、quick_check 失败返回错误。
func TestVerifyMainDatabaseIntegrity(t *testing.T) {
	t.Run("a healthy database passes", func(t *testing.T) {
		db := openIntegrityTestDatabase(t)
		if err := db.AutoMigrate(&models.User{}, &models.Client{}, &models.Session{}); err != nil {
			t.Fatalf("AutoMigrate: %v", err)
		}
		if err := verifyMainDatabaseIntegrity(db); err != nil {
			t.Fatalf("a healthy database failed the integrity check: %v", err)
		}
	})

	t.Run("missing indexes warn but do not block startup", func(t *testing.T) {
		db := openIntegrityTestDatabase(t,
			`CREATE TABLE users (uuid TEXT PRIMARY KEY, username TEXT NOT NULL)`,
			`CREATE TABLE clients (uuid TEXT PRIMARY KEY, token TEXT NOT NULL)`,
		)
		// 这个 fixture 必须真的缺索引，否则本用例什么都没验证。
		missing, err := missingAuthenticationIndexes(db)
		if err != nil {
			t.Fatalf("missingAuthenticationIndexes: %v", err)
		}
		if len(missing) == 0 {
			t.Fatal("the fixture was supposed to be missing authentication indexes, so this case would prove nothing")
		}
		if err := verifyMainDatabaseIntegrity(db); err != nil {
			t.Fatalf("missing authentication indexes must not fail startup, only warn: %v", err)
		}
	})

	t.Run("a failed quick_check warns but does not block by default", func(t *testing.T) {
		t.Setenv(strictDatabaseCheckEnv, "")
		db := openIntegrityTestDatabase(t)
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("get sql.DB: %v", err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close test database: %v", err)
		}
		if err := verifyMainDatabaseIntegrity(db); err != nil {
			t.Fatalf("an integrity finding must not stop startup by default: %v", err)
		}
	})

	t.Run("a failed quick_check is fatal in strict mode", func(t *testing.T) {
		t.Setenv(strictDatabaseCheckEnv, "1")
		db := openIntegrityTestDatabase(t)
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("get sql.DB: %v", err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close test database: %v", err)
		}
		if err := verifyMainDatabaseIntegrity(db); err == nil {
			t.Fatal("strict mode must return the quick_check failure so startup can be refused")
		}
	})

	t.Run("a nil handle is ignored", func(t *testing.T) {
		if err := verifyMainDatabaseIntegrity(nil); err != nil {
			t.Errorf("verifyMainDatabaseIntegrity(nil) = %v, want nil", err)
		}
	})
}

// quoteSQLiteIdentifier 必须能安全地嵌进 PRAGMA：名字里带引号时不能把 SQL 拼坏。
func TestQuoteSQLiteIdentifier(t *testing.T) {
	if got := quoteSQLiteIdentifier("idx_sessions_session"); got != `"idx_sessions_session"` {
		t.Errorf("quoteSQLiteIdentifier = %q", got)
	}
	if got := quoteSQLiteIdentifier(`we"ird`); got != `"we""ird"` {
		t.Errorf("quoteSQLiteIdentifier did not escape the embedded quote: %q", got)
	}
}

// withMainDatabaseInstance 临时把包级主库句柄指向 db，用例结束还原。
//
// RunMainDatabaseQuickCheck 读的是 ReadyDBInstance()，而那个句柄只由 Initialize 设置；
// 这些用例不需要跑整条启动链（恢复、迁移、升级备份），所以直接换句柄。
func withMainDatabaseInstance(t *testing.T, db *gorm.DB) {
	t.Helper()
	previous := instance
	instance = db
	t.Cleanup(func() { instance = previous })
}

// OK 的判据：三项里任意一项有问题就不是 ok。
func TestMainDatabaseIntegrityReportOK(t *testing.T) {
	if !(MainDatabaseIntegrityReport{}).OK() {
		t.Error("an empty report (nothing found) must be OK")
	}
	cases := []struct {
		name   string
		report MainDatabaseIntegrityReport
	}{
		{"a quick_check finding", MainDatabaseIntegrityReport{QuickCheckErr: errors.New("page 7 is never used")}},
		{"an index-check failure", MainDatabaseIntegrityReport{IndexCheckErr: errors.New("query failed")}},
		{"a missing index", MainDatabaseIntegrityReport{MissingIndexes: []string{"clients.token"}}},
	}
	for _, testCase := range cases {
		if testCase.report.OK() {
			t.Errorf("%s was reported as OK: %+v", testCase.name, testCase.report)
		}
	}
}

// 周期检查的正常路径：健康库给出一个干净的报告。
func TestRunMainDatabaseQuickCheckOnAHealthyDatabase(t *testing.T) {
	db := openIntegrityTestDatabase(t)
	if err := db.AutoMigrate(&models.User{}, &models.Client{}, &models.Session{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	withMainDatabaseInstance(t, db)

	report, err := RunMainDatabaseQuickCheck()
	if err != nil {
		t.Fatalf("RunMainDatabaseQuickCheck: %v", err)
	}
	if !report.OK() {
		t.Fatalf("a healthy database produced a report with problems: %+v", report)
	}
	if report.QuickCheckErr != nil || report.IndexCheckErr != nil || len(report.MissingIndexes) != 0 {
		t.Fatalf("the report carries stray findings: %+v", report)
	}
}

// 结构问题必须落进报告，而不是 panic 或当作 ok —— 这里用已关闭的连接制造「检查没法通过」。
func TestRunMainDatabaseQuickCheckReportsAFailedQuickCheck(t *testing.T) {
	db := openIntegrityTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}
	withMainDatabaseInstance(t, db)

	report, err := RunMainDatabaseQuickCheck()
	if err != nil {
		// 句柄存在时检查本身应当跑完（把失败放进报告），而不是走「库没就绪」那条路。
		t.Fatalf("the check ran against an existing handle, so no top-level error is expected: %v", err)
	}
	if report.QuickCheckErr == nil {
		t.Fatal("a failed quick_check was not reported")
	}
	if report.OK() {
		t.Fatal("OK() must be false when quick_check failed")
	}
}

// 缺认证索引必须出现在报告里，而 quick_check 那一项保持干净（两个问题要分得开）。
func TestRunMainDatabaseQuickCheckReportsMissingIndexes(t *testing.T) {
	db := openIntegrityTestDatabase(t,
		`CREATE TABLE users (uuid TEXT PRIMARY KEY, username TEXT NOT NULL)`,
		`CREATE TABLE clients (uuid TEXT PRIMARY KEY, token TEXT NOT NULL)`,
	)
	withMainDatabaseInstance(t, db)

	report, err := RunMainDatabaseQuickCheck()
	if err != nil {
		t.Fatalf("RunMainDatabaseQuickCheck: %v", err)
	}
	if report.QuickCheckErr != nil {
		t.Fatalf("the fixture is structurally sound, quick_check should pass: %v", report.QuickCheckErr)
	}
	reportText := strings.Join(report.MissingIndexes, "\n")
	for _, want := range []string{"users.username", "clients.token"} {
		if !strings.Contains(reportText, want) {
			t.Errorf("%q was not reported as missing: %v", want, report.MissingIndexes)
		}
	}
	if report.OK() {
		t.Fatal("OK() must be false when an authentication index is missing")
	}
}

// 库还没就绪时返回错误：不初始化、不 panic、也不能静默地报 ok。
func TestRunMainDatabaseQuickCheckWithoutADatabase(t *testing.T) {
	withMainDatabaseInstance(t, nil)
	if _, err := RunMainDatabaseQuickCheck(); err == nil {
		t.Fatal("an uninitialised main database must be reported as an error, not checked silently")
	}
}
