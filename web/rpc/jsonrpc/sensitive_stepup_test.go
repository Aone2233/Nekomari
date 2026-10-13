package jsonrpc

import (
	"context"
	"testing"

	"github.com/Aone2233/nekomari/database/accounts"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

// sensitiveMethodsAllowlist 是当前【经过审阅】的敏感操作名单。
//
// 这些方法除了 admin 角色，还要求敏感操作二次验证（敏感 2FA）。名单是安全边界，
// 所以它的增删都必须被人看见：本用例断言它恰好等于这份名单，任何一方变化都会失败。
var sensitiveMethodsAllowlist = []string{
	"admin:dbExec",
	"admin:dbQuery",
	"admin:exec",
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
