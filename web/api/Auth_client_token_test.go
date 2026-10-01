package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newAuthTestContext(method, target string, headers map[string]string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		c.Request.Header.Set(key, value)
	}
	return c
}

// 新增路径：凭据走 Authorization 头。
//
// 这就是这次修复的目的——URL 查询串会被 nginx 与 Cloudflare 的访问日志完整记录，
// 现场实测 14 天日志里留下过 47 个不同的 agent token、其中 9 个仍是有效凭据；
// 请求头不会被记录到那个位置。
func TestExtractClientTokenAcceptsBearerHeader(t *testing.T) {
	c := newAuthTestContext("GET", "/api/me", map[string]string{
		"Authorization": "Bearer header-token",
	})
	if got := extractClientToken(c); got != "header-token" {
		t.Fatalf("Authorization 头未被采纳: got %q, want %q", got, "header-token")
	}
}

// 向后兼容：两个查询参数分支必须继续有效。
//
// 现场还有 9 台 agent 跑 v1.6.2，它们只会用查询串；这条测试是为了保证
// "服务端先发、agent 后升"的顺序不会把在跑的节点踢下线。
func TestExtractClientTokenKeepsQueryParams(t *testing.T) {
	cases := map[string]string{
		"/api/me?token=query-token":             "query-token",
		"/api/me?Authorization=legacy-token":    "legacy-token",
	}
	for target, want := range cases {
		c := newAuthTestContext("GET", target, nil)
		if got := extractClientToken(c); got != want {
			t.Fatalf("%s: got %q, want %q", target, got, want)
		}
	}
}

// 查询参数仍然优先于请求头：不改变既有优先级，避免同时带两种凭据时行为漂移。
func TestExtractClientTokenQueryWinsOverHeader(t *testing.T) {
	c := newAuthTestContext("GET", "/api/me?token=query-token", map[string]string{
		"Authorization": "Bearer header-token",
	})
	if got := extractClientToken(c); got != "query-token" {
		t.Fatalf("查询参数应优先: got %q, want %q", got, "query-token")
	}
}

// 非 Bearer 的 Authorization 头不能被当成 client token。
//
// 同一个头也被 API Key 认证使用（principal.go 的 isApiKeyValid），
// 所以只认 "Bearer " 前缀、且前缀后必须有内容。
func TestExtractClientTokenIgnoresNonBearerHeader(t *testing.T) {
	cases := map[string]string{
		"Basic dXNlcjpwYXNz": "",
		"Bearer ":            "",
		"Bearer":             "",
	}
	for header, want := range cases {
		c := newAuthTestContext("GET", "/api/me", map[string]string{"Authorization": header})
		if got := extractClientToken(c); got != want {
			t.Fatalf("Authorization=%q: got %q, want %q", header, got, want)
		}
	}
}
