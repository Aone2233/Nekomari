package models

import "time"

// MaintenanceWindow is a scheduled period in which a node's alerts are suppressed.
//
// Why this is scoped to *notification* and not to collection: the metrics keep recording through
// a window, because the point of a maintenance window is that the node is doing something real.
// A report that showed a gap in the charts afterwards would be lying about what happened, and a
// suppressed alert that also suppressed the data would hide the maintenance's effect.
//
// Clients is deliberately "empty means every node" rather than "empty means none". A fleet-wide
// maintenance is the common case, and a window that applies to nothing should be deleted rather
// than stored — the alternative is a table where the meaning of the empty list has to be
// remembered. See internal/maintenance for the boundary rules.
type MaintenanceWindow struct {
	ID        uint        `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
	Name      string      `json:"name" gorm:"type:varchar(100);not null"`
	Start     time.Time   `json:"start" gorm:"not null;index"`
	End       time.Time   `json:"end" gorm:"not null;index"`
	Clients   StringArray `json:"clients" gorm:"type:longtext"` // empty = every node
	Reason    string      `json:"reason,omitempty" gorm:"type:varchar(255)"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}
