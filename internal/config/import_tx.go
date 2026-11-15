package config

import (
	"encoding/json"
	"fmt"

	"github.com/Aone2233/nekomari/internal/dbcache"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// SetManyTx writes settings using the caller's transaction and returns the
// committed event to publish only after that transaction succeeds.
func SetManyTx(tx *gorm.DB, values map[string]any) (ConfigEvent, error) {
	if tx == nil {
		return ConfigEvent{}, fmt.Errorf("configuration transaction is unavailable")
	}
	event := ConfigEvent{Old: map[string]any{}, New: map[string]any{}}
	if len(values) == 0 {
		return event, nil
	}
	keys := make([]string, 0, len(values))
	items := make([]ConfigItem, 0, len(values))
	for key, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return ConfigEvent{}, fmt.Errorf("marshal key %s: %w", key, err)
		}
		var canonical any
		if err := json.Unmarshal(raw, &canonical); err != nil {
			return ConfigEvent{}, err
		}
		keys = append(keys, key)
		items = append(items, ConfigItem{Key: key, Value: string(raw)})
		event.New[key] = canonical
	}
	var old []ConfigItem
	// Settings may contain credentials; neither successful slow SQL nor
	// failed SQL may interpolate their values into application logs.
	quiet := tx.Session(&gorm.Session{Logger: logger.Discard})
	if err := quiet.Where("key IN ?", keys).Find(&old).Error; err != nil {
		return ConfigEvent{}, err
	}
	for _, item := range old {
		var value any
		if err := json.Unmarshal([]byte(item.Value), &value); err == nil {
			event.Old[item.Key] = value
		}
	}
	if err := quiet.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&items).Error; err != nil {
		return ConfigEvent{}, err
	}
	return event, nil
}

// PublishCommitted notifies subscribers and invalidates caches after commit.
// It must never be called for a rolled-back transaction.
func PublishCommitted(event ConfigEvent) {
	if len(event.New) == 0 {
		return
	}
	dbcache.InvalidateAll()
	publishEvent(event.Old, event.New)
}
