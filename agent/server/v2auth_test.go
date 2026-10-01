package server

import (
	"net/http"
	"strings"
	"testing"
)

// 凭据必须走请求头，不能回到 URL 查询串。
//
// 这条测试是为了防回归：现场实测面板的 14 天 nginx 日志里留下过 47 个不同的 agent
// token，其中 9 个仍是当天有效的现役凭据——所以任何"把 token 拼回 URL"的改动都是在
// 制造同一类泄露。它同样确认端点拼接本身没有被改坏。
func TestV2RPCEndpointCarriesNoCredential(t *testing.T) {
	saved := *flags
	defer func() { *flags = saved }()

	flags.Endpoint = "https://panel.example.com/"
	flags.Token = "secret-token"

	endpoint := v2RPCEndpoint()
	if endpoint != "https://panel.example.com/api/clients/v2/rpc" {
		t.Fatalf("端点拼接不正确: %q", endpoint)
	}
	if strings.Contains(endpoint, "token") || strings.Contains(endpoint, "secret-token") {
		t.Fatalf("端点里不应出现任何凭据: %q", endpoint)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	setV2Auth(req)
	if got := req.Header.Get("Authorization"); got != "Bearer secret-token" {
		t.Fatalf("Authorization 头不正确: got %q, want %q", got, "Bearer secret-token")
	}

	// WebSocket 握手用的是同一份凭据，只是以 http.Header 形式传入 Dial。
	if got := v2RPCAuthHeaders().Get("Authorization"); got != "Bearer secret-token" {
		t.Fatalf("WebSocket 握手头不正确: got %q, want %q", got, "Bearer secret-token")
	}
}

// setV2Auth 必须覆盖调用方可能已经设过的 Authorization 头，避免出现
// "半新半旧"的请求同时带两种凭据。
func TestSetV2AuthOverwritesExistingHeader(t *testing.T) {
	saved := *flags
	defer func() { *flags = saved }()

	flags.Token = "new-token"
	req, err := http.NewRequest(http.MethodPost, "https://panel.example.com/api/clients/v2/rpc", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer stale-token")

	setV2Auth(req)

	if got := req.Header.Get("Authorization"); got != "Bearer new-token" {
		t.Fatalf("Authorization 头未被覆盖: got %q, want %q", got, "Bearer new-token")
	}
}
