package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
)

// sensitiveMethodsAllowlist 是当前【经过审阅】的敏感操作名单。
//
// 这些方法除了 admin 角色，还要求敏感操作二次验证（敏感 2FA）。名单是安全边界，
// 所以它的增删都必须被人看见：本用例断言它恰好等于这份名单，任何一方变化都会失败。
var sensitiveMethodsAllowlist = []string{
	"admin:dbExec",
	"admin:dbQuery",
	"admin:exec",
	"admin:exportConfigSecrets",
	"admin:getClientToken",
	"admin:rollAgentUpdate",
}

func TestSensitiveMethodsAreAnExplicitList(t *testing.T) {
	got := rpc.SensitiveMethods()
	if len(got) != len(sensitiveMethodsAllowlist) {
		t.Fatalf("sensitive methods = %v, want %v", got, sensitiveMethodsAllowlist)
	}
	for i := range got {
		if got[i] != sensitiveMethodsAllowlist[i] {
			t.Fatalf("sensitive methods = %v, want %v", got, sensitiveMethodsAllowlist)
		}
	}
}

// TestDatabaseRPCsRequireASecondFactor 证明这两条 RPC 真的走敏感操作门：
// 一个已登录、已通过 admin 角色校验，但没有提供第二因子的会话必须被拒。
//
// 反向对照同样重要：同一个调用者在没有开启 2FA 的账号下必须能通过这道门，
// 否则「一律拒绝」也能让上面的断言通过，而那会是一个坏掉的接口而不是一道门。
func TestDatabaseRPCsRequireASecondFactor(t *testing.T) {
	// 账号 A：开启 2FA —— 不带 code 调用必须被拒。
	withTwoFactor, err := accounts.CreateAccount("stepup-2fa-user", "StepUp-2fa-Passw0rd")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	secret, _, err := accounts.Generate2Fa()
	if err != nil {
		t.Fatalf("generate 2fa secret: %v", err)
	}
	if err := accounts.Enable2Fa(withTwoFactor.UUID, secret); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}

	// 账号 B：未开启 2FA —— 同一道门必须放行。
	withoutTwoFactor, err := accounts.CreateAccount("stepup-plain-user", "StepUp-plain-Passw0rd")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	call := func(method, userUUID string) *rpc.JsonRpcResponse {
		t.Helper()
		principal := rpc.PrincipalFromRole(rpc.RoleAdmin)
		principal.UserUUID = userUUID
		meta := &rpc.ContextMeta{Principal: principal, Permission: rpc.RoleAdmin, UserUUID: userUUID}
		params := map[string]any{"sql": "SELECT 1"}
		if method == "admin:dbExec" {
			params = map[string]any{"sql": "SELECT 1"}
		}
		return dispatchWithSensitive(context.Background(), nil, meta, &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION,
			Method:  method,
			Params:  params,
		})
	}

	for _, method := range []string{"admin:dbQuery", "admin:dbExec"} {
		// 前置条件：这个方法确实被登记为敏感操作，否则下面的断言会因为它
		// 「恰好也返回 PermissionDenied」而假通过。
		if !rpc.IsSensitive(method) {
			t.Fatalf("%s is not marked sensitive", method)
		}

		refused := call(method, withTwoFactor.UUID)
		if refused == nil || refused.Error == nil {
			t.Fatalf("%s without a second factor returned %#v, want a refusal", method, refused)
		}
		if refused.Error.Code != rpc.PermissionDenied {
			t.Fatalf("%s without a second factor = %+v, want PermissionDenied", method, refused.Error)
		}

		allowed := call(method, withoutTwoFactor.UUID)
		if allowed != nil && allowed.Error != nil && allowed.Error.Code == rpc.PermissionDenied {
			t.Fatalf("%s was refused for an account without 2FA: %+v", method, allowed.Error)
		}
	}
}

// TestEverySensitiveMethodRefusesASessionWithoutASecondFactor walks the reviewed list itself
// rather than a hand-copied subset. The two lists must not drift: a method that is marked
// sensitive but never exercised is a gate that could be broken by a typo in its name, and this
// catches that without reaching any handler (the gate runs before Dispatch).
func TestEverySensitiveMethodRefusesASessionWithoutASecondFactor(t *testing.T) {
	account, err := accounts.CreateAccount("stepup-gate-user", "StepUp-gate-Passw0rd")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	secret, _, err := accounts.Generate2Fa()
	if err != nil {
		t.Fatalf("generate 2fa secret: %v", err)
	}
	if err := accounts.Enable2Fa(account.UUID, secret); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}

	principal := rpc.PrincipalFromRole(rpc.RoleAdmin)
	principal.UserUUID = account.UUID
	meta := &rpc.ContextMeta{Principal: principal, Permission: rpc.RoleAdmin, UserUUID: account.UUID}

	for _, method := range sensitiveMethodsAllowlist {
		if !rpc.IsSensitive(method) {
			t.Fatalf("%s is in the allowlist but not marked sensitive", method)
		}
		response := dispatchWithSensitive(context.Background(), nil, meta, &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION,
			Method:  method,
			Params:  map[string]any{},
		})
		if response == nil || response.Error == nil {
			t.Fatalf("%s without a second factor returned %#v, want a refusal", method, response)
		}
		if response.Error.Code != rpc.PermissionDenied {
			t.Fatalf("%s without a second factor = %+v, want PermissionDenied", method, response.Error)
		}
	}
}

// admin:getClientToken 返回的是 Agent 的长期 bearer 凭据：普通会话读取它必须过敏感 2FA，
// 而 API key 仍按既有设计豁免 —— 门开了，但豁免范围没有扩大。
//
// 三条正向对照都必须存在，否则「一律拒绝」也能让拒绝断言通过：带正确 TOTP 的会话能读到
// token、API key 不提供 code 也能读、未开启 2FA 的账号按既有设计不需要 step-up。
func TestGetClientTokenRequiresASecondFactorButAPIKeysStayExempt(t *testing.T) {
	if !rpc.IsSensitive("admin:getClientToken") {
		t.Fatal("admin:getClientToken is not marked sensitive: reading an agent credential must require the step-up")
	}

	db := dbcore.GetDBInstance()
	clientUUID := uuid.NewString()
	const clientToken = "step-up-client-token"
	if err := db.Create(&models.Client{
		UUID: clientUUID, Name: "step-up-token-client", Token: clientToken,
	}).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}

	call := func(principal *rpc.Principal, code string) *rpc.JsonRpcResponse {
		t.Helper()
		meta := &rpc.ContextMeta{
			Principal:  principal,
			Permission: principal.PrimaryRole(),
			UserUUID:   principal.UserUUID,
		}
		params := map[string]any{"uuid": clientUUID}
		if code != "" {
			params["2fa_code"] = code
		}
		return dispatchWithSensitive(context.Background(), nil, meta, &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION,
			Method:  "admin:getClientToken",
			Params:  params,
		})
	}
	requireToken := func(response *rpc.JsonRpcResponse, source string) {
		t.Helper()
		if response == nil || response.Error != nil {
			t.Fatalf("%s was refused: %#v", source, response)
		}
		payload, ok := response.Result.(map[string]any)
		if !ok || payload["token"] != clientToken {
			t.Fatalf("%s returned %#v, want token %q", source, response.Result, clientToken)
		}
	}

	// 账号 A：开启 2FA。不带第二因子必须被拒 —— 这是这次改动新增的行为。
	withTwoFactor, err := accounts.CreateAccount("stepup-token-user", "StepUp-Token-Passw0rd")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	secret, _, err := accounts.Generate2Fa()
	if err != nil {
		t.Fatalf("generate 2fa secret: %v", err)
	}
	if err := accounts.Enable2Fa(withTwoFactor.UUID, secret); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}

	refused := call(rpc.NewUserPrincipal(withTwoFactor.UUID), "")
	if refused == nil || refused.Error == nil {
		t.Fatalf("a session without a second factor was not refused: %#v", refused)
	}
	if refused.Error.Code != rpc.PermissionDenied {
		t.Fatalf("a session without a second factor = %+v, want PermissionDenied", refused.Error)
	}

	// 同一个账号带上正确的 TOTP 必须通过：证明这是一道门，不是一堵墙。
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	requireToken(call(rpc.NewUserPrincipal(withTwoFactor.UUID), code), "a session with a valid TOTP")

	// API key：既有豁免不变，不需要第二因子。
	requireToken(call(rpc.NewAPIKeyPrincipal(), ""), "an API key")

	// 未开启 2FA 的普通账号：既有设计里也不需要 step-up。
	plain, err := accounts.CreateAccount("stepup-token-plain", "StepUp-Token-Plain1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	requireToken(call(rpc.NewUserPrincipal(plain.UUID), ""), "a session without 2FA")
}

// API key 的审计归属：日志必须写 "api_key"，而不是空串或全零占位 UUID；
// 普通会话仍然记录真实用户，不能一并变成 api_key。
func TestAPIKeyAuditActorIsNamedExplicitly(t *testing.T) {
	db := dbcore.GetDBInstance()
	clientUUID := uuid.NewString()
	if err := db.Create(&models.Client{
		UUID: clientUUID, Name: "audit-actor-client", Token: "audit-actor-token",
	}).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}

	// meta 里刻意放旧中间件的全零占位 UUID：API key 的审计不能被它伪装成用户。
	meta := &rpc.ContextMeta{
		Principal:  rpc.NewAPIKeyPrincipal(),
		Permission: rpc.RoleAdmin,
		UserUUID:   "00000000-0000-0000-0000-000000000000",
		RemoteIP:   "203.0.113.9",
	}
	response := dispatchWithSensitive(context.Background(), nil, meta, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:getClientToken",
		Params:  map[string]any{"uuid": clientUUID},
	})
	if response == nil || response.Error != nil {
		t.Fatalf("an API key was refused: %#v", response)
	}

	var entry models.Log
	if err := db.Where("message = ?", "read client token:"+clientUUID).First(&entry).Error; err != nil {
		t.Fatalf("the token read was not audited: %v", err)
	}
	if entry.UUID != "api_key" {
		t.Errorf("audit actor = %q, want %q: an API key must not look like a user", entry.UUID, "api_key")
	}
	if entry.IP != "203.0.113.9" {
		t.Errorf("audit ip = %q, want the request source IP", entry.IP)
	}

	// 普通会话的审计仍然记录真实用户 UUID。
	userContext := rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{
		Principal:  rpc.NewUserPrincipal("user-uuid"),
		Permission: rpc.RoleAdmin,
		UserUUID:   "user-uuid",
		RemoteIP:   "198.51.100.7",
	})
	if actor, ip := auditActor(userContext); actor != "user-uuid" || ip != "198.51.100.7" {
		t.Errorf("user audit actor = (%q, %q), want the real UUID and IP", actor, ip)
	}

	// 没有 meta 时返回空值且不 panic（与改动前一致）。
	if actor, ip := auditActor(context.Background()); actor != "" || ip != "" {
		t.Errorf("auditActor(background) = (%q, %q), want empty", actor, ip)
	}
}
