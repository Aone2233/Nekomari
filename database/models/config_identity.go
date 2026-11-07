package models

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// A configuration identity survives changes to a panel-local numeric ID.
// It is deliberately distinct from the pinned probe's runtime generation.
func newConfigUID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("create configuration identity: %w", err)
	}
	return id.String(), nil
}

func (task *PingTask) BeforeCreate(_ *gorm.DB) error {
	if task.ConfigUID == "" {
		id, err := newConfigUID()
		if err != nil {
			return err
		}
		task.ConfigUID = id
	}
	if !validConfigUID(task.ConfigUID) {
		return fmt.Errorf("invalid ping task configuration identity")
	}
	return nil
}

func (window *MaintenanceWindow) BeforeCreate(_ *gorm.DB) error {
	if window.ConfigUID == "" {
		id, err := newConfigUID()
		if err != nil {
			return err
		}
		window.ConfigUID = id
	}
	if !validConfigUID(window.ConfigUID) {
		return fmt.Errorf("invalid maintenance window configuration identity")
	}
	return nil
}

// EnsureConfigIdentities backfills older databases transactionally and rejects
// ambiguous existing identities instead of silently mapping two records.
func EnsureConfigIdentities(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("configuration database is unavailable")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var tasks []PingTask
		if err := tx.Select("id", "config_uid").Find(&tasks).Error; err != nil {
			return err
		}
		seen := make(map[string]struct{}, len(tasks))
		if tx.Migrator().HasTable(&PingTaskDeletion{}) {
			var deletions []PingTaskDeletion
			if err := tx.Select("task_id", "config_uid").Find(&deletions).Error; err != nil {
				return err
			}
			for _, deletion := range deletions {
				if deletion.TaskID == 0 {
					return fmt.Errorf("invalid reserved ping task ID")
				}
				if deletion.ConfigUID == "" {
					continue // Pre-identity reservations cannot name a past UID.
				}
				if !validConfigUID(deletion.ConfigUID) {
					return fmt.Errorf("invalid deleted ping task configuration identity")
				}
				if _, exists := seen[deletion.ConfigUID]; exists {
					return fmt.Errorf("duplicate deleted ping task configuration identity")
				}
				seen[deletion.ConfigUID] = struct{}{}
			}
		}
		for _, task := range tasks {
			uid := task.ConfigUID
			if uid == "" {
				var err error
				uid, err = newConfigUID()
				if err != nil {
					return err
				}
				if err := tx.Model(&PingTask{}).Where("id = ? AND config_uid = ?", task.Id, "").Update("config_uid", uid).Error; err != nil {
					return err
				}
			}
			if !validConfigUID(uid) {
				return fmt.Errorf("invalid ping task configuration identity for task %d", task.Id)
			}
			if _, exists := seen[uid]; exists {
				return fmt.Errorf("duplicate ping task configuration identity")
			}
			seen[uid] = struct{}{}
		}
		var windows []MaintenanceWindow
		if err := tx.Select("id", "config_uid").Find(&windows).Error; err != nil {
			return err
		}
		seen = make(map[string]struct{}, len(windows))
		for _, window := range windows {
			uid := window.ConfigUID
			if uid == "" {
				var err error
				uid, err = newConfigUID()
				if err != nil {
					return err
				}
				if err := tx.Model(&MaintenanceWindow{}).Where("id = ? AND config_uid = ?", window.ID, "").Update("config_uid", uid).Error; err != nil {
					return err
				}
			}
			if !validConfigUID(uid) {
				return fmt.Errorf("invalid maintenance window configuration identity for window %d", window.ID)
			}
			if _, exists := seen[uid]; exists {
				return fmt.Errorf("duplicate maintenance window configuration identity")
			}
			seen[uid] = struct{}{}
		}
		for _, statement := range []string{
			"CREATE UNIQUE INDEX IF NOT EXISTS idx_ping_tasks_config_uid ON ping_tasks(config_uid) WHERE config_uid <> ''",
			"CREATE UNIQUE INDEX IF NOT EXISTS idx_maintenance_windows_config_uid ON maintenance_windows(config_uid) WHERE config_uid <> ''",
		} {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func validConfigUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}
