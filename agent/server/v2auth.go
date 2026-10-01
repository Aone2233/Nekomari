package server

import (
	"net/http"
	"strings"
)

// v2RPCEndpoint 返回 v2 RPC 端点，**不带任何凭据**。
//
// 为什么凭据不再放进查询串：URL 的查询串会被 nginx / Cloudflare 的访问日志完整记录，
// 也会在请求出错时被原样打进各节点自己的 journal。现场实测（2026-10-01）：面板的
// 14 天 nginx 日志里留下了 47 个不同的 agent token，其中 **9 个是当天仍然有效的
// 现役凭据**——即"把 token 从命令行挪进 0600 凭据文件"那轮加固，被日志这一侧抵消了。
//
// 服务端 `web/api/Auth.go` 的 `extractClientToken` 同时保留 `?token=` 与
// `?Authorization=` 两个查询参数分支，所以旧版 agent（现场还有 9 台跑 v1.6.2）
// 不需要同时升级也能继续认证。
func v2RPCEndpoint() string {
	return strings.TrimSuffix(flags.Endpoint, "/") + "/api/clients/v2/rpc"
}

// setV2Auth 给一个 HTTP 请求挂上 agent 凭据。
//
// 服务端按 "Bearer " 前缀解析 Authorization 头（见 extractClientToken）；
// 查询参数分支仍排在最前，因此新旧 agent 可以混跑，不需要同时切换。
func setV2Auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+flags.Token)
}

// v2RPCAuthHeaders 返回 WebSocket 握手需要的认证头。
//
// 与 setV2Auth 同一份凭据来源，只是 WebSocket 的 Dial 接受一个 http.Header。
func v2RPCAuthHeaders() http.Header {
	return http.Header{"Authorization": {"Bearer " + flags.Token}}
}
