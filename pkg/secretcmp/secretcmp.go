// Package secretcmp 比较密钥而不泄露比较耗时。
//
// 凭据比较必须是恒定时间的：`==` 在第一个不同的字节处就返回，于是调用方可以
// 逐字节地把 API key、自动发现 key 或临时分享令牌猜出来。这里的三处调用
// （web/api/Auth.go 的临时分享令牌与 API key、web/security/origin.go 的
// Bearer 判定、web/api/client/autoDiscovery.go 的自动发现 key）都用它，
// 而不是各自写一个 `==`。
//
// 长度不同时直接返回 false。长度不是秘密：这些值都由服务端生成、长度固定，
// 而 crypto/subtle 本身也要求两个切片等长才能做恒定时间比较。
package secretcmp

import "crypto/subtle"

// Equal 恒定时间比较两个字符串。
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
