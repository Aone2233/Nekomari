package jsonrpc

import (
	"context"
	"errors"

	"github.com/Aone2233/nekomari/database/auditlog"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/internal/apikeys"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/google/uuid"
)

func init() {
	reg("listAPIKeys", adminListAPIKeys, "List API key metadata and supported scopes (session only)")
	reg("createAPIKey", adminCreateAPIKey, "Create an expiring scoped key; token is returned once (session + step-up)")
	reg("rotateAPIKey", adminRotateAPIKey, "Replace a token immediately, preserving scopes and expiry (session + step-up)")
	reg("revokeAPIKey", adminRevokeAPIKey, "Revoke an API key immediately (session + step-up)")
	for _, method := range []string{"admin:createAPIKey", "admin:rotateAPIKey", "admin:revokeAPIKey"} {
		rpc.MarkSensitive(method)
	}
}

func apiKeySession(ctx context.Context) *rpc.JsonRpcError {
	meta := rpc.MetaFromContext(ctx)
	if meta == nil || meta.Principal == nil || meta.Principal.Type != rpc.PrincipalUser || meta.Principal.UserUUID == "" || meta.SessionToken == "" {
		return rpc.MakeLocalizedError(rpc.PermissionDenied, "API key management requires an administrator session", "session_required")
	}
	return nil
}

func adminListAPIKeys(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if jerr := apiKeySession(ctx); jerr != nil {
		return nil, jerr
	}
	keys, err := apikeys.List(ctx, dbcore.GetDBInstance())
	if err != nil {
		return nil, rpc.MakeLocalizedError(rpc.InternalError, "Failed to list API keys", "api_keys_unavailable")
	}
	return map[string]any{"keys": keys, "scopes": rpc.APIKeyScopes()}, nil
}

func adminCreateAPIKey(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if jerr := apiKeySession(ctx); jerr != nil {
		return nil, jerr
	}
	params := struct {
		Name        string   `json:"name"`
		Scopes      []string `json:"scopes"`
		ExpiresDays int      `json:"expires_days"`
	}{ExpiresDays: 90}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeLocalizedError(rpc.InvalidParams, "Invalid API key parameters", "invalid_params")
	}
	key, token, err := apikeys.Create(ctx, dbcore.GetDBInstance(), params.Name, params.Scopes, params.ExpiresDays, rpc.MetaFromContext(ctx).Principal.UserUUID)
	if err != nil {
		return nil, apiKeyCreateError(err)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "create API key:"+key.ID, "warn")
	return map[string]any{"key": key, "token": token}, nil
}

func apiKeyCreateError(err error) *rpc.JsonRpcError {
	// Classify owned validation errors, never SQL/upstream diagnostic text.
	switch {
	case errors.Is(err, apikeys.ErrNameInvalid):
		return rpc.MakeLocalizedError(rpc.InvalidParams, err.Error(), "api_key_name_invalid")
	case errors.Is(err, apikeys.ErrScopesRequired), errors.Is(err, apikeys.ErrScopeUnsupported):
		return rpc.MakeLocalizedError(rpc.InvalidParams, err.Error(), "api_key_scopes_invalid")
	case errors.Is(err, apikeys.ErrExpiryInvalid):
		return rpc.MakeLocalizedError(rpc.InvalidParams, err.Error(), "api_key_expiry_invalid")
	case errors.Is(err, apikeys.ErrActiveLimit):
		return rpc.MakeLocalizedError(rpc.InvalidParams, err.Error(), "api_key_limit")
	default:
		return rpc.MakeLocalizedError(rpc.InternalError, "Failed to create API key", "api_key_create_failed")
	}
}

func apiKeyID(req *rpc.JsonRpcRequest) (string, *rpc.JsonRpcError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := req.BindParams(&params); err != nil || uuid.Validate(params.ID) != nil {
		return "", rpc.MakeLocalizedError(rpc.InvalidParams, "id must be an API key UUID", "api_key_id_invalid")
	}
	return params.ID, nil
}

func adminRotateAPIKey(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if jerr := apiKeySession(ctx); jerr != nil {
		return nil, jerr
	}
	id, jerr := apiKeyID(req)
	if jerr != nil {
		return nil, jerr
	}
	key, token, err := apikeys.Rotate(ctx, dbcore.GetDBInstance(), id)
	if err != nil {
		return nil, rpc.MakeLocalizedError(rpc.InvalidParams, "API key cannot be rotated (missing, expired or revoked)", "api_key_rotation_failed")
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "rotate API key:"+id, "warn")
	return map[string]any{"key": key, "token": token}, nil
}

func adminRevokeAPIKey(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if jerr := apiKeySession(ctx); jerr != nil {
		return nil, jerr
	}
	id, jerr := apiKeyID(req)
	if jerr != nil {
		return nil, jerr
	}
	if err := apikeys.Revoke(ctx, dbcore.GetDBInstance(), id); err != nil {
		return nil, rpc.MakeLocalizedError(rpc.InvalidParams, "API key cannot be revoked (missing or already revoked)", "api_key_revocation_failed")
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "revoke API key:"+id, "warn")
	return nil, nil
}
