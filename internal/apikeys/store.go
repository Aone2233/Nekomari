// Package apikeys implements scoped, expiring, non-recoverable credentials.
package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const MaxActiveKeys = 100

var (
	ErrInvalid          = errors.New("API key is invalid, expired or revoked")
	ErrNameInvalid      = errors.New("name must be 1 to 80 bytes without control characters")
	ErrScopesRequired   = errors.New("choose at least one supported scope (no wildcards)")
	ErrScopeUnsupported = errors.New("unsupported API key scope")
	ErrExpiryInvalid    = errors.New("expires_days must be between 1 and 365")
	ErrActiveLimit      = errors.New("at most 100 active API keys are supported; revoke a key first")
)

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "nk_" + base64.RawURLEncoding.EncodeToString(random[:]), nil
}

func ValidateScopes(scopes []string) ([]string, error) {
	known := make(map[string]bool)
	for _, scope := range rpc.APIKeyScopes() {
		known[scope] = true
	}
	if len(scopes) == 0 || len(scopes) > len(known) {
		return nil, ErrScopesRequired
	}
	seen := make(map[string]bool)
	for _, scope := range scopes {
		if !known[scope] {
			return nil, fmt.Errorf("%w: %s", ErrScopeUnsupported, scope)
		}
		seen[scope] = true
	}
	out := make([]string, 0, len(seen))
	for scope := range seen {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out, nil
}

func Create(ctx context.Context, db *gorm.DB, name string, scopes []string, days int, issuer string) (models.APIKey, string, error) {
	if !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return models.APIKey{}, "", ErrNameInvalid
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return models.APIKey{}, "", ErrNameInvalid
	}
	grants, err := ValidateScopes(scopes)
	if err != nil {
		return models.APIKey{}, "", err
	}
	if days < 1 || days > 365 {
		return models.APIKey{}, "", ErrExpiryInvalid
	}
	token, err := newToken()
	if err != nil {
		return models.APIKey{}, "", err
	}
	now := time.Now().UTC()
	key := models.APIKey{ID: uuid.NewString(), Name: name, Scopes: grants, TokenDigest: digest(token),
		IssuedBy: issuer, CreatedAt: now, ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour)}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active int64
		if err := tx.Model(&models.APIKey{}).Where("revoked_at IS NULL AND expires_at > ?", now).Count(&active).Error; err != nil {
			return err
		}
		if active >= MaxActiveKeys {
			return ErrActiveLimit
		}
		return tx.Create(&key).Error
	})
	if err != nil {
		return models.APIKey{}, "", err
	}
	return key, token, nil
}

func Authenticate(ctx context.Context, db *gorm.DB, authorization string) (*rpc.Principal, error) {
	if db == nil {
		return nil, ErrInvalid
	}
	if !strings.HasPrefix(authorization, "Bearer ") {
		return nil, ErrInvalid
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	if len(token) < 12 || len(token) > 512 || strings.TrimSpace(token) != token {
		return nil, ErrInvalid
	}
	var key models.APIKey
	if err := db.WithContext(ctx).Where("token_digest = ? AND revoked_at IS NULL AND expires_at > ?",
		digest(token), time.Now().UTC()).Take(&key).Error; err != nil {
		return nil, ErrInvalid
	}
	// Compare instants as well: SQLite's textual timestamp ordering is not a
	// timezone-aware comparison when a row came from an external DB tool.
	if !key.ExpiresAt.After(time.Now().UTC()) {
		return nil, ErrInvalid
	}
	grants, err := ValidateScopes(key.Scopes)
	if err != nil {
		return nil, ErrInvalid
	}
	return rpc.NewScopedAPIKeyPrincipal(key.ID, grants), nil
}

// No credential cache: rotation, revocation and expiry apply on the next request
// (or WebSocket message), including when another process updates the same DB.
func List(ctx context.Context, db *gorm.DB) ([]models.APIKey, error) {
	keys := make([]models.APIKey, 0)
	// Active credentials first; retain a bounded view of the revocation history.
	err := db.WithContext(ctx).Order("CASE WHEN revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP THEN 0 ELSE 1 END").Order("created_at DESC").Limit(200).Find(&keys).Error
	return keys, err
}

func Rotate(ctx context.Context, db *gorm.DB, id string) (models.APIKey, string, error) {
	var key models.APIKey
	token, err := newToken()
	if err != nil {
		return key, "", err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND revoked_at IS NULL AND expires_at > ?", id, time.Now().UTC()).Take(&key).Error; err != nil {
			return ErrInvalid
		}
		if !key.ExpiresAt.After(time.Now().UTC()) {
			return ErrInvalid
		}
		result := tx.Model(&models.APIKey{}).Where("id = ? AND token_digest = ? AND revoked_at IS NULL AND expires_at > ?",
			id, key.TokenDigest, time.Now().UTC()).Update("token_digest", digest(token))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalid
		}
		return nil
	})
	if err != nil {
		return models.APIKey{}, "", err
	}
	key.TokenDigest = digest(token)
	return key, token, nil
}

func Revoke(ctx context.Context, db *gorm.DB, id string) error {
	result := db.WithContext(ctx).Model(&models.APIKey{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", time.Now().UTC())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInvalid
	}
	return nil
}
