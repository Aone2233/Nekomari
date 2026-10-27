package clients

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/clientguard"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// RotateClientTokenAndReset exposes the replacement only after commit and local
// invalidation. reset must not block on sockets, external providers or streams.
func RotateClientTokenAndReset(ctx context.Context, clientUUID string, reset func()) (string, error) {
	if clientUUID == "" {
		return "", fmt.Errorf("client UUID is required")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate client token: %w", err)
	}
	token := "na_" + base64.RawURLEncoding.EncodeToString(random[:])
	err := clientguard.With(ctx, clientUUID, func() error {
		// Never include a secret in a failed/slow SQL trace.
		db := dbcore.GetDBInstance().WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
		result := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Updates(map[string]any{
			"token": token, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if reset != nil {
			reset()
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}
