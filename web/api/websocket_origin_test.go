package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
)

// CheckWebSocketOrigin 会经 security.IsAPIKeyRequest 读配置库，所以这个包需要一个已绑定的
// config 库。`web/api` 其余测试不需要数据库，因此这里按需初始化而不是加一个包级 TestMain：
// GetDBInstance 会完成 config.SetDb（database/dbcore/dbcore.go）。
func initWSOriginTestConfig(t *testing.T) {
	t.Helper()
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:web_api_ws_origin_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if db == nil {
		t.Fatal("config database is not available")
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
}

// CheckWebSocketOrigin 决定一次 WebSocket 握手是否放行。
//
// 这条判据在 2026-10-01 出过一次真实事故：agent 的凭据从查询串挪到 Authorization 头之后，
// "无 Origin + 查询串里有 token" 这个识别非浏览器客户端的条件不再成立，于是 agent 的握手
// 被跨站 WebSocket 劫持防护拦成 403。v1.6.6 的发布校验正是这样失败的
// （13 passed, 2 failed；agent 日志 `Failed to connect to WebSocket: 403 Forbidden`），
// 而同一个 agent 的 POST 上报照常成功——因为那条路径不做 Origin 校验。
func TestCheckWebSocketOriginAcceptsCredentialledNonBrowserClients(t *testing.T) {
	initWSOriginTestConfig(t)

	cases := []struct {
		name    string
		origin  string
		query   string
		headers map[string]string
		want    bool
	}{
		{
			name: "agent with the credential in the query string (legacy)",
			query: "?token=agent-token",
			want: true,
		},
		{
			name:    "agent with the credential in the Authorization header",
			headers: map[string]string{"Authorization": "Bearer agent-token"},
			want:    true,
		},
		{
			name: "no Origin and no credential at all is still refused",
			want: false,
		},
		{
			name:    "an empty Bearer value is not a credential",
			headers: map[string]string{"Authorization": "Bearer "},
			want:    false,
		},
		{
			name:    "a non-Bearer Authorization scheme is not our credential",
			headers: map[string]string{"Authorization": "Basic dXNlcjpwYXNz"},
			want:    false,
		},
		{
			name:   "browser on the panel's own origin",
			origin: "http://panel.example.com",
			want:   true,
		},
		{
			name:   "browser on some other origin is refused",
			origin: "http://evil.example.com",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/clients/v2/rpc"+tc.query, nil)
			req.Host = "panel.example.com"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			if got := CheckWebSocketOrigin(req); got != tc.want {
				t.Errorf("CheckWebSocketOrigin() = %v, want %v (origin=%q query=%q headers=%v)",
					got, tc.want, tc.origin, tc.query, tc.headers)
			}
		})
	}
}
