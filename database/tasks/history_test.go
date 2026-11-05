package tasks

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

func TestHistoryPagingBoundsAndRetention(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:task-history-test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	const prefix = "history-test-"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id LIKE ?", prefix+"%")
		db.Exec("DELETE FROM tasks WHERE task_id LIKE ?", prefix+"%")
	})
	for i := 0; i < 45; i++ {
		id := fmt.Sprintf("%s%02d", prefix, i)
		if err := CreateTask(id, []string{"missing-client"}, "secret-command"); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("DELETE FROM task_results WHERE task_id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
	}
	when := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	exit0, exit1 := 0, 1
	rows := []models.TaskResult{
		{TaskId: prefix + "44", Client: "deleted-node", Result: "secret-output", CreatedAt: when},
		{TaskId: prefix + "44", Client: "finished", Result: "secret-output", ExitCode: &exit0, FinishedAt: &when, CreatedAt: when},
		{TaskId: prefix + "44", Client: "failed", ExitCode: &exit1, FinishedAt: &when, CreatedAt: when},
		{TaskId: prefix + "44", Client: "unknown", FinishedAt: &when, CreatedAt: when},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	first, err := ListHistory(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != HistoryPageSize || first.NextCursor == "" || first.Tasks[0].TaskID != prefix+"44" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	s := first.Tasks[0]
	if s.Status != "incomplete" || s.PendingCount != 1 || s.SuccessCount != 1 || s.FailedCount != 1 || s.UnknownCount != 1 || s.EarliestResultTime == nil || !s.EarliestResultTime.Equal(when) || !s.TimeIncomplete {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if first.Tasks[1].Status != "no_retained_results" || first.Tasks[1].EarliestResultTime != nil {
		t.Fatalf("missing result must not claim a time or cause: %+v", first.Tasks[1])
	}
	raw, _ := json.Marshal(first)
	if bytes.Contains(raw, []byte("secret-command")) || bytes.Contains(raw, []byte("secret-output")) {
		t.Fatal("summary leaked command or output")
	}
	seen := map[string]bool{}
	for cursor := ""; ; {
		page, err := ListHistory(t.Context(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Tasks {
			if strings.HasPrefix(row.TaskID, prefix) {
				if seen[row.TaskID] {
					t.Fatalf("duplicate task %q", row.TaskID)
				}
				seen[row.TaskID] = true
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 45 {
		t.Fatalf("saw %d of 45 tasks", len(seen))
	}
	for _, invalid := range []string{strings.Repeat("x", 37), "\x00"} {
		if _, err := ListHistory(t.Context(), invalid); !errors.Is(err, ErrInvalidHistoryParams) {
			t.Errorf("cursor %q: %v", invalid, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListHistory(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
}

func TestHistoryIndexSupportsBoundedKeyset(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:task-history-test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	var plan []struct{ Detail string }
	if err := db.Raw(`EXPLAIN QUERY PLAN SELECT rowid, CAST(substr(CAST(client AS BLOB), 1, 37) AS TEXT) AS client FROM task_results AS r WHERE task_id = ? AND (r.client, rowid) > (?, ?) ORDER BY r.client, rowid LIMIT ?`, "task", "client", 1, HistoryResultPageSize+1).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	if !bytes.Contains(raw, []byte("idx_task_results_history")) || bytes.Contains(raw, []byte("TEMP B-TREE")) {
		t.Fatalf("unbounded history query plan: %s", raw)
	}
	plan = nil
	if err := db.Raw(`EXPLAIN QUERY PLAN SELECT CAST(substr(CAST(task_id AS BLOB), 1, 37) AS TEXT) AS task_id, history_order FROM tasks AS t WHERE (history_order,t.task_id) < (?,?) ORDER BY history_order DESC, t.task_id DESC LIMIT ?`, 1000, "task", HistoryPageSize+1).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(plan)
	if !bytes.Contains(raw, []byte("idx_tasks_history_order")) || bytes.Contains(raw, []byte("TEMP B-TREE")) {
		t.Fatalf("unbounded task query plan: %s", raw)
	}
	plan = nil
	if err := db.Raw(`EXPLAIN QUERY PLAN SELECT created_at, finished_at, exit_code FROM task_results WHERE task_id = ? ORDER BY rowid LIMIT ?`, "task", HistoryCountLimit+1).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(plan)
	if !bytes.Contains(raw, []byte("idx_task_results_task_id")) || bytes.Contains(raw, []byte("TEMP B-TREE")) {
		t.Fatalf("unbounded summary query plan: %s", raw)
	}
}

func TestHistoryResultKeysetSeeksWithManyRows(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := "history-keyset-work-test"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	if err := CreateTask(id, nil, "synthetic"); err != nil {
		t.Fatal(err)
	}
	rows := make([]models.TaskResult, 10000)
	for i := range rows {
		rows[i] = models.TaskResult{TaskId: id, Client: fmt.Sprintf("node-%05d", i), CreatedAt: time.Now().UTC()}
	}
	if err := db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	var cursorRow struct {
		RowID    int64
		Client   string
		Identity string
	}
	if err := db.Raw(`SELECT rowid AS row_id, client, history_identity AS identity FROM task_results WHERE task_id = ? AND client = ?`, id, "node-09900").Scan(&cursorRow).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	page, err := GetHistoryDetail(ctx, id, encodeResultCursor(cursorRow.Client, cursorRow.RowID, cursorRow.Identity))
	if err != nil || len(page.Results) != HistoryResultPageSize || page.Results[0].Client != "node-09901" {
		t.Fatalf("deep indexed seek: %d %v", len(page.Results), err)
	}
}

func TestCreateTaskOrderDoesNotRegressAfterRowIDChanges(t *testing.T) {
	db := dbcore.GetDBInstance()
	ids := []string{"history-order-first", "history-order-second"}
	t.Cleanup(func() { db.Exec("DELETE FROM tasks WHERE task_id IN ?", ids) })
	if err := CreateTask(ids[0], nil, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tasks SET history_order = 1000000000 WHERE task_id = ?", ids[0]).Error; err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(ids[1], nil, "secret"); err != nil {
		t.Fatal(err)
	}
	var task models.Task
	if err := db.Where("task_id = ?", ids[1]).First(&task).Error; err != nil || task.HistoryOrder == nil || *task.HistoryOrder <= 1000000000 {
		t.Fatalf("new task sorted behind migrated rows: %+v, %v", task, err)
	}
}

func TestCreateTaskAtomicConcurrentAndExhaustion(t *testing.T) {
	db := dbcore.GetDBInstance()
	const prefix = "history-atomic-test-"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id LIKE ?", prefix+"%")
		db.Exec("DELETE FROM tasks WHERE task_id LIKE ?", prefix+"%")
	})
	const count = 24
	var wg sync.WaitGroup
	errors := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errors <- CreateTask(fmt.Sprintf("%s%02d", prefix, i), []string{"node"}, "synthetic")
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var counts struct{ Tasks, Orders, Results int }
	if err := db.Raw(`SELECT count(*) AS tasks, count(DISTINCT history_order) AS orders,
		(SELECT count(*) FROM task_results WHERE task_id LIKE ?) AS results
		FROM tasks WHERE task_id LIKE ?`, prefix+"%", prefix+"%").Scan(&counts).Error; err != nil || counts.Tasks != count || counts.Orders != count || counts.Results != count {
		t.Fatalf("concurrent task creation: %+v %v", counts, err)
	}
	if err := db.Exec(`UPDATE tasks SET history_order = ? WHERE task_id = ?`, int64(math.MaxInt64), prefix+"00").Error; err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(prefix+"exhausted", []string{"node"}, "synthetic"); err == nil {
		t.Fatal("exhausted history order accepted")
	}
	var parents, results int64
	if err := db.Model(&models.Task{}).Where("task_id = ?", prefix+"exhausted").Count(&parents).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.TaskResult{}).Where("task_id = ?", prefix+"exhausted").Count(&results).Error; err != nil || parents != 0 || results != 0 {
		t.Fatalf("failed create left partial task/results: %d %d %v", parents, results, err)
	}
}

func TestHistoryTaskCursorHandlesTiedOrders(t *testing.T) {
	db := dbcore.GetDBInstance()
	const prefix = "history-tie-test-"
	t.Cleanup(func() { db.Exec("DELETE FROM tasks WHERE task_id LIKE ?", prefix+"%") })
	for i := 0; i < 25; i++ {
		if err := db.Exec(`INSERT INTO tasks(task_id,clients,command,history_order) VALUES(?, '[]', '', 1000000000000)`, fmt.Sprintf("%s%02d", prefix, i)).Error; err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for cursor := ""; ; {
		page, err := ListHistory(t.Context(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range page.Tasks {
			if strings.HasPrefix(task.TaskID, prefix) {
				if seen[task.TaskID] {
					t.Fatalf("duplicate task across tied pages: %s", task.TaskID)
				}
				seen[task.TaskID] = true
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 25 {
		t.Fatalf("tied pagination saw %d of 25 tasks", len(seen))
	}
}

func TestHistoryDetailAndByteChunks(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:task-history-test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	id := "history-chunk-test"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	command := strings.Repeat("x", HistoryChunkBytes-1) + "中\x00<script>"
	if err := db.Create(&models.Task{TaskId: id, Clients: models.StringArray{"deleted"}, Command: command}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if err := db.Create(&models.TaskResult{TaskId: id, Client: fmt.Sprintf("deleted-%02d", i), Result: command, CreatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	detail, err := GetHistoryDetail(t.Context(), id, "")
	if err != nil || len(detail.Results) != HistoryResultPageSize || detail.NextCursor == "" {
		t.Fatalf("detail: %+v, %v", detail, err)
	}
	second, err := GetHistoryDetail(t.Context(), id, detail.NextCursor)
	if err != nil || len(second.Results) != 5 || second.NextCursor != "" {
		t.Fatalf("second detail: %+v, %v", second, err)
	}
	for _, kind := range []string{"command", "output"} {
		cursor := ""
		if kind == "output" {
			cursor = detail.Results[0].Cursor
		}
		chunk, err := ReadHistoryChunk(t.Context(), id, kind, cursor, 0)
		if err != nil || !chunk.HasMore || len(chunk.Data) != HistoryChunkBytes-1 {
			t.Fatalf("first %s chunk: %+v, %v", kind, chunk, err)
		}
		tail, err := ReadHistoryChunk(t.Context(), id, kind, cursor, chunk.NextOffset)
		if err != nil || tail.HasMore || string(append(chunk.Data, tail.Data...)) != command {
			t.Fatalf("%s chunks did not preserve UTF-8 boundary/NUL: %v", kind, err)
		}
	}
	if _, err := GetHistoryDetail(t.Context(), "unknown", ""); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing task: %v", err)
	}
	if _, err := ReadHistoryChunk(t.Context(), "other-task", "output", detail.Results[0].Cursor, 0); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-task result must not be readable: %v", err)
	}
	if _, err := ReadHistoryChunk(t.Context(), id, "command", "", -1); !errors.Is(err, ErrInvalidHistoryParams) {
		t.Fatalf("negative offset: %v", err)
	}
}

func TestHistoryDuplicateClientsAndStaleCursor(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := "history-duplicates-test"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	if err := db.Create(&models.Task{TaskId: id, Clients: models.StringArray{"same"}, Command: "secret"}).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		if err := db.Create(&models.TaskResult{TaskId: id, Client: "same", Result: fmt.Sprintf("result-%d", i), CreatedAt: base.Add(time.Duration(i) * time.Second)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	first, err := GetHistoryDetail(t.Context(), id, "")
	if err != nil || len(first.Results) != 20 || first.NextCursor == "" {
		t.Fatalf("first duplicate page: %+v %v", first, err)
	}
	second, err := GetHistoryDetail(t.Context(), id, first.NextCursor)
	if err != nil || len(second.Results) != 5 {
		t.Fatalf("second duplicate page: %+v %v", second, err)
	}
	seen := map[string]bool{}
	for _, result := range append(first.Results, second.Results...) {
		if seen[result.Cursor] {
			t.Fatalf("duplicate cursor %q", result.Cursor)
		}
		seen[result.Cursor] = true
	}
	stale, err := decodeResultCursor(first.Results[0].Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM task_results WHERE rowid = ?", stale.RowID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO task_results(rowid,task_id,client,result,created_at) VALUES(?,?,?,?,?)", stale.RowID, id, stale.Client, "replacement", base.Add(48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHistoryChunk(t.Context(), id, "output", first.Results[0].Cursor, 0); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("reused row must not answer an old result cursor: %v", err)
	}
	if _, err := GetHistoryDetail(t.Context(), id, first.Results[0].Cursor); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("reused row must invalidate pagination cursor: %v", err)
	}
}

func TestHistoryOutputMayChangeBetweenChunks(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := "history-update-test"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	if err := CreateTask(id, []string{"node"}, "synthetic"); err != nil {
		t.Fatal(err)
	}
	initial := strings.Repeat("a", HistoryChunkBytes+8)
	if err := SaveTaskResult(id, "node", initial, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	detail, err := GetHistoryDetail(t.Context(), id, "")
	if err != nil || len(detail.Results) != 1 {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	cursor := detail.Results[0].Cursor
	first, err := ReadHistoryChunk(t.Context(), id, "output", cursor, 0)
	if err != nil || !first.HasMore {
		t.Fatalf("first chunk: %+v %v", first, err)
	}
	updated := strings.Repeat("b", HistoryChunkBytes+8)
	if err := SaveTaskResult(id, "node", updated, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := ReadHistoryChunk(t.Context(), id, "output", cursor, first.NextOffset)
	if err != nil || string(second.Data) != strings.Repeat("b", 8) || second.HasMore {
		t.Fatalf("updated result did not return current content: %+v %v", second, err)
	}
}

func TestHistoryMalformedIdentityFailsClosed(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := "history-malformed-test"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	if err := CreateTask(id, nil, "synthetic"); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{strings.Repeat("x", 1<<20), "client\x00suffix", strings.Repeat("界", 13)} {
		if err := db.Exec(`INSERT INTO task_results(task_id,client,result,created_at) VALUES(?,?,'secret','2026-11-05 00:00:00+00:00')`, id, client).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := GetHistoryDetail(t.Context(), id, ""); !errors.Is(err, ErrInvalidHistoryParams) {
			t.Fatalf("malformed client identity accepted: %v", err)
		}
		if err := db.Exec("DELETE FROM task_results WHERE task_id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestHistorySummaryCapsResultScans(t *testing.T) {
	db := dbcore.GetDBInstance()
	id := "history-large-test"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	if err := CreateTask(id, nil, "secret"); err != nil {
		t.Fatal(err)
	}
	var rows []models.TaskResult
	for i := 0; i < HistoryCountLimit+1; i++ {
		rows = append(rows, models.TaskResult{TaskId: id, Client: fmt.Sprint(i), CreatedAt: time.Now().UTC()})
	}
	if err := db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	page, err := ListHistory(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Tasks[0].TaskID != id || !page.Tasks[0].CountsTruncated || !page.Tasks[0].TimeIncomplete || page.Tasks[0].ResultCount != HistoryCountLimit {
		t.Fatalf("oversized summary: %+v", page.Tasks[0])
	}
}

func TestHistoryOversizedFieldsDoNotMaterialize(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:task-history-test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	const id = "history-oversized-test"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	if err := CreateTask(id, []string{"node"}, "safe"); err != nil {
		t.Fatal(err)
	}
	// Generate the values inside SQLite rather than allocating them in Go.
	const oversized = HistorySQLiteMaxBytes * 2
	if err := db.Exec(`UPDATE tasks SET clients = CAST(zeroblob(?) AS TEXT), command = CAST(zeroblob(?) AS TEXT) WHERE task_id = ?`, oversized, oversized, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`UPDATE task_results SET result = CAST(zeroblob(?) AS TEXT) WHERE task_id = ?`, oversized, id).Error; err != nil {
		t.Fatal(err)
	}
	page, err := ListHistory(t.Context(), "")
	if err != nil || len(page.Tasks) == 0 || page.Tasks[0].TaskID != id || page.Tasks[0].TargetCount != nil || page.Tasks[0].Status != "incomplete" {
		t.Fatalf("oversized targets should remain unknown: %+v %v", page, err)
	}
	detail, err := GetHistoryDetail(t.Context(), id, "")
	if err != nil || len(detail.Results) != 1 || !detail.Results[0].OutputAvailable || detail.Results[0].OutputBytes != oversized {
		t.Fatalf("oversized output metadata: %+v %v", detail, err)
	}
	for _, item := range []struct{ kind, cursor string }{{"command", ""}, {"output", detail.Results[0].Cursor}} {
		for _, offset := range []int64{0, oversized / 2, oversized - 7, oversized, HistoryOffsetLimit} {
			chunk, err := ReadHistoryChunk(t.Context(), id, item.kind, item.cursor, offset)
			want := min(int64(HistoryChunkBytes), max(int64(0), oversized-offset))
			if err != nil || !chunk.Available || int64(len(chunk.Data)) != want || chunk.TotalBytes != oversized ||
				chunk.NextOffset != offset+want || chunk.HasMore != (offset+want < oversized) || !bytes.Equal(chunk.Data, make([]byte, want)) {
				t.Fatalf("oversized %s offset %d: available=%t bytes=%d next=%d total=%d more=%t err=%v", item.kind, offset, chunk.Available, len(chunk.Data), chunk.NextOffset, chunk.TotalBytes, chunk.HasMore, err)
			}
		}
	}
	// The read guard must not truncate or rewrite a historical field.
	var lengths struct{ Command, Output int64 }
	if err := db.Raw(`SELECT octet_length(command) AS command, (SELECT octet_length(result) FROM task_results WHERE task_id = ?) AS output FROM tasks WHERE task_id = ?`, id, id).Scan(&lengths).Error; err != nil || lengths.Command != oversized || lengths.Output != oversized {
		t.Fatalf("history changed stored data: %+v %v", lengths, err)
	}
}

func TestHistoryLargeFieldBoundaries(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:task-history-test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	const id = "history-field-limit"
	t.Cleanup(func() {
		db.Exec("DELETE FROM task_results WHERE task_id = ?", id)
		db.Exec("DELETE FROM tasks WHERE task_id = ?", id)
	})
	if err := CreateTask(id, []string{"node"}, "safe"); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{8 << 20, (8 << 20) + 1, HistorySQLiteMaxBytes, HistorySQLiteMaxBytes + 1} {
		if err := db.Exec(`UPDATE tasks SET command = CAST(zeroblob(?) AS TEXT) WHERE task_id = ?`, size, id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`UPDATE task_results SET result = CAST(zeroblob(?) AS TEXT) WHERE task_id = ?`, size, id).Error; err != nil {
			t.Fatal(err)
		}
		detail, err := GetHistoryDetail(t.Context(), id, "")
		if err != nil || len(detail.Results) != 1 || !detail.Results[0].OutputAvailable {
			t.Fatalf("metadata at size %d: %+v %v", size, detail, err)
		}
		for _, item := range []struct{ kind, cursor string }{{"command", ""}, {"output", detail.Results[0].Cursor}} {
			for _, offset := range []int64{0, int64(size - 1), int64(size + 1)} {
				chunk, err := ReadHistoryChunk(t.Context(), id, item.kind, item.cursor, offset)
				want := min(int64(HistoryChunkBytes), max(int64(0), int64(size)-offset))
				if err != nil || !chunk.Available || chunk.TotalBytes != int64(size) || int64(len(chunk.Data)) != want ||
					chunk.NextOffset != offset+want || chunk.HasMore != (offset+want < int64(size)) {
					t.Fatalf("%s size=%d offset=%d available=%t bytes=%d next=%d total=%d more=%t err=%v", item.kind, size, offset, chunk.Available, len(chunk.Data), chunk.NextOffset, chunk.TotalBytes, chunk.HasMore, err)
				}
			}
		}
	}
}

func TestHistoryConnectionLimitRestored(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:task-history-test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	var previous int
	if err := db.Connection(func(tx *gorm.DB) error {
		return tx.Statement.ConnPool.(*sql.Conn).Raw(func(driverConn any) error {
			previous = driverConn.(*sqlite3.SQLiteConn).GetLimit(sqlite3.SQLITE_LIMIT_LENGTH)
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if stats := sqlDB.Stats(); stats.MaxOpenConnections != 1 {
		t.Fatalf("expected the configured single SQLite connection, got %+v", stats)
	}
	if _, err := ReadHistoryChunk(t.Context(), "missing", "command", "", 0); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing task: %v", err)
	}
	if err := db.Connection(func(tx *gorm.DB) error {
		return tx.Statement.ConnPool.(*sql.Conn).Raw(func(driverConn any) error {
			got := driverConn.(*sqlite3.SQLiteConn).GetLimit(sqlite3.SQLITE_LIMIT_LENGTH)
			if got != previous {
				t.Errorf("history leaked SQLite length limit: got %d, want %d", got, previous)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	err = withHistoryLimit(historyDB(ctx), func(tx *gorm.DB) error {
		var value int64
		return tx.Raw(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x + 1 FROM n WHERE x < 100000000) SELECT sum(x) FROM n`).Scan(&value).Error
	})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("running query did not honor deadline: %v", err)
	}
	if _, err := ListHistory(t.Context(), ""); err != nil {
		t.Fatalf("history connection unusable after cancellation: %v", err)
	}
	if err := db.Connection(func(tx *gorm.DB) error {
		return tx.Statement.ConnPool.(*sql.Conn).Raw(func(driverConn any) error {
			got := driverConn.(*sqlite3.SQLiteConn).GetLimit(sqlite3.SQLITE_LIMIT_LENGTH)
			if got != previous {
				t.Errorf("canceled history leaked SQLite length limit: got %d, want %d", got, previous)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
}
