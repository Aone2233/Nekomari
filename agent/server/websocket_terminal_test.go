package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	pkg_flags "github.com/Aone2233/nekomari/agent/cmd/flags"
	"github.com/gorilla/websocket"
)

// 终端握手此前是 `/api/clients/terminal?token=<token>&id=<id>`：凭据进了 URL，也就会进
// nginx / Cloudflare 的访问日志与各节点自己的 journal。这条测试把真实的握手请求接到一个
// 本地 WebSocket 服务端上，直接断言线上实际发出去的东西——URL 里没有凭据、凭据在
// Authorization 头、会话 id 保留。
//
// 只断言"URL 构造函数"是不够的：如果 Dial 那一行仍然传 nil（或漏传头部），只有端到端的
// 观测才能发现。
func TestTerminalHandshakeKeepsCredentialOutOfURL(t *testing.T) {
	type observation struct {
		requestURI    string
		token         string
		id            string
		authorization string
	}
	observed := make(chan observation, 1)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		observed <- observation{
			requestURI:    r.RequestURI,
			token:         query.Get("token"),
			id:            query.Get("id"),
			authorization: r.Header.Get("Authorization"),
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer panel.Close()

	preserveAgentConfig(t, panel.URL)
	// StartTerminal 在这条测试里直接返回，不去拉起真实 PTY；握手本身不受影响。
	pkg_flags.GlobalConfig.DisableWebSsh = true

	establishTerminalConnection("session-42", panel.URL)

	var got observation
	select {
	case got = <-observed:
	case <-time.After(15 * time.Second):
		t.Fatal("终端握手没有到达测试服务端")
	}

	if got.token != "" {
		t.Fatalf("终端握手把凭据放进了 URL 查询串: %s", got.requestURI)
	}
	if strings.Contains(got.requestURI, "agent-token") {
		t.Fatalf("终端握手 URL 里出现了 agent token: %s", got.requestURI)
	}
	if got.id != "session-42" {
		t.Fatalf("终端握手 id = %q, want %q (URI %s)", got.id, "session-42", got.requestURI)
	}
	if want := "Bearer agent-token"; got.authorization != want {
		t.Fatalf("终端握手 Authorization = %q, want %q", got.authorization, want)
	}
}

// URL 里除了不能有凭据，也不应该允许会话 id 拼出额外的查询参数；`id` 必须整体留在 id
// 参数的值里，同时中文域名的 ASCII 转换行为保持不变。
func TestTerminalEndpointEscapesSessionIDAndDropsCredential(t *testing.T) {
	saved := *flags
	defer func() { *flags = saved }()

	flags.Endpoint = "https://panel.example.com/"
	flags.Token = "secret-token"

	got := terminalEndpoint("a&token=evil", flags.Endpoint)
	if strings.Contains(got, "secret-token") || strings.Contains(got, "token=evil") {
		t.Fatalf("终端端点里不应出现任何凭据或注入的参数: %q", got)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("终端端点不是合法 URL: %v", err)
	}
	if parsed.Path != "/api/clients/terminal" {
		t.Fatalf("终端端点路径 = %q", parsed.Path)
	}
	if parsed.Query().Get("token") != "" {
		t.Fatalf("终端端点不应带 token 参数: %q", got)
	}
	if parsed.Query().Get("id") != "a&token=evil" {
		t.Fatalf("会话 id 未被完整保留: %q (endpoint %q)", parsed.Query().Get("id"), got)
	}

	idn := terminalEndpoint("session-1", "https://面板.example.com")
	if !strings.HasPrefix(idn, "wss://xn--") {
		t.Fatalf("中文域名终端端点转换异常: %q", idn)
	}
	if strings.Contains(idn, "token") {
		t.Fatalf("中文域名端点里不应出现凭据: %q", idn)
	}
}
