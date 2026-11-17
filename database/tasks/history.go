package tasks

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	HistoryChunkBytes     = 16384
	HistoryOffsetLimit    = 1<<53 - 1
	HistorySQLiteMaxBytes = 16 << 20
	// Addressability bound for a client or task ID that may be displayed.
	HistoryClientMaxBytes = 128
	// A boundary value only has to travel inside a cursor, so it may be longer
	// than a displayable one; this is what keeps a page made entirely of
	// unaddressable rows from stranding everything behind it.
	HistoryCursorValueMaxBytes = 512
	HistoryCursorMaxEncoded    = 1024
	HistoryClientDisplay       = 36
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
	EarliestResultTime *time.Time `json:"earliest_result_time"`
	TimeIncomplete     bool       `json:"time_incomplete"`
	Status             string     `json:"status"`
}

type HistoryPage struct {
	Tasks            []HistorySummary `json:"tasks"`
	NextCursor       string           `json:"next_cursor,omitempty"`
	SkippedMalformed int              `json:"skipped_malformed"`
	Blocked          bool             `json:"blocked"`
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
	TaskID           string          `json:"task_id"`
	Results          []HistoryResult `json:"results"`
	NextCursor       string          `json:"next_cursor,omitempty"`
	SkippedMalformed int             `json:"skipped_malformed"`
	Blocked          bool            `json:"blocked"`
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

// Guard other malformed fields from SQLite materialization. Constrain only
// the exclusively leased connection, then restore its previous pool setting.
func withHistoryLimit(db *gorm.DB, query func(*gorm.DB) error) error {
	return db.Connection(func(tx *gorm.DB) (err error) {
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
		// A dropped restore error would leave the shared main connection at the
		// reduced limit for the process lifetime, so unrelated large writes would
		// fail with "string or blob too big" until a restart. Report it instead.
		defer func() {
			restoreErr := conn.Raw(func(driverConn any) error {
				sqliteConn, ok := driverConn.(*sqlite3.SQLiteConn)
				if !ok {
					return errors.New("history requires the SQLite driver")
				}
				sqliteConn.SetLimit(sqlite3.SQLITE_LIMIT_LENGTH, previous)
				return nil
			})
			if restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore SQLite length limit: %w", restoreErr))
			}
		}()
		return query(tx)
	})
}

func validHistoryID(value string) bool {
	return value != "" && len(value) <= 36 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

// A target client is normally a node UUID, but a legacy row may hold a longer
// label. Rows up to this bound stay addressable (their full value is carried in
// the cursor); longer, NUL-bearing or non-UTF-8 rows cannot be addressed and
// are skipped and counted instead of failing the page.
func validHistoryClient(value string) bool {
	return value != "" && len(value) <= HistoryClientMaxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

type historyResultCursor struct {
	Client   []byte `json:"c"`
	RowID    int64  `json:"r"`
	Identity string `json:"i"`
}

type historyTaskCursor struct {
	Order  int64  `json:"o"`
	TaskID []byte `json:"t"`
}

func encodeTaskCursor(order int64, taskID string) string {
	raw, _ := json.Marshal(historyTaskCursor{Order: order, TaskID: []byte(taskID)})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeTaskCursor(encoded string) (historyTaskCursor, error) {
	var cursor historyTaskCursor
	if len(encoded) > HistoryCursorMaxEncoded {
		return cursor, ErrInvalidHistoryParams
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	// The order value is a position, not an identifier: a boundary row may sit at
	// any order, so it is not narrowed to positive values here.
	if err != nil || json.Unmarshal(raw, &cursor) != nil || len(cursor.TaskID) == 0 || len(cursor.TaskID) > HistoryCursorValueMaxBytes {
		return historyTaskCursor{}, ErrInvalidHistoryParams
	}
	return cursor, nil
}

func decodeResultCursor(encoded string) (historyResultCursor, error) {
	var cursor historyResultCursor
	if len(encoded) > HistoryCursorMaxEncoded {
		return cursor, ErrInvalidHistoryParams
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	// SQLite rowids are signed and may legitimately be zero or negative. A
	// pointer distinguishes rowid zero from a missing or null cursor field.
	var wire struct {
		Client   []byte `json:"c"`
		RowID    *int64 `json:"r"`
		Identity string `json:"i"`
	}
	if err != nil || json.Unmarshal(raw, &wire) != nil || len(wire.Client) > HistoryCursorValueMaxBytes || wire.RowID == nil {
		return historyResultCursor{}, ErrInvalidHistoryParams
	}
	// An empty identity marks a boundary-only cursor: it names a position rather
	// than a row, and every path that needs a row refuses it.
	if wire.Identity != "" && !validResultIdentity(wire.Identity) {
		return historyResultCursor{}, ErrInvalidHistoryParams
	}
	cursor = historyResultCursor{Client: wire.Client, RowID: *wire.RowID, Identity: wire.Identity}
	return cursor, nil
}

func validResultIdentity(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if !('0' <= c && c <= '9') && !('a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

func encodeResultCursor(client string, rowID int64, identity string) string {
	raw, _ := json.Marshal(historyResultCursor{Client: []byte(client), RowID: rowID, Identity: identity})
	return base64.RawURLEncoding.EncodeToString(raw)
}

type historyParent struct {
	TaskID       string
	HistoryOrder int64
	TargetCount  *int
}

type historyAggregate struct {
	Total       int
	Pending     int
	Unknown     int
	Success     int
	Failed      int
	MissingTime int
	Earliest    *time.Time
}

// summariseResults answers one page of parents in a single statement. The counts
// are exact: no result rows reach Go, so a per-task ceiling would only have made
// the numbers wrong.
func summariseResults(db *gorm.DB, parents []historyParent) (map[string]historyAggregate, error) {
	out := make(map[string]historyAggregate, len(parents))
	if len(parents) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(parents))
	args := make([]any, len(parents))
	for i, parent := range parents {
		placeholders[i] = "?"
		args[i] = parent.TaskID
	}
	var rows []struct {
		TaskID      string
		Total       int
		Pending     int
		Unknown     int
		Success     int
		Failed      int
		MissingTime int
		// created_at is stored as text, so the aggregate comes back as one; the
		// UTC form keeps it parseable whatever offset the row was written with.
		Earliest *string
	}
	err := db.Raw(`SELECT task_id, count(*) AS total,
		sum(CASE WHEN finished_at IS NULL THEN 1 ELSE 0 END) AS pending,
		sum(CASE WHEN finished_at IS NOT NULL AND exit_code IS NULL THEN 1 ELSE 0 END) AS unknown,
		sum(CASE WHEN finished_at IS NOT NULL AND exit_code = 0 THEN 1 ELSE 0 END) AS success,
		sum(CASE WHEN finished_at IS NOT NULL AND exit_code IS NOT NULL AND exit_code <> 0 THEN 1 ELSE 0 END) AS failed,
		sum(CASE WHEN created_at IS NULL THEN 1 ELSE 0 END) AS missing_time,
		strftime('%Y-%m-%dT%H:%M:%fZ', min(created_at)) AS earliest
		FROM task_results WHERE task_id IN (`+strings.Join(placeholders, ",")+`) GROUP BY task_id`, args...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var earliest *time.Time
		if row.Earliest != nil {
			parsed, err := time.Parse(time.RFC3339Nano, *row.Earliest)
			if err != nil {
				return nil, fmt.Errorf("parse earliest result time: %w", err)
			}
			earliest = &parsed
		}
		out[row.TaskID] = historyAggregate{
			Total: row.Total, Pending: row.Pending, Unknown: row.Unknown,
			Success: row.Success, Failed: row.Failed, MissingTime: row.MissingTime,
			Earliest: earliest,
		}
	}
	return out, nil
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
	var parents []historyParent
	query := fmt.Sprintf(`SELECT CASE WHEN octet_length(task_id) <= %d AND instr(CAST(task_id AS BLOB), x'00') = 0 THEN CAST(task_id AS TEXT) ELSE '' END AS task_id, history_order, CASE WHEN coalesce(octet_length(clients), 0) <= 1048576 AND json_valid(clients) THEN CASE WHEN json_type(clients) = 'array' THEN json_array_length(clients) END END AS target_count FROM tasks AS t `, HistoryCursorValueMaxBytes)
	if cursor == "" {
		err = db.Raw(query+`WHERE history_order IS NOT NULL ORDER BY history_order DESC, t.task_id DESC LIMIT ?`, HistoryPageSize+1).Scan(&parents).Error
	} else {
		err = db.Raw(query+`WHERE (history_order, t.task_id) < (?, ?) ORDER BY history_order DESC, t.task_id DESC LIMIT ?`, after.Order, string(after.TaskID), HistoryPageSize+1).Scan(&parents).Error
	}
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Tasks: make([]HistorySummary, 0, HistoryPageSize)}
	more := len(parents) > HistoryPageSize
	if more {
		parents = parents[:HistoryPageSize]
	}
	// A malformed parent must not take the whole list with it: skip it and count
	// it. The cursor is minted below from the last row actually fetched, not the
	// last good one, so a full window of bad rows still leads somewhere instead
	// of stranding everything behind it.
	addressable := make([]historyParent, 0, len(parents))
	for _, parent := range parents {
		if !validHistoryID(parent.TaskID) || parent.HistoryOrder <= 0 {
			page.SkippedMalformed++
			continue
		}
		addressable = append(addressable, parent)
	}
	// One aggregate statement for the whole page, not one per parent: the page
	// holds the single main connection for a fraction of the time, and the counts
	// are exact because nothing is materialised in Go.
	aggregates, err := summariseResults(db, addressable)
	if err != nil {
		return HistoryPage{}, err
	}
	for _, parent := range addressable {
		agg := aggregates[parent.TaskID]
		summary := HistorySummary{
			TaskID:       parent.TaskID,
			TargetCount:  parent.TargetCount,
			ResultCount:  agg.Total,
			PendingCount: agg.Pending,
			UnknownCount: agg.Unknown,
			SuccessCount: agg.Success,
			FailedCount:  agg.Failed,
		}
		if agg.Earliest != nil {
			created := agg.Earliest.UTC()
			summary.EarliestResultTime = &created
		}
		summary.TimeIncomplete = agg.MissingTime > 0 || summary.TargetCount == nil || *summary.TargetCount != summary.ResultCount
		switch {
		case summary.ResultCount == 0:
			summary.Status = "no_retained_results"
		case summary.TargetCount == nil || *summary.TargetCount != summary.ResultCount:
			summary.Status = "incomplete"
		default:
			summary.Status = "retained"
		}
		page.Tasks = append(page.Tasks, summary)
	}
	if more {
		// Walk back to the last row whose key can actually travel: one untravelable
		// ID at the window edge must not hide the rows behind it. Rows after that
		// position are fetched again and counted again on the next page, which the
		// contract allows. If none can travel, no honest cursor exists and the
		// response says so instead of looping over the same window.
		var boundaryID string
		var boundaryOrder int64
		for i := len(parents) - 1; i >= 0; i-- {
			if parents[i].TaskID != "" {
				boundaryID, boundaryOrder = parents[i].TaskID, parents[i].HistoryOrder
				break
			}
		}
		if boundaryID == "" {
			page.Blocked = true
		} else {
			page.NextCursor = encodeTaskCursor(boundaryOrder, boundaryID)
		}
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
		ClientFull      string
		ClientTruncated bool
		CreatedAt       *time.Time
		Identity        string
		FinishedAt      *time.Time
		ExitCode        *int
		OutputBytes     int64
		OutputAvailable bool
	}
	// The displayed client is a bounded prefix; the full value is carried
	// separately so a cursor can address the row exactly and the UI can say
	// truthfully that what it shows is shortened.
	query := fmt.Sprintf(`SELECT rowid AS row_id, substr(CAST(client AS TEXT), 1, %d) AS client, CASE WHEN octet_length(client) <= %d AND instr(CAST(client AS BLOB), x'00') = 0 THEN CAST(client AS TEXT) ELSE '' END AS client_full, length(CAST(client AS TEXT)) > %d AS client_truncated, created_at, CASE WHEN octet_length(history_identity) = 32 THEN CAST(history_identity AS TEXT) ELSE '' END AS identity, finished_at, exit_code, coalesce(octet_length(result), 0) AS output_bytes, typeof(result) IN ('null', 'text', 'blob') AS output_available FROM task_results AS r WHERE task_id = ? `, HistoryClientDisplay, HistoryCursorValueMaxBytes, HistoryClientDisplay)
	if cursor == "" {
		err = db.Raw(query+`ORDER BY r.client, rowid LIMIT ?`, taskID, HistoryResultPageSize+1).Scan(&rows).Error
	} else {
		// A cursor that names a row must still name that row; a boundary-only
		// cursor (empty identity) is a position and is used without that check.
		if after.Identity != "" {
			var present int
			result := db.Raw(`SELECT 1 FROM task_results WHERE task_id = ? AND client = ? AND rowid = ? AND history_identity = ?`, taskID, string(after.Client), after.RowID, after.Identity).Scan(&present)
			if result.Error != nil {
				return HistoryDetail{}, result.Error
			}
			if result.RowsAffected == 0 {
				return HistoryDetail{}, gorm.ErrRecordNotFound
			}
		}
		err = db.Raw(query+`AND (r.client, rowid) > (?, ?) ORDER BY r.client, rowid LIMIT ?`, taskID, string(after.Client), after.RowID, HistoryResultPageSize+1).Scan(&rows).Error
	}
	if err != nil {
		return HistoryDetail{}, err
	}
	detail := HistoryDetail{TaskID: taskID, Results: make([]HistoryResult, 0, HistoryResultPageSize)}
	more := len(rows) > HistoryResultPageSize
	if more {
		rows = rows[:HistoryResultPageSize]
	}
	// An unaddressable row is skipped and counted instead of failing the page.
	// The next cursor comes from the last row fetched, good or not, so a window
	// made entirely of unaddressable rows still advances.
	for _, row := range rows {
		if row.ClientFull == "" || !validHistoryClient(row.ClientFull) || !validResultIdentity(row.Identity) {
			detail.SkippedMalformed++
			continue
		}
		detail.Results = append(detail.Results, HistoryResult{
			Cursor:          encodeResultCursor(row.ClientFull, row.RowID, row.Identity),
			Client:          row.Client,
			ClientTruncated: row.ClientTruncated,
			CreatedAt:       row.CreatedAt,
			FinishedAt:      row.FinishedAt,
			ExitCode:        row.ExitCode,
			OutputBytes:     row.OutputBytes,
			OutputAvailable: row.OutputAvailable,
		})
	}
	if more {
		var boundary string
		var boundaryRowID int64
		var boundaryIdentity string
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].ClientFull == "" {
				continue
			}
			boundary, boundaryRowID = rows[i].ClientFull, rows[i].RowID
			// Only a row the page could show may be named as a row; a skipped
			// row's position still carries the reader past it, but its cursor is
			// boundary-only so no read path can refetch its output.
			if validHistoryClient(boundary) && validResultIdentity(rows[i].Identity) {
				boundaryIdentity = rows[i].Identity
			}
			break
		}
		if boundary == "" {
			detail.Blocked = true
		} else {
			detail.NextCursor = encodeResultCursor(boundary, boundaryRowID, boundaryIdentity)
		}
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
		result = db.Raw(`SELECT CASE typeof(command) WHEN 'null' THEN x'' WHEN 'text' THEN nekomari_history_blob(0, rowid, ?) WHEN 'blob' THEN nekomari_history_blob(0, rowid, ?) END AS data, coalesce(octet_length(command), 0) AS total_bytes, typeof(command) IN ('null', 'text', 'blob') AS available FROM tasks WHERE task_id = ?`, offset, offset, taskID).Scan(&row)
	case "output":
		cursor, err := decodeResultCursor(resultCursor)
		if err != nil {
			return HistoryChunk{}, ErrInvalidHistoryParams
		}
		// A boundary-only cursor names a position, not a row, so it cannot be used
		// to read anyone's output.
		if cursor.Identity == "" {
			return HistoryChunk{}, ErrInvalidHistoryParams
		}
		result = db.Raw(`SELECT CASE typeof(result) WHEN 'null' THEN x'' WHEN 'text' THEN nekomari_history_blob(1, rowid, ?) WHEN 'blob' THEN nekomari_history_blob(1, rowid, ?) END AS data, coalesce(octet_length(result), 0) AS total_bytes, typeof(result) IN ('null', 'text', 'blob') AS available FROM task_results WHERE task_id = ? AND client = ? AND rowid = ? AND history_identity = ?`, offset, offset, taskID, string(cursor.Client), cursor.RowID, cursor.Identity).Scan(&row)
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
