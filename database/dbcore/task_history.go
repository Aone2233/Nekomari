package dbcore

import (
	"fmt"
	"math"

	"gorm.io/gorm"
)

// SQLite may renumber implicit rowids during VACUUM. Persist their order on
// the initial upgrade and reconcile rows written during an older-image rollback.
func backfillTaskHistoryOrder(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Raw(`SELECT count(*) FROM tasks WHERE history_order IS NULL`).Scan(&count).Error; err != nil || count == 0 {
			return err
		}
		var maximum int64
		if err := tx.Raw(`SELECT coalesce(max(history_order), 0) FROM tasks WHERE history_order IS NOT NULL`).Scan(&maximum).Error; err != nil {
			return err
		}
		if maximum < 0 {
			maximum = 0
		}
		if count > math.MaxInt64-maximum {
			return fmt.Errorf("task history order exhausted")
		}
		// Rank only unnumbered rows: implicit rowids may be negative, sparse,
		// or close to MaxInt64 after imports or database maintenance.
		return tx.Exec(`WITH unnumbered AS MATERIALIZED (
			SELECT rowid AS rid, row_number() OVER (ORDER BY rowid) AS seq
			FROM tasks WHERE history_order IS NULL
		) UPDATE tasks SET history_order = ? + seq FROM unnumbered WHERE tasks.rowid = unnumbered.rid`, maximum).Error
	})
}

// Random identities survive VACUUM and prevent a cursor from retargeting a
// different row after implicit SQLite rowids are renumbered or reused. Old
// binaries ignore the nullable column; the trigger covers their inserts and
// new startup reconciles any rows written while the trigger was absent.
func backfillTaskResultHistoryIdentity(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE task_results SET history_identity = lower(hex(randomblob(16))) WHERE history_identity IS NULL`).Error; err != nil {
			return err
		}
		return tx.Exec(`CREATE TRIGGER IF NOT EXISTS task_results_assign_history_identity
			AFTER INSERT ON task_results WHEN NEW.history_identity IS NULL
			BEGIN UPDATE task_results SET history_identity = lower(hex(randomblob(16))) WHERE rowid = NEW.rowid; END`).Error
	})
}
