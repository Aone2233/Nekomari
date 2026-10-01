package jsonrpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
)

type getMeResponse struct {
	LoggedIn bool   `json:"logged_in"`
	SSOType  string `json:"sso_type"`
	Username string `json:"username"`
	UUID     string `json:"uuid"`
}

func callGetMe(t *testing.T, meta *rpc.ContextMeta) (getMeResponse, string) {
	t.Helper()

	ctx := rpc.NewContextWithMeta(context.Background(), meta)
	result, jerr := getMe(ctx, nil)
	if jerr != nil {
		t.Fatalf("getMe 返回错误: %v", jerr)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("序列化 getMe 结果失败: %v", err)
	}
	var resp getMeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("解析 getMe 结果失败: %v", err)
	}
	return resp, string(raw)
}

// agent 的 token 是裸凭据，不能经 /api/me 回显。此前这里写反了判断：成功查到 uuid 时
// resp.UUID 仍是 meta.ClientToken，于是持有凭据的调用方可以从 /api/me 把凭据本身取回。
// 本 PR 又让 Authorization 头路径也填满 meta.ClientToken，回显面因此扩大，必须钉住。
func TestGetMeReturnsClientUUIDInsteadOfAgentToken(t *testing.T) {
	const (
		token      = "agent-token-getme-ok"
		clientUUID = "client-uuid-getme-ok"
	)
	if err := dbcore.GetDBInstance().Create(&models.Client{
		UUID: clientUUID, Name: "getme-ok", Token: token,
	}).Error; err != nil {
		t.Fatal(err)
	}

	// meta.ClientToken 正是 Authorization: Bearer <token> 路径填进来的值。
	resp, raw := callGetMe(t, &rpc.ContextMeta{
		Principal:   rpc.NewAgentPrincipal(clientUUID),
		ClientUUID:  clientUUID,
		ClientToken: token,
	})

	if !resp.LoggedIn || resp.SSOType != "client" {
		t.Fatalf("agent 的 getMe 结果不正确: %s", raw)
	}
	if resp.UUID != clientUUID {
		t.Fatalf("getMe uuid = %q, want %q (原始结果 %s)", resp.UUID, clientUUID, raw)
	}
	if strings.Contains(raw, token) {
		t.Fatalf("getMe 把 agent 裸 token 回显出来了: %s", raw)
	}
}

// token 查不到对应客户端时，uuid 保持为空——绝不能"顺手"把 token 当成 uuid 返回。
func TestGetMeDoesNotFallBackToAgentTokenWhenLookupFails(t *testing.T) {
	const token = "agent-token-getme-unknown"

	resp, raw := callGetMe(t, &rpc.ContextMeta{
		Principal:   rpc.NewAgentPrincipal("client-uuid-getme-unknown"),
		ClientUUID:  "client-uuid-getme-unknown",
		ClientToken: token,
	})

	if resp.UUID != "" {
		t.Fatalf("token 解析失败时 uuid 应为空, got %q (原始结果 %s)", resp.UUID, raw)
	}
	if strings.Contains(raw, token) {
		t.Fatalf("getMe 在解析失败时回显了 agent 裸 token: %s", raw)
	}
}
