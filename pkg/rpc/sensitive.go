package rpc

// sensitive.go
// 敏感操作登记表。被标记的方法（如 admin:exec）在通过角色鉴权后，仍需额外的
// 敏感操作二次验证（sensitive 2FA）。登记与传输无关，由 RPC 边界统一判定，
// 确保所有调用入口行为一致。

import (
	"sort"
	"sync"
)

var (
	muSensitive      sync.RWMutex
	sensitiveMethods = map[string]bool{}
)

// MarkSensitive 标记某方法为敏感操作。method 为完整方法名（如 "admin:exec"）。
// 重复标记是幂等的。供方法注册处声明。
func MarkSensitive(method string) {
	muSensitive.Lock()
	defer muSensitive.Unlock()
	sensitiveMethods[method] = true
}

// IsSensitive 判断方法是否被标记为敏感操作。
func IsSensitive(method string) bool {
	muSensitive.RLock()
	defer muSensitive.RUnlock()
	return sensitiveMethods[method]
}

// SensitiveMethods 返回当前所有被标记为敏感操作的方法名（已排序）。
//
// 存在的理由是可测：敏感名单是安全边界，它的每一次【增删】都该被看见。用例断言
// 它等于一份经过审阅的名单，于是「谁把某个方法从敏感名单里拿掉了」会立刻失败，
// 而新加的敏感方法也必须显式写进那份名单。
func SensitiveMethods() []string {
	muSensitive.RLock()
	defer muSensitive.RUnlock()
	out := make([]string, 0, len(sensitiveMethods))
	for method := range sensitiveMethods {
		out = append(out, method)
	}
	sort.Strings(out)
	return out
}
