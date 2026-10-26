package apikeys

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MigrateLegacy preserves monitoring scripts for 30 days, not global admin
// power. Creation and removal of the plaintext config row commit together.
func MigrateLegacy(db *gorm.DB) error {
	if err := db.AutoMigrate(&models.APIKey{}, &config.ConfigItem{}); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		const markerKey = "api_key_scopes_v1_migrated"
		var marker config.ConfigItem
		if err := tx.Where("key = ?", markerKey).Take(&marker).Error; err == nil {
			// Never re-authorize a retired credential reintroduced by an old
			// settings import, even if the migrated key has since been rotated.
			return tx.Delete(&config.ConfigItem{}, "key = ?", config.ApiKeyKey).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var row config.ConfigItem
		if err := tx.Where("key = ?", config.ApiKeyKey).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tx.Create(&config.ConfigItem{Key: markerKey, Value: "true"}).Error
			}
			return err
		}
		var token string
		if json.Unmarshal([]byte(row.Value), &token) == nil && len(token) >= 12 && len(token) <= 512 {
			now := time.Now().UTC()
			key := models.APIKey{ID: uuid.NewString(), Name: "Migrated legacy key (read-only, replace within 30 days)",
				TokenDigest: digest(token), Scopes: []string{rpc.ScopeNodesRead}, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "token_digest"}}, DoNothing: true}).Create(&key).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(&config.ConfigItem{}, "key = ?", config.ApiKeyKey).Error; err != nil {
			return err
		}
		return tx.Create(&config.ConfigItem{Key: markerKey, Value: "true"}).Error
	})
}
