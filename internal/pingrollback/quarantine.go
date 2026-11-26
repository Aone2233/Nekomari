// Package pingrollback contains an experimental offline downgrade boundary.
// It is not a production rollback tool: the older panel can delete metric
// history before a blocked main-database delete, so the entire old admin API
// must also be isolated. See docs/PING-FAMILY-ROLLBACK-DRAFT.md.
package pingrollback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/pingguard"
	"gorm.io/gorm"
)

const (
	maxTasks     = 10000
	maxClients   = 10000
	parkedClient = "__nekomari_family_rollback_parked__"
)

var ErrConflict = errors.New("pinned ping quarantine conflicts with the current database")

// These tables are deliberately not part of the panel's automatic migrations.
// They are created only during an offline operation, and removed on restore.
type snapshot struct {
	ID           uint `gorm:"primaryKey"`
	Count        int
	Manifest     string
	RosterDigest string
}

func (snapshot) TableName() string { return "nekomari_ping_rollback_snapshot" }

type row struct {
	TaskID uint `gorm:"primaryKey"`
	Data   []byte
	Digest string
}

func (row) TableName() string { return "nekomari_ping_rollback_rows" }

type taskData struct {
	ID                  uint               `json:"id"`
	ConfigUID           string             `json:"config_uid"`
	Weight              int                `json:"weight"`
	Name                string             `json:"name"`
	Clients             models.StringArray `json:"clients"`
	ClientsRaw          []byte             `json:"clients_raw"`
	ClientsType         string             `json:"clients_type"`
	DefaultOn           bool               `json:"default_on"`
	Type                string             `json:"type"`
	Target              string             `json:"target"`
	Reference           string             `json:"reference"`
	Interval            int                `json:"interval"`
	Family              string             `json:"family"`
	Generation          string             `json:"generation"`
	ProbeAttempts       int                `json:"probe_attempts,omitempty"`
	ProbeSpacingMS      int                `json:"probe_spacing_ms,omitempty"`
	ProbeTimeoutMS      int                `json:"probe_timeout_ms,omitempty"`
	HTTPMode            string             `json:"http_mode,omitempty"`
	HTTPExpectedStatus  string             `json:"http_expected_status,omitempty"`
	IPVersionPreference string             `json:"ip_version_preference,omitempty"`
}

type rawClients struct {
	Raw         []byte
	StorageType string
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func manifest(rows []row) string {
	sort.Slice(rows, func(i, j int) bool { return rows[i].TaskID < rows[j].TaskID })
	h := sha256.New()
	for _, r := range rows {
		_, _ = fmt.Fprintf(h, "%d:%s\n", r.TaskID, r.Digest)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func encode(task models.PingTask, raw rawClients) (row, error) {
	if task.Id == 0 || (task.TargetFamily != "ipv4" && task.TargetFamily != "ipv6") || !pingguard.ValidGeneration(task.PinnedGeneration) {
		return row{}, fmt.Errorf("task %d has invalid pinned identity", task.Id)
	}
	var clients models.StringArray
	switch raw.StorageType {
	case "null":
		if raw.Raw != nil {
			return row{}, ErrConflict
		}
		clients = models.StringArray{}
	case "text", "blob":
		if err := clients.Scan(raw.Raw); err != nil {
			return row{}, ErrConflict
		}
	default:
		return row{}, ErrConflict
	}
	if !reflect.DeepEqual(clients, task.Clients) {
		return row{}, ErrConflict
	}
	data, err := json.Marshal(taskData{task.Id, task.ConfigUID, task.Weight, task.Name, task.Clients, raw.Raw, raw.StorageType,
		task.DefaultOn, task.Type, task.Target, task.Reference, task.Interval, task.TargetFamily, task.PinnedGeneration,
		task.ProbeAttempts, task.ProbeSpacingMS, task.ProbeTimeoutMS, task.HTTPMode, task.HTTPExpectedStatus, task.IPVersionPreference})
	if err != nil {
		return row{}, err
	}
	return row{TaskID: task.Id, Data: data, Digest: digest(data)}, nil
}

func decode(r row) (models.PingTask, rawClients, error) {
	var data taskData
	if len(r.Data) > 1<<20 || r.Digest != digest(r.Data) || json.Unmarshal(r.Data, &data) != nil || data.ID != r.TaskID {
		return models.PingTask{}, rawClients{}, ErrConflict
	}
	task := models.PingTask{Id: data.ID, ConfigUID: data.ConfigUID, Weight: data.Weight, Name: data.Name, Clients: data.Clients,
		DefaultOn: data.DefaultOn, Type: data.Type, Target: data.Target, Reference: data.Reference,
		Interval: data.Interval, TargetFamily: data.Family, PinnedGeneration: data.Generation,
		ProbeAttempts: data.ProbeAttempts, ProbeSpacingMS: data.ProbeSpacingMS, ProbeTimeoutMS: data.ProbeTimeoutMS,
		HTTPMode: data.HTTPMode, HTTPExpectedStatus: data.HTTPExpectedStatus, IPVersionPreference: data.IPVersionPreference}
	raw := rawClients{Raw: data.ClientsRaw, StorageType: data.ClientsType}
	if _, err := encode(task, raw); err != nil {
		return models.PingTask{}, rawClients{}, ErrConflict
	}
	return task, raw, nil
}

// Protect both parked rows and their manifest against normal SQL mutations by
// an older panel. SQLite triggers do not protect the separate metrics DB.
var triggerSQL = []string{
	`CREATE TRIGGER nekomari_ping_rollback_task_insert BEFORE INSERT ON ping_tasks WHEN EXISTS (SELECT 1 FROM nekomari_ping_rollback_rows WHERE task_id = NEW.id) BEGIN SELECT RAISE(ABORT, 'family-pinned task is quarantined'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_task_update BEFORE UPDATE ON ping_tasks WHEN EXISTS (SELECT 1 FROM nekomari_ping_rollback_rows WHERE task_id = OLD.id) BEGIN SELECT RAISE(ABORT, 'family-pinned task is quarantined'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_task_delete BEFORE DELETE ON ping_tasks WHEN EXISTS (SELECT 1 FROM nekomari_ping_rollback_rows WHERE task_id = OLD.id) BEGIN SELECT RAISE(ABORT, 'family-pinned task is quarantined'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_rows_insert BEFORE INSERT ON nekomari_ping_rollback_rows BEGIN SELECT RAISE(ABORT, 'family quarantine is sealed'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_rows_update BEFORE UPDATE ON nekomari_ping_rollback_rows BEGIN SELECT RAISE(ABORT, 'family quarantine is sealed'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_rows_delete BEFORE DELETE ON nekomari_ping_rollback_rows BEGIN SELECT RAISE(ABORT, 'family quarantine is sealed'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_snapshot_insert BEFORE INSERT ON nekomari_ping_rollback_snapshot BEGIN SELECT RAISE(ABORT, 'family quarantine is sealed'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_snapshot_update BEFORE UPDATE ON nekomari_ping_rollback_snapshot BEGIN SELECT RAISE(ABORT, 'family quarantine is sealed'); END`,
	`CREATE TRIGGER nekomari_ping_rollback_snapshot_delete BEFORE DELETE ON nekomari_ping_rollback_snapshot BEGIN SELECT RAISE(ABORT, 'family quarantine is sealed'); END`,
}

func triggerName(sql string) string { return strings.Fields(sql)[2] }

func checkSchema(tx *gorm.DB) error {
	var columns []struct{ Name string }
	if err := tx.Raw(`PRAGMA table_info('ping_tasks')`).Scan(&columns).Error; err != nil {
		return err
	}
	wanted := map[string]bool{"id": true, "config_uid": true, "weight": true, "name": true, "clients": true, "all_clients": true,
		"type": true, "target": true, "reference": true, "interval": true, "target_family": true, "pinned_generation": true,
		"probe_attempts": true, "probe_spacing_ms": true, "probe_timeout_ms": true,
		"http_mode": true, "http_expected_status": true, "ip_version_preference": true}
	if len(columns) != len(wanted) {
		return ErrConflict
	}
	for _, col := range columns {
		if !wanted[col.Name] {
			return ErrConflict
		}
		delete(wanted, col.Name)
	}
	if len(wanted) != 0 || !tx.Migrator().HasTable(&models.Client{}) {
		return ErrConflict
	}
	return nil
}

func clientRosterDigest(tx *gorm.DB) (string, error) {
	// A changed membership needs explicit reconciliation, particularly for a
	// default-on task. Never include tokens in rollback state or logs.
	var clients []struct{ UUID, CreatedAt string }
	if err := tx.Raw(`SELECT uuid, CAST(created_at AS TEXT) AS created_at FROM clients ORDER BY uuid LIMIT ?`, maxClients+1).Scan(&clients).Error; err != nil {
		return "", err
	}
	if len(clients) > maxClients {
		return "", ErrConflict
	}
	canonical := make([][2]string, 0, len(clients))
	for _, c := range clients {
		created, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
		if err != nil {
			created, err = time.Parse("2006-01-02 15:04:05.999999999-07:00", c.CreatedAt)
		}
		if c.UUID == "" || err != nil || created.IsZero() {
			return "", ErrConflict
		}
		canonical = append(canonical, [2]string{c.UUID, created.UTC().Format(time.RFC3339Nano)})
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func readClients(tx *gorm.DB, id uint) (rawClients, error) {
	var raw rawClients
	result := tx.Raw(`SELECT CAST(clients AS BLOB) AS raw, typeof(clients) AS storage_type FROM ping_tasks WHERE id = ?`, id).Scan(&raw)
	if result.Error != nil {
		return raw, result.Error
	}
	if result.RowsAffected != 1 {
		return raw, ErrConflict
	}
	return raw, nil
}

// Quarantine is an experimental SQLite-only operation. The panel and every
// database/metric writer MUST remain stopped throughout this operation. The
// caller must also prevent old-panel admin/API mutation for the whole rollback.
// Keeping the parent rows avoids cascades and prevents their IDs being reused.
func Quarantine(db *gorm.DB) (int, error) {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return 0, errors.New("offline quarantine currently requires SQLite")
	}
	var count int
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := checkSchema(tx); err != nil {
			return err
		}
		for _, name := range []string{"nekomari_ping_rollback_snapshot", "nekomari_ping_rollback_rows"} {
			if tx.Migrator().HasTable(name) {
				return ErrConflict
			}
		}
		for _, sql := range triggerSQL {
			var n int64
			if err := tx.Raw(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, triggerName(sql)).Scan(&n).Error; err != nil {
				return err
			}
			if n != 0 {
				return ErrConflict
			}
		}
		var tasks []models.PingTask
		if err := tx.Where("coalesce(target_family, '') <> '' OR coalesce(pinned_generation, '') <> ''").Order("id").Limit(maxTasks + 1).Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) > maxTasks {
			return ErrConflict
		}
		if len(tasks) == 0 {
			return nil
		}
		roster, err := clientRosterDigest(tx)
		if err != nil {
			return err
		}
		rows := make([]row, 0, len(tasks))
		for _, task := range tasks {
			raw, err := readClients(tx, task.Id)
			if err != nil {
				return err
			}
			r, err := encode(task, raw)
			if err != nil {
				return err
			}
			rows = append(rows, r)
		}
		if err := tx.Exec(`CREATE TABLE nekomari_ping_rollback_snapshot (id INTEGER PRIMARY KEY, count INTEGER NOT NULL, manifest TEXT NOT NULL, roster_digest TEXT NOT NULL)`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE nekomari_ping_rollback_rows (task_id INTEGER PRIMARY KEY, data BLOB NOT NULL, digest TEXT NOT NULL)`).Error; err != nil {
			return err
		}
		if err := tx.Create(&snapshot{ID: 1, Count: len(rows), Manifest: manifest(rows), RosterDigest: roster}).Error; err != nil {
			return err
		}
		for _, r := range rows {
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
			// Nonempty clients prevent the old all-clients startup migration
			// from expanding a parked task. A zero interval disables scheduling.
			result := tx.Model(&models.PingTask{}).Where("id = ?", r.TaskID).Updates(map[string]any{"clients": models.StringArray{parkedClient}, "all_clients": false, "interval": 0})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrConflict
			}
		}
		for _, sql := range triggerSQL {
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
		}
		count = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Restore validates the whole parked set before changing anything, then
// restores assignments and creates fresh generations in a single transaction.
// It requires the same offline and admin/API isolation conditions as Quarantine.
func Restore(db *gorm.DB) (int, error) {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return 0, errors.New("offline restore currently requires SQLite")
	}
	var count int
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := checkSchema(tx); err != nil {
			return err
		}
		if !tx.Migrator().HasTable(&snapshot{}) || !tx.Migrator().HasTable(&row{}) {
			return ErrConflict
		}
		for _, sql := range triggerSQL {
			var found string
			if err := tx.Raw(`SELECT coalesce((SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?), '')`, triggerName(sql)).Scan(&found).Error; err != nil {
				return err
			}
			if found != sql {
				return ErrConflict
			}
		}
		var snapshots []snapshot
		if err := tx.Limit(2).Find(&snapshots).Error; err != nil {
			return err
		}
		if len(snapshots) != 1 || snapshots[0].ID != 1 {
			return ErrConflict
		}
		s := snapshots[0]
		var rows []row
		if err := tx.Order("task_id").Limit(maxTasks + 1).Find(&rows).Error; err != nil {
			return err
		}
		if s.Count <= 0 || s.Count > maxTasks || len(rows) != s.Count || manifest(rows) != s.Manifest {
			return ErrConflict
		}
		roster, err := clientRosterDigest(tx)
		if err != nil {
			return err
		}
		if roster != s.RosterDigest {
			return ErrConflict
		}
		type saved struct {
			task       models.PingTask
			raw        rawClients
			generation string
		}
		savedTasks := make([]saved, 0, len(rows))
		for _, r := range rows {
			task, raw, err := decode(r)
			if err != nil {
				return err
			}
			var current models.PingTask
			if err := tx.Take(&current, task.Id).Error; err != nil {
				return ErrConflict
			}
			expected := task
			expected.Clients, expected.DefaultOn, expected.Interval = models.StringArray{parkedClient}, false, 0
			if !reflect.DeepEqual(current, expected) {
				return ErrConflict
			}
			generation, err := pingguard.NewGeneration()
			if err != nil {
				return err
			}
			savedTasks = append(savedTasks, saved{task: task, raw: raw, generation: generation})
		}
		for _, sql := range triggerSQL {
			if err := tx.Exec("DROP TRIGGER " + triggerName(sql)).Error; err != nil {
				return err
			}
		}
		for _, item := range savedTasks {
			var raw any
			switch item.raw.StorageType {
			case "null":
				raw = nil
			case "text":
				raw = string(item.raw.Raw)
			case "blob":
				raw = item.raw.Raw
			default:
				return ErrConflict
			}
			result := tx.Exec(`UPDATE ping_tasks SET clients = ?, all_clients = ?, interval = ?, pinned_generation = ? WHERE id = ?`, raw, item.task.DefaultOn, item.task.Interval, item.generation, item.task.Id)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrConflict
			}
		}
		if err := tx.Exec(`DROP TABLE nekomari_ping_rollback_rows`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DROP TABLE nekomari_ping_rollback_snapshot`).Error; err != nil {
			return err
		}
		count = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
