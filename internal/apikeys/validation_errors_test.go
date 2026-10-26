package apikeys

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

func TestKeyValidationErrorIdentitiesPreserveDiagnostics(t *testing.T) {
	for _, item := range []struct {
		name   string
		scopes []string
		days   int
		cause  error
		text   string
	}{
		{"", []string{rpc.ScopeNodesRead}, 1, ErrNameInvalid, "name must be 1 to 80 bytes without control characters"},
		{strings.Repeat("x", 81), []string{rpc.ScopeNodesRead}, 1, ErrNameInvalid, ErrNameInvalid.Error()},
		{strings.Repeat("\u754c", 27), []string{rpc.ScopeNodesRead}, 1, ErrNameInvalid, ErrNameInvalid.Error()},
		{"line\nbreak", []string{rpc.ScopeNodesRead}, 1, ErrNameInvalid, ErrNameInvalid.Error()},
		{"\nleading control", []string{rpc.ScopeNodesRead}, 1, ErrNameInvalid, ErrNameInvalid.Error()},
		{string([]byte{'x', 0xff}), []string{rpc.ScopeNodesRead}, 1, ErrNameInvalid, ErrNameInvalid.Error()},
		{"key", nil, 1, ErrScopesRequired, "choose at least one supported scope (no wildcards)"},
		{"key", make([]string, len(rpc.APIKeyScopes())+1), 1, ErrScopesRequired, ErrScopesRequired.Error()},
		{"key", []string{"admin:*"}, 1, ErrScopeUnsupported, "unsupported API key scope: admin:*"},
		{"key", []string{rpc.ScopeNodesRead}, 0, ErrExpiryInvalid, "expires_days must be between 1 and 365"},
		{"key", []string{rpc.ScopeNodesRead}, 366, ErrExpiryInvalid, ErrExpiryInvalid.Error()},
	} {
		key, token, err := Create(context.Background(), nil, item.name, item.scopes, item.days, "owned")
		if !errors.Is(err, item.cause) || err.Error() != item.text || key.ID != "" || token != "" {
			t.Fatalf("validation contract changed: key=%q token returned=%t err=%v", key.ID, token != "", err)
		}
		if errors.Is(errors.New(item.text), item.cause) || !errors.Is(fmt.Errorf("wrapped: %w", err), item.cause) {
			t.Fatal("validation identity must not depend on matching diagnostic text")
		}
	}
}

func TestKeyLimitErrorIdentityAndExplicitRecovery(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	for index := 0; index < MaxActiveKeys; index++ {
		key := models.APIKey{ID: fmt.Sprint(index), Name: "owned", TokenDigest: digest(fmt.Sprint(index)),
			Scopes: []string{rpc.ScopeNodesRead}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := db.Create(&key).Error; err != nil {
			t.Fatal(err)
		}
	}
	key, token, err := Create(t.Context(), db, "over limit", []string{rpc.ScopeNodesRead}, 1, "owned")
	if !errors.Is(err, ErrActiveLimit) || err.Error() != "at most 100 active API keys are supported; revoke a key first" || key.ID != "" || token != "" {
		t.Fatal("limit refusal lost identity, diagnostic or empty result")
	}
	if err := Revoke(t.Context(), db, "0"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(t.Context(), db, "explicit retry", []string{rpc.ScopeNodesRead}, 1, "owned"); err != nil {
		t.Fatal(err)
	}
}
