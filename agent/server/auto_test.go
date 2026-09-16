package server

import (
	"errors"
	"os"
	"testing"
)

// decideAuto 是纯函数，把所有决策分支钉住。
// 它决定 `auto` 类型最终用哪种协议去测目标 —— 选错的后果是恒定 100% 丢包。
func TestDecideAuto(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		result     autoProbeResult
		wantKind   string
		wantTarget string
	}{
		{
			// 用户显式写了端口，就尊重这个意图，不再猜测
			name:       "目标自带端口 -> 直接 tcp",
			target:     "example.com:8443",
			result:     autoProbeResult{},
			wantKind:   "tcp",
			wantTarget: "example.com:8443",
		},
		{
			name:       "ICMP 通 -> icmp",
			target:     "202.112.0.33",
			result:     autoProbeResult{icmpOK: true},
			wantKind:   "icmp",
			wantTarget: "202.112.0.33",
		},
		{
			// 这是当初坑了整个机房的那种目标：只答 TCP
			name:       "ICMP 不通但 443 可用 -> tcp:443",
			target:     "1.51.3.134",
			result:     autoProbeResult{openPort: "443"},
			wantKind:   "tcp",
			wantTarget: "1.51.3.134:443",
		},
		{
			name:       "ICMP 不通但 80 可用 -> tcp:80",
			target:     "legacy.example.com",
			result:     autoProbeResult{openPort: "80"},
			wantKind:   "tcp",
			wantTarget: "legacy.example.com:80",
		},
		{
			// 本地没有 ICMP 权限时 icmp 在这台机器上根本用不了，
			// 所以若有可用 TCP 端口就退到 tcp
			name:       "ICMP 权限不足但有可用端口 -> tcp",
			target:     "10.0.0.1",
			result:     autoProbeResult{icmpDenied: true, openPort: "443"},
			wantKind:   "tcp",
			wantTarget: "10.0.0.1:443",
		},
		{
			// 都不通时保持 icmp，让上报如实反映失败，而不是假装成功
			name:       "全都不通 -> 保持 icmp（如实失败）",
			target:     "unreachable.example.com",
			result:     autoProbeResult{},
			wantKind:   "icmp",
			wantTarget: "unreachable.example.com",
		},
		{
			name:       "IPv6 字面量 + 端口",
			target:     "[2001:db8::1]:443",
			result:     autoProbeResult{icmpOK: true},
			wantKind:   "tcp",
			wantTarget: "[2001:db8::1]:443",
		},
		{
			name:       "IPv6 字面量无端口，ICMP 通",
			target:     "2001:db8::1",
			result:     autoProbeResult{icmpOK: true},
			wantKind:   "icmp",
			wantTarget: "2001:db8::1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, target := decideAuto(tc.target, tc.result)
			if kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", kind, tc.wantKind)
			}
			if target != tc.wantTarget {
				t.Errorf("target = %q, want %q", target, tc.wantTarget)
			}
		})
	}
}

// 权限类错误必须与「目标不可达」区分开 —— 混淆会得出相反结论。
func TestIsPermissionErr(t *testing.T) {
	yes := []string{
		"error setting traffic class: setsockopt: An attempt was made to access a socket in a way forbidden by its access permissions.",
		"operation not permitted",
		"permission denied",
		"socket: permission denied",
		"Access is denied.",
	}
	for _, m := range yes {
		if !isPermissionErr(errors.New(m)) {
			t.Errorf("应判为权限错误: %q", m)
		}
	}
	no := []string{
		"no packets received",
		"i/o timeout",
		"no such host",
		"network is unreachable",
	}
	for _, m := range no {
		if isPermissionErr(errors.New(m)) {
			t.Errorf("不应判为权限错误: %q", m)
		}
	}
	if isPermissionErr(nil) {
		t.Error("nil 不应判为权限错误")
	}
}

// auto 解析结果要进缓存：同一目标短时间内重复调用不应反复探测。
func TestResolveAutoCaches(t *testing.T) {
	const target = "cache-test.invalid"
	kind1, tgt1 := ResolveAuto(target)
	kind2, tgt2 := ResolveAuto(target)
	if kind1 != kind2 || tgt1 != tgt2 {
		t.Fatalf("缓存命中前后结果不一致: (%s,%s) vs (%s,%s)", kind1, tgt1, kind2, tgt2)
	}
	// 解析一个必然失败的目标：应保持 icmp 且目标原样
	if kind1 != "icmp" || tgt1 != target {
		t.Fatalf("不可达目标应保持 icmp/原目标，实际 (%s,%s)", kind1, tgt1)
	}
}
// 真实网络验证：对两个互补目标跑完整的 probeAutoProtocol（含真实 ICMP/TCP）。
// 默认跳过（网络测试不应拖慢常规 go test），用环境变量显式开启：
//
//	NEKOMARI_LIVE_PROBE=1 go test -run TestProbeAutoProtocolLive -v ./server/
//
// 注意：要得到 ICMP 的确定结论，需以 root 运行（否则 ICMP 失败会走 TCP 分支）。
func TestProbeAutoProtocolLive(t *testing.T) {
	if os.Getenv("NEKOMARI_LIVE_PROBE") != "1" {
		t.Skip("设置 NEKOMARI_LIVE_PROBE=1 才跑真实网络探测")
	}
	cases := []struct {
		target   string
		wantKind string
		why      string
	}{
		{"202.112.0.33", "icmp", "该地址只答 ICMP、TCP 全关"},
		{"1.51.3.134", "tcp", "该地址不答 ICMP、但 TCP:443/80 开放"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			kind, target := probeAutoProtocol(tc.target)
			t.Logf("%s -> kind=%s target=%s  (%s)", tc.target, kind, target, tc.why)
			if kind != tc.wantKind {
				t.Errorf("kind = %q, want %q (%s)", kind, tc.wantKind, tc.why)
			}
			if kind == "tcp" && target == tc.target {
				t.Errorf("解析为 tcp 时应补上端口，实际仍是 %q", target)
			}
		})
	}
}
