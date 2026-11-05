package tasks

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	HistoryPageSize       = 20
	HistoryResultPageSize = 20
	HistoryCountLimit     = 1000
	HistoryChunkBytes     = 16384
	HistoryOffsetLimit    = 1<<53 - 1
	HistoryFieldMaxBytes  = 8 << 20
	HistorySQLiteMaxBytes = 16 << 20
)

var ErrInvalidHistoryParams = errors.New("invalid execution history parameters")

type HistorySummary struct {
	TaskID             string     `json:"task_id"`
	TargetCount        *int       `json:"target_count"`
	ResultCount        int        `json:"result_count"`
	PendingCount       int        `json:"pending_count"`
	SuccessCount       int        `json:"success_count"`
	FailedCount        int        `json:"failed_count"`
	UnknownCount       int        `json:"unknown_count"`
	CountsTruncated    bool       `json:"counts_truncated"`
	EarliestResultTime *time.Time `json:"earliest_result_time"`
	TimeIncomplete     bool       `json:"time_incomplete"`
	Status             string     `json:"status"`
}

type HistoryPage struct {
	Tasks      []HistorySummary `json:"tasks"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type HistoryResult struct {
	Cursor          string     `json:"cursor"`
	Client          string     `json:"client"`
	ClientTruncated bool       `json:"client_truncated"`
	CreatedAt       *time.Time `json:"created_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	ExitCode        *int       `json:"exit_code"`
	OutputBytes     int64      `json:"output_bytes"`
	OutputAvailable bool       `json:"output_available"`
}

type HistoryDetail struct {
	TaskID     string          `json:"task_id"`
	Results    []HistoryResult `json:"results"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type HistoryChunk struct {
	Data       []byte `json:"data_base64"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	TotalBytes int64  `json:"total_bytes"`
	HasMore    bool   `json:"has_more"`
	Available  bool   `json:"available"`
}

func historyDB(ctx context.Context) *gorm.DB {
	// A SQL error or slow-query log must never interpolate sensitive parameters.
	return dbcore.GetDBInstance().WithContext(ctx).Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

// SQLite materializes values before substr/length runs. Constrain only the
// exclusively leased connection, then restore its previous pool setting.
func withHistoryLimit(db *gorm.DB, query func(*gorm.DB) error) error {
	return db.Connection(func(tx *gorm.DB) error {
		conn, ok := tx.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return errors.New("history requires a dedicated SQLite connection")
		}
		var previous int
		if err := conn.Raw(func(driverConn any) error {
			sqliteConn, ok := driverConn.(*sqlite3.SQLiteConn)
			if !ok {
				return errors.New("history requires the SQLite driver")
			}
			previous = sqliteConn.GetLimit(sqlite3.SQLITE_LIMIT_LENGTH)
			if previous > HistorySQLiteMaxBytes {
				sqliteConn.SetLimit(sqlite3.SQLITE_LIMIT_LENGTH, HistorySQLiteMaxBytes)
			} else if previous < HistorySQLiteMaxBytes {
				return errors.New("SQLite length limit is below the history safety limit")
			}
			return nil
		}); err != nil {
			return err
		}
		defer conn.Raw(func(driverConn any) error {
			driverConn.(*sqlite3.SQLiteConn).SetLimit(sqlite3.SQLITE_LIMIT_LENGTH, previous)
			return nil
		})
		return query(tx)
	})
}

func validHistoryID(value string) bool {
	return value != "" && len(value) <= 36 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

type historyResultCursor struct {
	Client    string `json:"c"`
	RowID     int64  `json:"r"`
	CreatedAt string `json:"t"`
}

type historyTaskCursor struct {
	Order  int64  `json:"o"`
	TaskID string `json:"t"`
}

func encodeTaskCursor(order int64, taskID string) string {
	raw, _ := json.Marshal(historyTaskCursor{Order: order, TaskID: taskID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeTaskCursor(encoded string) (historyTaskCursor, error) {
	var cursor historyTaskCursor
	if len(encoded) > 512 {
		return cursor, ErrInvalidHistoryParams
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Order <= 0 || !validHistoryID(cursor.TaskID) {
		return historyTaskCursor{}, ErrInvalidHistoryParams
	}
	return cursor, nil
}

func decodeResultCursor(encoded string) (historyResultCursor, error) {
	var cursor historyResultCursor
	if len(encoded) > 512 {
		return cursor, ErrInvalidHistoryParams
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(raw, &cursor) != nil || !validHistoryID(cursor.Client) || cursor.RowID <= 0 || cursor.CreatedAt == "" || len(cursor.CreatedAt) > 128 || !utf8.ValidString(cursor.CreatedAt) {
		return historyResultCursor{}, ErrInvalidHistoryParams
	}
	return cursor, nil
}

func encodeResultCursor(client string, rowID int64, createdAt string) string {
	raw, _ := json.Marshal(historyResultCursor{Client: client, RowID: rowID, CreatedAt: createdAt})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ListHistory follows persisted insertion order, not timestamps: legacy parent
// tasks lack creation times and retained result times may disappear.
func ListHistory(ctx context.Context, cursor string) (HistoryPage, error) {
	var page HistoryPage
	err := withHistoryLimit(historyDB(ctx), func(db *gorm.DB) error {
		var err error
		page, err = listHistory(db, cursor)
		return err
	})
	return page, err
}

func listHistory(db *gorm.DB, cursor string) (HistoryPage, error) {
	var after historyTaskCursor
	var err error
	if cursor != "" {
		after, err = decodeTaskCursor(cursor)
		if err != nil {
			return HistoryPage{}, err
		}
	}
	var parents []struct {
		TaskID       string
		HistoryOrder int64
		TargetCount  *int
	}
	query := `SELECT CAST(substr(CAST(task_id AS BLOB), 1, 37) AS TEXT) AS task_id, history_order, CASE WHEN coalesce(octet_length(clients), 0) <= 1048576 AND json_valid(clients) THEN CASE WHEN json_type(clients) = 'array' THEN json_array_length(clients) END END AS target_count FROM tasks AS t `
	if cursor == "" {
		err = db.Raw(query+`WHERE history_order IS NOT NULL ORDER BY history_order DESC, t.task_id DESC LIMIT ?`, HistoryPageSize+1).Scan(&parents).Error
	} else {
		err = db.Raw(query+`WHERE (history_order, t.task_id) < (?, ?) ORDER BY history_order DESC, t.task_id DESC LIMIT ?`, after.Order, after.TaskID, HistoryPageSize+1).Scan(&parents).Error
	}
	if err != nil {
		return HistoryPage{}, err
	}
	for _, parent := range parents {
		if !validHistoryID(parent.TaskID) || parent.HistoryOrder <= 0 {
			return HistoryPage{}, ErrInvalidHistoryParams
		}
	}
	page := HistoryPage{Tasks: make([]HistorySummary, 0, HistoryPageSize)}
	if len(parents) > HistoryPageSize {
		parents = parents[:HistoryPageSize]
		last := parents[len(parents)-1]
		page.NextCursor = encodeTaskCursor(last.HistoryOrder, last.TaskID)
	}
	for _, parent := range parents {
		var results []struct {
			CreatedAt  *time.Time
			FinishedAt *time.Time
			ExitCode   *int
		}
		err = db.Raw(`SELECT created_at, finished_at, exit_code FROM task_results WHERE task_id = ? ORDER BY rowid LIMIT ?`, parent.TaskID, HistoryCountLimit+1).Scan(&results).Error
		if err != nil {
			return HistoryPage{}, err
		}
		summary := HistorySummary{TaskID: parent.TaskID, TargetCount: parent.TargetCount}
		if len(results) > HistoryCountLimit {
			summary.CountsTruncated = true
			results = results[:HistoryCountLimit]
		}
		summary.ResultCount = len(results)
		missingResultTime := false
		for _, result := range results {
			if result.CreatedAt == nil {
				missingResultTime = true
			} else if summary.EarliestResultTime == nil || result.CreatedAt.Before(*summary.EarliestResultTime) {
				created := result.CreatedAt.UTC()
				summary.EarliestResultTime = &created
			}
			switch {
			case result.FinishedAt == nil:
				summary.PendingCount++
			case result.ExitCode == nil:
				summary.UnknownCount++
			case *result.ExitCode == 0:
				summary.SuccessCount++
			default:
				summary.FailedCount++
			}
		}
		summary.TimeIncomplete = missingResultTime || summary.CountsTruncated || summary.TargetCount == nil || *summary.TargetCount != summary.ResultCount
		if summary.ResultCount == 0 {
			summary.Status = "no_retained_results"
		} else if summary.TargetCount == nil || summary.CountsTruncated || *summary.TargetCount != summary.ResultCount {
			summary.Status = "incomplete"
		} else {
			summary.Status = "retained"
		}
		page.Tasks = append(page.Tasks, summary)
	}
	return page, nil
}

func GetHistoryDetail(ctx context.Context, taskID, cursor string) (HistoryDetail, error) {
	var detail HistoryDetail
	err := withHistoryLimit(historyDB(ctx), func(db *gorm.DB) error {
		var err error
		detail, err = getHistoryDetail(db, taskID, cursor)
		return err
	})
	return detail, err
}

func getHistoryDetail(db *gorm.DB, taskID, cursor string) (HistoryDetail, error) {
	if !validHistoryID(taskID) {
		return HistoryDetail{}, ErrInvalidHistoryParams
	}
	var after historyResultCursor
	var err error
	if cursor != "" {
		after, err = decodeResultCursor(cursor)
		if err != nil {
			return HistoryDetail{}, err
		}
	}
	var parent struct{ TaskID string }
	if err = db.Raw(`SELECT task_id FROM tasks WHERE task_id = ?`, taskID).Scan(&parent).Error; err != nil {
		return HistoryDetail{}, err
	}
	if parent.TaskID == "" {
		return HistoryDetail{}, gorm.ErrRecordNotFound
	}
	var rows []struct {
		RowID           int64
		Client          string
		ClientTruncated bool
		CreatedAt       *time.Time
		CreatedKey      string
		FinishedAt      *time.Time
		ExitCode        *int
		OutputBytes     int64
		OutputAvailable bool
	}
	query := `SELECT rowid AS row_id, CAST(substr(CAST(client AS BLOB), 1, 37) AS TEXT) AS client, length(CAST(client AS BLOB)) > 36 AS client_truncated, created_at, substr(CAST(created_at AS TEXT), 1, 129) AS created_key, finished_at, exit_code, coalesce(octet_length(result), 0) AS output_bytes, coalesce(octet_length(result), 0) <= ? AS output_available FROM task_results AS r WHERE task_id = ? `
	if cursor == "" {
		err = db.Raw(query+`ORDER BY r.client, rowid LIMIT ?`, HistoryFieldMaxBytes, taskID, HistoryResultPageSize+1).Scan(&rows).Error
	} else {
		var present int
		result := db.Raw(`SELECT 1 FROM task_results WHERE task_id = ? AND client = ? AND rowid = ? AND CAST(created_at AS TEXT) = ?`, taskID, after.Client, after.RowID, after.CreatedAt).Scan(&present)
		if result.Error != nil {
			return HistoryDetail{}, result.Error
		}
		if result.RowsAffected == 0 {
			return HistoryDetail{}, gorm.ErrRecordNotFound
		}
		err = db.Raw(query+`AND (r.client, rowid) > (?, ?) ORDER BY r.client, rowid LIMIT ?`, HistoryFieldMaxBytes, taskID, after.Client, after.RowID, HistoryResultPageSize+1).Scan(&rows).Error
	}
	if err != nil {
		return HistoryDetail{}, err
	}
	detail := HistoryDetail{TaskID: taskID, Results: make([]HistoryResult, 0, HistoryResultPageSize)}
	if len(rows) > HistoryResultPageSize {
		rows = rows[:HistoryResultPageSize]
		last := rows[len(rows)-1]
		if last.ClientTruncated || !validHistoryID(last.Client) || last.CreatedKey == "" || len(last.CreatedKey) > 128 {
			return HistoryDetail{}, ErrInvalidHistoryParams
		}
		detail.NextCursor = encodeResultCursor(last.Client, last.RowID, last.CreatedKey)
	}
	for _, row := range rows {
		if row.ClientTruncated || !validHistoryID(row.Client) || row.CreatedKey == "" || len(row.CreatedKey) > 128 {
			return HistoryDetail{}, ErrInvalidHistoryParams
		}
		detail.Results = append(detail.Results, HistoryResult{Cursor: encodeResultCursor(row.Client, row.RowID, row.CreatedKey), Client: row.Client, ClientTruncated: row.ClientTruncated, CreatedAt: row.CreatedAt, FinishedAt: row.FinishedAt, ExitCode: row.ExitCode, OutputBytes: row.OutputBytes, OutputAvailable: row.OutputAvailable})
	}
	return detail, nil
}

// ReadHistoryChunk returns a bounded, explicit slice; callers may request the
// next offset without fetching an unbounded command or agent result.
func ReadHistoryChunk(ctx context.Context, taskID, kind, resultCursor string, offset int64) (HistoryChunk, error) {
	var chunk HistoryChunk
	err := withHistoryLimit(historyDB(ctx), func(db *gorm.DB) error {
		var err error
		chunk, err = readHistoryChunk(db, taskID, kind, resultCursor, offset)
		return err
	})
	return chunk, err
}

func readHistoryChunk(db *gorm.DB, taskID, kind, resultCursor string, offset int64) (HistoryChunk, error) {
	if !validHistoryID(taskID) || offset < 0 || offset > HistoryOffsetLimit {
		return HistoryChunk{}, ErrInvalidHistoryParams
	}
	var row struct {
		Data       []byte
		TotalBytes int64
		Available  bool
	}
	var result *gorm.DB
	switch kind {
	case "command":
		if resultCursor != "" {
			return HistoryChunk{}, ErrInvalidHistoryParams
		}
		result = db.Raw(`SELECT CASE WHEN coalesce(octet_length(command), 0) <= ? THEN substr(CAST(coalesce(command, '') AS BLOB), ?, ?) END AS data, coalesce(octet_length(command), 0) AS total_bytes, coalesce(octet_length(command), 0) <= ? AS available FROM tasks WHERE task_id = ?`, HistoryFieldMaxBytes, offset+1, HistoryChunkBytes, HistoryFieldMaxBytes, taskID).Scan(&row)
	case "output":
		cursor, err := decodeResultCursor(resultCursor)
		if err != nil {
			return HistoryChunk{}, ErrInvalidHistoryParams
		}
		result = db.Raw(`SELECT CASE WHEN coalesce(octet_length(result), 0) <= ? THEN substr(CAST(coalesce(result, '') AS BLOB), ?, ?) END AS data, coalesce(octet_length(result), 0) AS total_bytes, coalesce(octet_length(result), 0) <= ? AS available FROM task_results WHERE task_id = ? AND client = ? AND rowid = ? AND CAST(created_at AS TEXT) = ?`, HistoryFieldMaxBytes, offset+1, HistoryChunkBytes, HistoryFieldMaxBytes, taskID, cursor.Client, cursor.RowID, cursor.CreatedAt).Scan(&row)
	default:
		return HistoryChunk{}, ErrInvalidHistoryParams
	}
	if result.Error != nil {
		return HistoryChunk{}, result.Error
	}
	if result.RowsAffected == 0 {
		return HistoryChunk{}, gorm.ErrRecordNotFound
	}
	if row.Data == nil {
		row.Data = []byte{}
	}
	if !row.Available {
		return HistoryChunk{Data: row.Data, Offset: offset, NextOffset: offset, TotalBytes: row.TotalBytes}, nil
	}
	// Do not split a valid trailing UTF-8 sequence when a browser requests the
	// next byte chunk. Invalid stored bytes remain available as base64.
	if offset+int64(len(row.Data)) < row.TotalBytes {
		for i := len(row.Data) - 1; i >= 0 && i >= len(row.Data)-4; i-- {
			if utf8.RuneStart(row.Data[i]) {
				if !utf8.FullRune(row.Data[i:]) {
					row.Data = row.Data[:i]
				}
				break
			}
		}
	}
	next := offset + int64(len(row.Data))
	return HistoryChunk{Data: row.Data, Offset: offset, NextOffset: next, TotalBytes: row.TotalBytes, HasMore: next < row.TotalBytes, Available: true}, nil
}
