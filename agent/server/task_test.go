package server

import (
	"testing"
	"time"
)

// TestAddressFamilyOf 覆盖族的判定本身：纯函数，不依赖网络，所以它是这部分
// 最稳的一条测试。上报的族必须来自【实际拨向的那个 IP】，判错会让两条路径
// 又被合成一条曲线 —— 也就是这个改动要解决的问题本身。
func TestAddressFamilyOf(t *testing.T) {
	cases := []struct {
		ip   string
		want string
	}{
		{"117.185.125.154", "ipv4"},
		{"0.0.0.0", "ipv4"},
		{"2409:8c1e:8f80:2:6a::", "ipv6"},
		{"::1", "ipv6"},
		{"2001:db8::1", "ipv6"},
		// IPv4-mapped 地址实际是 IPv4 目标，必须按 IPv4 上报，
		// 否则同一台双栈主机会被拆成两条并不存在的路径。
		{"::ffff:1.51.3.134", "ipv4"},
		// 无法判断的情况一律返回空：不写标签，保持改动前的行为。
		{"", ""},
		{"v6-sh-cm.oojj.de", ""},
		{"not an ip", ""},
	}
	for _, tc := range cases {
		if got := addressFamilyOf(tc.ip); got != tc.want {
			t.Errorf("addressFamilyOf(%q) = %q, want %q", tc.ip, got, tc.want)
		}
	}
}

// testTargets 里的域名是按族命名的（v4-sh-cm / v6-sh-cm），字面量则天然确定，
// 所以「测到的族」可以直接断言。域名只在探测成功时断言，避免把解析器差异
// 变成测试失败。
var testTargets = []struct {
	target     string
	wantFamily string
	literal    bool
}{
	{"v6-sh-cm.oojj.de", "ipv6", false},
	{"2409:8c1e:8f80:2:6a::", "ipv6", true},
	{"[2409:8c1e:8f80:2:6a::]", "ipv6", true},
	{"[2409:8c1e:8f80:2:6a::]:80", "ipv6", true},
	{"v4-sh-cm.oojj.de", "ipv4", false},
	{"117.185.125.154", "ipv4", true},
	{"117.185.125.154:80", "ipv4", true},
}

// checkFamily 断言上报的族。字面量目标必须始终命中；域名只在探测成功时检查。
func checkFamily(t *testing.T, kind, target, got, want string, literal bool, pingErr error) {
	t.Helper()
	if literal && got != want {
		t.Errorf("%s ping %s: reported family %q, want %q", kind, target, got, want)
		return
	}
	if !literal && pingErr == nil && got != want {
		t.Errorf("%s ping %s: reported family %q, want %q", kind, target, got, want)
	}
}

func TestICMPPing(t *testing.T) {
	timeout := 3 * time.Second
	for _, tt := range testTargets {
		t.Run(tt.target, func(t *testing.T) {
			latency, family, err := icmpPing(tt.target, timeout)
			if latency < -1 {
				t.Errorf("ICMP ping %s: invalid latency %d", tt.target, latency)
			}
			if err != nil {
				t.Errorf("ICMP ping %s error: %v", tt.target, err)
			}
			checkFamily(t, "ICMP", tt.target, family, tt.wantFamily, tt.literal, err)
		})
	}
}

func TestTCPPing(t *testing.T) {
	timeout := 3 * time.Second
	for _, tt := range testTargets {
		t.Run(tt.target, func(t *testing.T) {
			latency, family, err := tcpPing(tt.target, timeout)
			if latency < -1 {
				t.Errorf("TCP ping %s: invalid latency %d", tt.target, latency)
			}
			if err != nil {
				t.Errorf("TCP ping %s error: %v", tt.target, err)
			}
			checkFamily(t, "TCP", tt.target, family, tt.wantFamily, tt.literal, err)
		})
	}
}

func TestHTTPPing(t *testing.T) {
	timeout := 3 * time.Second
	for _, tt := range testTargets {
		t.Run(tt.target, func(t *testing.T) {
			latency, family, err := httpPing(tt.target, timeout)
			if latency < -1 {
				t.Errorf("HTTP ping %s: invalid latency %d", tt.target, latency)
			}
			if err != nil {
				t.Errorf("HTTP ping %s error: %v", tt.target, err)
			}
			checkFamily(t, "HTTP", tt.target, family, tt.wantFamily, tt.literal, err)
		})
	}
}
