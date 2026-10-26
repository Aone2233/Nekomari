package jsonrpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/apikeys"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/google/uuid"
)

func TestAPIKeyCreateErrorsUseIdentityNotDiagnosticText(t *testing.T) {
	for _, item := range []struct {
		cause error
		key   string
	}{
		{apikeys.ErrNameInvalid, "api_key_name_invalid"},
		{apikeys.ErrScopesRequired, "api_key_scopes_invalid"},
		{apikeys.ErrScopeUnsupported, "api_key_scopes_invalid"},
		{apikeys.ErrExpiryInvalid, "api_key_expiry_invalid"},
		{apikeys.ErrActiveLimit, "api_key_limit"},
		{context.Canceled, "api_key_create_failed"},
		{errors.New("private storage detail"), "api_key_create_failed"},
	} {
		for _, cause := range []error{item.cause, fmt.Errorf("wrapped: %w", item.cause)} {
			actual := apiKeyCreateError(cause)
			code, message := rpc.InvalidParams, cause.Error()
			if item.key == "api_key_create_failed" {
				code, message = rpc.InternalError, "Failed to create API key"
			}
			if actual.Code != code || actual.Message != message || actual.LocalizationKey() != item.key {
				t.Fatalf("create error contract changed: %+v", actual)
			}
		}
		if apiKeyCreateError(errors.New(item.cause.Error())).LocalizationKey() != "api_key_create_failed" {
			t.Fatal("untyped diagnostic was reinterpreted as an owned validation error")
		}
	}
}

func TestLocalizedAPIKeyValidationAndSessionErrors(t *testing.T) {
	ctx := rpc.NewContextWithMeta(t.Context(), &rpc.ContextMeta{
		Principal: rpc.NewUserPrincipal(uuid.NewString()), SessionToken: "owned-session",
	})
	for _, item := range []struct {
		name    string
		handler rpc.Handler
		params  any
		key     string
		message string
	}{
		{"shape", adminCreateAPIKey, []any{true}, "invalid_params", "Invalid API key parameters"},
		{"name", adminCreateAPIKey, map[string]any{}, "api_key_name_invalid", "name must be 1 to 80 bytes without control characters"},
		{"scope", adminCreateAPIKey, map[string]any{"name": "owned", "scopes": []string{"admin:*"}}, "api_key_scopes_invalid", "unsupported API key scope: admin:*"},
		{"empty scopes", adminCreateAPIKey, map[string]any{"name": "owned"}, "api_key_scopes_invalid", "choose at least one supported scope (no wildcards)"},
		{"expiry", adminCreateAPIKey, map[string]any{"name": "owned", "scopes": []string{rpc.ScopeNodesRead}, "expires_days": 366}, "api_key_expiry_invalid", "expires_days must be between 1 and 365"},
		{"rotate ID", adminRotateAPIKey, nil, "api_key_id_invalid", "id must be an API key UUID"},
		{"revoke ID", adminRevokeAPIKey, map[string]any{"id": true}, "api_key_id_invalid", "id must be an API key UUID"},
		{"rotate missing", adminRotateAPIKey, map[string]any{"id": uuid.NewString()}, "api_key_rotation_failed", "API key cannot be rotated (missing, expired or revoked)"},
		{"revoke missing", adminRevokeAPIKey, map[string]any{"id": uuid.NewString()}, "api_key_revocation_failed", "API key cannot be revoked (missing or already revoked)"},
	} {
		t.Run(item.name, func(t *testing.T) {
			value, err := item.handler(ctx, &rpc.JsonRpcRequest{Params: item.params})
			if value != nil || err == nil || err.Code != rpc.InvalidParams || err.Message != item.message || err.LocalizationKey() != item.key {
				t.Fatalf("API key error changed: result=%v err=%+v", value, err)
			}
		})
	}
	for _, meta := range []*rpc.ContextMeta{nil, {}, {Principal: rpc.NewAPIKeyPrincipal()},
		{Principal: rpc.NewUserPrincipal("owned")}, {Principal: rpc.NewUserPrincipal(""), SessionToken: "owned"}} {
		for _, handler := range []rpc.Handler{adminListAPIKeys, adminCreateAPIKey, adminRotateAPIKey, adminRevokeAPIKey} {
			value, err := handler(rpc.NewContextWithMeta(t.Context(), meta), &rpc.JsonRpcRequest{})
			if value != nil || err == nil || err.Code != rpc.PermissionDenied || err.LocalizationKey() != "session_required" || err.Message != "API key management requires an administrator session" {
				t.Fatalf("session boundary changed: result=%v err=%+v", value, err)
			}
		}
	}
}

func TestLocalizedAPIKeyStorageFailuresKeepExistingCodesAndTokens(t *testing.T) {
	db := dbcore.GetDBInstance()
	issuer := uuid.NewString()
	key, token, err := apikeys.Create(t.Context(), db, "owned", []string{rpc.ScopeNodesRead}, 1, issuer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Delete(&models.APIKey{}, "issued_by = ?", issuer) })
	ctx, cancel := context.WithCancel(rpc.NewContextWithMeta(t.Context(), &rpc.ContextMeta{
		Principal: rpc.NewUserPrincipal(issuer), SessionToken: "owned-session",
	}))
	cancel()
	for _, item := range []struct {
		handler rpc.Handler
		params  any
		key     string
		code    int
	}{
		{adminListAPIKeys, nil, "api_keys_unavailable", rpc.InternalError},
		{adminCreateAPIKey, map[string]any{"name": "owned canceled", "scopes": []string{rpc.ScopeNodesRead}}, "api_key_create_failed", rpc.InternalError},
		{adminRotateAPIKey, map[string]any{"id": key.ID}, "api_key_rotation_failed", rpc.InvalidParams},
		{adminRevokeAPIKey, map[string]any{"id": key.ID}, "api_key_revocation_failed", rpc.InvalidParams},
	} {
		value, err := item.handler(ctx, &rpc.JsonRpcRequest{Params: item.params})
		if value != nil || err == nil || err.Code != item.code || err.LocalizationKey() != item.key || strings.Contains(err.Message, token) {
			t.Fatalf("storage refusal changed: result present=%t err=%+v", value != nil, err)
		}
		if _, err := apikeys.Authenticate(t.Context(), db, "Bearer "+token); err != nil {
			t.Fatal("failed mutation invalidated the original credential")
		}
	}
}

func TestLocalizedAPIKeyActiveLimitRefusesWithoutIssuingToken(t *testing.T) {
	db := dbcore.GetDBInstance()
	issuer := uuid.NewString()
	ctx := rpc.NewContextWithMeta(t.Context(), &rpc.ContextMeta{
		Principal: rpc.NewUserPrincipal(issuer), SessionToken: "owned-session",
	})
	t.Cleanup(func() { db.Delete(&models.APIKey{}, "issued_by = ?", issuer) })
	for index := 0; index < apikeys.MaxActiveKeys; index++ {
		if _, _, err := apikeys.Create(t.Context(), db, "owned limit", []string{rpc.ScopeNodesRead}, 1, issuer); err != nil {
			t.Fatal(err)
		}
	}
	var before int64
	if err := db.Model(&models.APIKey{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	value, err := adminCreateAPIKey(ctx, &rpc.JsonRpcRequest{Params: map[string]any{
		"name": "over limit", "scopes": []string{rpc.ScopeNodesRead},
	}})
	if value != nil || err == nil || err.Code != rpc.InvalidParams || err.LocalizationKey() != "api_key_limit" || err.Message != "at most 100 active API keys are supported; revoke a key first" {
		t.Fatalf("limit error changed: result=%v err=%+v", value, err)
	}
	var after int64
	if err := db.Model(&models.APIKey{}).Count(&after).Error; err != nil || after != before {
		t.Fatalf("refused create changed key rows: %d -> %d, %v", before, after, err)
	}
}
