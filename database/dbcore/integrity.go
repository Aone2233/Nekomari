package dbcore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	logger "github.com/Aone2233/nekomari/utils/log"
	"gorm.io/gorm"
)

// 主库的完整性检查（OPEN-WORK「认证依赖索引，索引不一致时静默变成 401」那条）。
//
// 两个检查回答的是不同的问题，缺一不可：
//
//   - quick_check 看的是**文件与页结构**：截断、悬空指针、同一页被引用两次。它抓不到
//     「索引里少了一行」—— 2026-10-01 的事故里 sqlite_autoindex_clients_2 少了一条记录，
//     quick_check 全程 ok，而令牌查询正是走那个索引，于是面板拒绝了自己数据库里的令牌
//     （"row 6 missing from index" 只有 integrity_check 报得出来）。
//   - 索引存在性检查看的是**schema**：AutoMigrate 没建出唯一索引（或有人 DROP 掉）时，
//     认证查询退化成全表扫描。表越大越慢，但结果仍然正确 —— 所以它只 Warn，不影响启动。
//
// 两者都不做在线 REINDEX：REINDEX 要独占数据库重写全部索引，那是运维动作（先备份、
// 停面板），不是启动流程能顺手做的事。

// mainDatabaseQuickCheck 对主库跑 PRAGMA quick_check，返回第一个问题。
//
// 用完整形式而不是 quick_check(1)：两者的代价对**健康**的库相同（都要读全部页），
// 差别只在遇到坏页时是否继续找。启动时多收集几条问题没有坏处，而只报第一条正是让
// 上次那次事故难以定位的原因之一。
func mainDatabaseQuickCheck(db *gorm.DB) error {
	if db == nil {
		return errors.New("main database handle is nil")
	}
	var result string
	if err := db.Raw("PRAGMA quick_check").Row().Scan(&result); err != nil {
		return fmt.Errorf("run PRAGMA quick_check: %w", err)
	}
	return quickCheckResultError(result)
}

// quickCheckResultError 把 quick_check 的输出变成错误："ok" 以外都算失败。
//
// 单独一个函数是为了让解析本身可测：构造一个真正损坏的库取决于 SQLite 版本与损坏位置，
// 而「哪些输出算通过」是这里唯一的判断逻辑。
func quickCheckResultError(result string) error {
	trimmed := strings.TrimSpace(result)
	if strings.EqualFold(trimmed, "ok") {
		return nil
	}
	return fmt.Errorf("PRAGMA quick_check reported %q", trimmed)
}

// authenticationIndex 描述一个认证路径依赖的索引：表、列，以及缺了它会发生什么。
//
// 只列三张表上的四列，每一条都对应一条真实查询，而不是「模型上所有 index 标签」：
//
//   - sessions.session：每个已登录请求都按它取会话（database/accounts/session_cache.go）；
//   - clients.token：agent 的每次上报都按它认客户端（database/clients/report.go）——
//     也就是 2026-10-01 那次「令牌明明在库里却 401」走的路；
//   - users.username：密码登录按它找账号（database/accounts/accounts.go）；
//   - users.uuid：会话 JOIN 回用户、以及按账号读写时用它（session_cache.go 的 JOIN）。
//
// 按**列**而不是按索引名检查：SQLite 为 PRIMARY KEY / UNIQUE 建的是
// sqlite_autoindex_<table>_<n>，编号随建表顺序变化；GORM 的 uniqueIndex 名字也会随
// 模型改动。名字对不上就误报，而列名是对查询本身的描述。
type authenticationIndex struct {
	table  string
	column string
	// impact 写进日志：缺索引是性能问题，运维需要知道它落在哪条路径上才决定要不要
	// 停机重建，只报一个列名等于没说。
	impact string
}

var authenticationIndexes = []authenticationIndex{
	{
		table:  "sessions",
		column: "session",
		impact: "every authenticated request resolves its session by this column; without it SQLite scans the whole sessions table on each request",
	},
	{
		table:  "clients",
		column: "token",
		impact: "agent authentication looks clients up by token; without it every report from every agent scans the clients table",
	},
	{
		table:  "users",
		column: "username",
		impact: "password login looks the account up by username; without it each login scans the users table",
	},
	{
		table:  "users",
		column: "uuid",
		impact: "session-to-user joins and per-account operations key on this column; without it each authenticated request scans the users table",
	},
}

// missingAuthenticationIndexes 返回主库里缺失的认证索引，每条都带影响说明。
//
// 表本身不存在时跳过而不是报告「缺索引」：那说明 AutoMigrate 还没建表（或失败了），
// 而调用方已经会把它当作建表错误处理 —— 把同一件事换个说法再报一遍只会误导。
func missingAuthenticationIndexes(db *gorm.DB) ([]string, error) {
	if db == nil {
		return nil, errors.New("main database handle is nil")
	}

	indexed := make(map[string]map[string]struct{}, len(authenticationIndexes))
	for _, requirement := range authenticationIndexes {
		if _, loaded := indexed[requirement.table]; loaded {
			continue
		}
		columns, err := sqliteIndexedColumns(db, requirement.table)
		if err != nil {
			return nil, err
		}
		if columns == nil {
			indexed[requirement.table] = nil // 表不存在
			continue
		}
		indexed[requirement.table] = columns
	}

	var missing []string
	for _, requirement := range authenticationIndexes {
		columns, tableExists := indexed[requirement.table]
		if !tableExists || columns == nil {
			continue
		}
		if _, ok := columns[requirement.column]; ok {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s.%s — %s", requirement.table, requirement.column, requirement.impact))
	}
	return missing, nil
}

// sqliteIndexedColumns 返回 table 上所有索引覆盖的列集合；表不存在时返回 (nil, nil)。
//
// 分两步查：先从 sqlite_master 取索引名，再逐个 PRAGMA index_info 取列。不能边遍历
// 索引名的结果集边发下一条查询 —— 主库的连接池只有一条连接（SetMaxOpenConns(1)），
// 那会自己把自己锁住。所以先把名字读完、关掉结果集，再查列。
func sqliteIndexedColumns(db *gorm.DB, table string) (map[string]struct{}, error) {
	var existing string
	err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Row().Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("look up table %s in sqlite_master: %w", table, err)
	}

	indexNames, err := sqliteIndexNames(db, table)
	if err != nil {
		return nil, err
	}

	columns := make(map[string]struct{})
	for _, indexName := range indexNames {
		// PRAGMA 的括号参数不能绑定占位符，只能拼进 SQL；索引名来自 sqlite_master，
		// 但「来自数据库」不等于「可信」（索引名可以带引号、空格），所以仍要转义。
		rows, err := db.Raw("PRAGMA index_info(" + quoteSQLiteIdentifier(indexName) + ")").Rows()
		if err != nil {
			return nil, fmt.Errorf("read columns of index %s: %w", indexName, err)
		}
		for rows.Next() {
			var sequence int
			var columnID sql.NullInt64
			var columnName sql.NullString
			if err := rows.Scan(&sequence, &columnID, &columnName); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan columns of index %s: %w", indexName, err)
			}
			// 表达式索引的列名是 NULL（或者是 -2 的表达式槽位），它不覆盖任何列。
			if columnName.Valid && columnName.String != "" {
				columns[columnName.String] = struct{}{}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read columns of index %s: %w", indexName, err)
		}
		rows.Close()
	}
	return columns, nil
}

// sqliteIndexNames 返回 table 在 sqlite_master 里登记的索引名（含自动索引）。
func sqliteIndexNames(db *gorm.DB, table string) ([]string, error) {
	rows, err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ?", table).Rows()
	if err != nil {
		return nil, fmt.Errorf("list indexes of %s: %w", table, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name sql.NullString
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan index name of %s: %w", table, err)
		}
		if name.Valid {
			names = append(names, name.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list indexes of %s: %w", table, err)
	}
	return names, nil
}

// quoteSQLiteIdentifier 给 SQLite 标识符加双引号（内部的双引号写两次）。
func quoteSQLiteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// verifyMainDatabaseIntegrity 在主库迁移完成后跑一次完整性检查。
//
// 返回 error 只表示 quick_check 失败：那意味着库本身是坏的，调用方应当让启动失败 ——
// 继续跑只会让每个查询各自失败（日志里是一堆互不相关的错误），而唯一能指导动作的那句
// 「库坏了，从备份恢复」会被埋掉。索引缺失是 Warn 而不是 error：缺索引只让认证变慢，
// 不让它出错，为它拒绝启动等于拿一个性能问题换一次停机。
func verifyMainDatabaseIntegrity(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if err := mainDatabaseQuickCheck(db); err != nil {
		logger.Errorf("dbcore",
			"main database failed PRAGMA quick_check: %v — restore it from data/backup/ or an off-host copy before trusting this instance", err)
		return err
	}
	missing, err := missingAuthenticationIndexes(db)
	if err != nil {
		// 查不出索引不等于库有问题（例如未来换成别的方言），只如实记录。
		logger.Warnf("dbcore", "could not verify the authentication indexes on the main database: %v", err)
		return nil
	}
	for _, problem := range missing {
		logger.Warnf("dbcore",
			"main database is missing an index the authentication path depends on: %s. Queries still return the right rows, but they fall back to a full table scan; after taking a backup, rebuild the indexes (REINDEX) to restore the fast path",
			problem)
	}
	return nil
}
