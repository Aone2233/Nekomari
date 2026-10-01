package security

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Aone2233/nekomari/internal/config"
)

func SplitAllowlist(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	entries := make([]string, 0, len(parts))
	for _, part := range parts {
		entry := strings.TrimSpace(part)
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func OriginMatchesHost(origin, host string) bool {
	_, originHost, ok := normalizeOrigin(origin)
	return ok && strings.EqualFold(originHost, host)
}

func OriginInAllowlist(origin, rawAllowlist string) bool {
	normalizedOrigin, originHost, ok := normalizeOrigin(origin)
	if !ok {
		return false
	}
	for _, entry := range SplitAllowlist(rawAllowlist) {
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "://") {
			normalizedEntry, _, ok := normalizeOrigin(entry)
			if ok && strings.EqualFold(normalizedEntry, normalizedOrigin) {
				return true
			}
			continue
		}
		if strings.EqualFold(entry, originHost) {
			return true
		}
	}
	return false
}

func IsAPIKeyRequest(r *http.Request) bool {
	apiKeyConfig, err := config.GetAs[string](config.ApiKeyKey, "")
	if err != nil || apiKeyConfig == "" || len(apiKeyConfig) < 12 {
		return false
	}
	return r.Header.Get("Authorization") == "Bearer "+apiKeyConfig
}

// HasBearerCredential 判断请求是否带着 Bearer 形式的凭据。
//
// 它**不校验**凭据是否有效——那是 IdentityMiddleware 的事。这里只回答一个问题：
// "这是不是一个自己设置请求头的非浏览器客户端"。WebSocket 的 Origin 校验需要这个答案。
//
// 为什么请求头与查询参数等价：浏览器**无法**在 WebSocket 握手上设置 Authorization 头
// （WebSocket API 不允许自定义头），所以"无 Origin 且带 Bearer 头"与 agent 原先用的
// "无 Origin 且带 ?token=" 一样，都是非浏览器客户端的可靠标志。少了这一条，
// 把凭据从查询串挪到请求头之后，agent 的握手会被跨站 WebSocket 劫持防护拦成 403
// （v1.6.6 的发布校验正是这样失败的：13 passed, 2 failed，agent 日志 `403 Forbidden`）。
func HasBearerCredential(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	return strings.HasPrefix(auth, "Bearer ") && len(auth) > len("Bearer ")
}

func IsAuthorizationPreflight(r *http.Request) bool {
	if r.Method != http.MethodOptions {
		return false
	}
	for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		if strings.EqualFold(strings.TrimSpace(header), "authorization") {
			return true
		}
	}
	return false
}

func normalizeOrigin(raw string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", false
	}
	host := strings.ToLower(parsed.Host)
	return strings.ToLower(parsed.Scheme) + "://" + host, host, true
}
