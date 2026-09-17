package ipinfo

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// nonPublicPrefixes 是 netip 的「全局单播」判定仍会放行、但不该拿去问上游的网段。
// netip.Addr.IsGlobalUnicast 只排除未指定/环回/组播/链路本地，像 CGNAT、
// 文档网段、基准测试网段这些都算「全局单播」，因此这里显式列出。
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // 本网络
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT 运营商级 NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF 协议保留
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // 基准测试
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // 保留（含 255.255.255.255）
	netip.MustParsePrefix("100::/64"),        // IPv6 丢弃前缀
	netip.MustParsePrefix("2001:db8::/32"),   // IPv6 文档网段
}

// isPublicIP 判断地址是否为可对外查询的公网地址。
//
// 私有、环回、链路本地、组播、未指定、CGNAT、文档网段等一律返回 false ——
// 这些地址在上游查不到有效数据，直接拒绝比拿一份空数据糊弄主题更清楚。
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	// IPv4-mapped IPv6（::ffff:1.2.3.4）按 IPv4 处理。
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() {
		return false
	}
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsUnspecified() ||
		addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// parsePublicIP 解析并校验查询目标，返回规范化后的地址与其地址族（4 或 6）。
// 任何校验失败都返回错误，调用方统一映射为 HTTP 400。
func parsePublicIP(raw string) (net.IP, int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, 0, fmt.Errorf("query parameter \"ip\" is required")
	}
	// 容忍带方括号的 IPv6 字面量（http://host/[::1] 那种写法）。
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	// 带端口或 zone 的写法不是合法 IP，net.ParseIP 会直接拒绝。
	ip := net.ParseIP(value)
	if ip == nil {
		return nil, 0, fmt.Errorf("invalid ip address %q", raw)
	}
	if !isPublicIP(ip) {
		return nil, 0, fmt.Errorf("ip address %q is not a public address; private, loopback, link-local and reserved ranges are not supported", ip.String())
	}
	family := 6
	if ip.To4() != nil {
		family = 4
	}
	return ip, family, nil
}
