package apikeys

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.APIKey{}, &config.ConfigItem{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestKeyLifecycleNeverStoresOrListsPlaintext(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	key, token, err := Create(ctx, db, "deploy monitor", []string{rpc.ScopeNodesRead, rpc.ScopeNodesRead}, 2, "issuer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "nk_") || len(token) != 46 || key.TokenDigest == token || len(key.Scopes) != 1 {
		t.Fatal("invalid generated credential metadata")
	}
	principal, err := Authenticate(ctx, db, "Bearer "+token)
	if err != nil || principal.APIKeyID != key.ID || !principal.HasScope(rpc.ScopeNodesRead) {
		t.Fatalf("authenticate: %v", err)
	}
	var stored models.APIKey
	if err := db.First(&stored, "id = ?", key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TokenDigest != digest(token) {
		t.Fatal("database does not store the token digest")
	}
	keys, err := List(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(keys)
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), stored.TokenDigest) || strings.Contains(string(raw), "token_digest") {
		t.Fatal("listing exposes a credential or digest")
	}
	rotated, replacement, err := Rotate(ctx, db, key.ID)
	if err != nil || replacement == token || !rotated.ExpiresAt.Equal(key.ExpiresAt) || strings.Join(rotated.Scopes, ",") != strings.Join(key.Scopes, ",") {
		t.Fatalf("rotation changed grants/expiry or failed: %v", err)
	}
	if _, err := Authenticate(ctx, db, "Bearer "+token); err == nil {
		t.Fatal("old credential survived rotation")
	}
	if _, err := Authenticate(ctx, db, "Bearer "+replacement); err != nil {
		t.Fatal("replacement does not authenticate")
	}
	if err := Revoke(ctx, db, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, db, "Bearer "+replacement); err == nil {
		t.Fatal("revoked credential authenticates")
	}
	if _, _, err := Rotate(ctx, db, key.ID); err == nil {
		t.Fatal("revoked key can be revived through rotation")
	}
	if err := Revoke(ctx, db, key.ID); err == nil {
		t.Fatal("second revocation reported success")
	}
}

func TestKeyValidationExpiryAndCancellation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		scopes []string
		days   int
	}{
		{"", []string{rpc.ScopeNodesRead}, 1}, {strings.Repeat("a", 81), []string{rpc.ScopeNodesRead}, 1},
		{"test\nname", []string{rpc.ScopeNodesRead}, 1}, {"key", nil, 1}, {"key", []string{"*"}, 1},
		{"key", []string{"admin:*"}, 1}, {"key", []string{rpc.ScopeNodesRead}, 0}, {"key", []string{rpc.ScopeNodesRead}, 366},
	} {
		if _, _, err := Create(ctx, db, tc.name, tc.scopes, tc.days, "issuer"); err == nil {
			t.Fatalf("accepted invalid key parameters: name length %d days %d scopes %v", len(tc.name), tc.days, tc.scopes)
		}
	}
	key, token, err := Create(ctx, db, "expiry", []string{rpc.ScopeNodesRead}, 1, "issuer")
	if err != nil {
		t.Fatal(err)
	}
	for _, auth := range []string{"", token, "bearer " + token, "Bearer wrong", "Bearer " + token + " ", "Bearer " + strings.Repeat("x", 513)} {
		if _, err := Authenticate(ctx, db, auth); err == nil {
			t.Fatal("accepted malformed credential")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Authenticate(canceled, db, "Bearer "+token); err == nil {
		t.Fatal("ignored canceled authentication")
	}
	if _, _, err := Rotate(canceled, db, key.ID); err == nil {
		t.Fatal("ignored canceled rotation")
	}
	if _, err := Authenticate(ctx, db, "Bearer "+token); err != nil {
		t.Fatal("failed rotation invalidated the old token")
	}
	if err := db.Model(&key).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, db, "Bearer "+token); err == nil {
		t.Fatal("expired key authenticates")
	}
	if _, _, err := Rotate(ctx, db, key.ID); err == nil {
		t.Fatal("rotation renews an expired key")
	}
}

func TestLegacyMigrationIsAtomicReadOnlyAndIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	const legacy = "legacy-monitor-key-for-test"
	encoded, _ := json.Marshal(legacy)
	if err := db.Create(&config.ConfigItem{Key: config.ApiKeyKey, Value: string(encoded)}).Error; err != nil {
		t.Fatal(err)
	}
	// The plaintext config alone must never authenticate, even before migration.
	if _, err := Authenticate(ctx, db, "Bearer "+legacy); err == nil {
		t.Fatal("unmigrated legacy key has implicit access")
	}
	if err := MigrateLegacy(db); err != nil {
		t.Fatal(err)
	}
	principal, err := Authenticate(ctx, db, "Bearer "+legacy)
	if err != nil || !rpc.CheckPrincipal(principal, "admin:getDatabaseSize") || rpc.CheckPrincipal(principal, "admin:exec") || rpc.CheckPrincipal(principal, "admin:dbQuery") || rpc.CheckPrincipal(principal, "admin:getClientToken") {
		t.Fatalf("legacy scope policy: %v", err)
	}
	var rows int64
	db.Model(&config.ConfigItem{}).Where("key = ?", config.ApiKeyKey).Count(&rows)
	if rows != 0 {
		t.Fatal("plaintext config survived migration")
	}
	keys, err := List(ctx, db)
	if err != nil || len(keys) != 1 || time.Until(keys[0].ExpiresAt) > 30*24*time.Hour {
		t.Fatalf("migration metadata: %v", err)
	}
	_, replacement, err := Rotate(ctx, db, keys[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := Revoke(ctx, db, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	// Even re-importing the same legacy row must not revive a revoked digest.
	if err := db.Create(&config.ConfigItem{Key: config.ApiKeyKey, Value: string(encoded)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(db); err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, db, "Bearer "+legacy); err == nil {
		t.Fatal("migration revived a revoked key")
	}
	if _, err := Authenticate(ctx, db, "Bearer "+replacement); err == nil {
		t.Fatal("migration revived its revoked replacement")
	}
	keys, _ = List(ctx, db)
	if len(keys) != 1 {
		t.Fatal("repeat migration duplicates keys")
	}
}

func TestKeyQuotaAndUnknownStoredScopes(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	for i := 0; i < MaxActiveKeys; i++ {
		if err := db.Create(&models.APIKey{ID: fmt.Sprint(i), Name: "fixture", TokenDigest: digest(fmt.Sprint(i)), Scopes: []string{rpc.ScopeNodesRead}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Create(context.Background(), db, "over quota", []string{rpc.ScopeNodesRead}, 1, "issuer"); err == nil {
		t.Fatal("active key quota ignored")
	}
	if err := Revoke(context.Background(), db, "0"); err != nil {
		t.Fatal(err)
	}
	key, token, err := Create(context.Background(), db, "after revoke", []string{rpc.ScopeNodesRead}, 1, "issuer")
	if err != nil {
		t.Fatal(err)
	}
	key.Scopes = []string{"*"}
	if err := db.Save(&key).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(context.Background(), db, "Bearer "+token); err == nil {
		t.Fatal("unsupported database grants authenticated")
	}
}

func TestLegacyMigrationFailureKeepsCredentialAndHasNoPartialGrant(t *testing.T) {
	db := testDB(t)
	const token = "test-only-legacy-atomic-key"
	encoded, _ := json.Marshal(token)
	if err := db.Create(&config.ConfigItem{Key: config.ApiKeyKey, Value: string(encoded)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER refuse_legacy_delete BEFORE DELETE ON configs
        WHEN OLD.key = 'api_key' BEGIN SELECT RAISE(ABORT, 'test failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(db); err == nil {
		t.Fatal("migration hid a delete failure")
	}
	var count int64
	db.Model(&models.APIKey{}).Count(&count)
	if count != 0 {
		t.Fatal("failed migration left a partial credential grant")
	}
	db.Model(&config.ConfigItem{}).Where("key = ?", config.ApiKeyKey).Count(&count)
	if count != 1 {
		t.Fatal("failed migration lost the original row")
	}
	if err := db.Exec("DROP TRIGGER refuse_legacy_delete").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(db); err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(context.Background(), db, "Bearer "+token); err != nil {
		t.Fatal("failed migration could not be retried")
	}
}

func TestLegacyMigrationMarkerDoesNotAuthorizeKeysAddedAfterFreshInstall(t *testing.T) {
	db := testDB(t)
	if err := MigrateLegacy(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&config.ConfigItem{Key: config.ApiKeyKey, Value: `"test-only-reintroduced-global-key"`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&models.APIKey{}).Count(&count)
	if count != 0 {
		t.Fatal("a fresh install later migrated an untrusted legacy setting")
	}
}
