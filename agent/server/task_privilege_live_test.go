package server

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestICMPWithoutCapabilitiesLive 证明「没有 CAP_NET_RAW 也能做 ICMP」这件事，
// 而且只在需要它的地方有意义：一台进程拿不到裸 socket 权限的主机。
//
//	NEKOMARI_LIVE_PROBE=1 go test -run TestICMPWithoutCapabilitiesLive -v ./server/
//
// 判定方式不依赖「跑测试的人是谁」，而是先确认裸 socket 在这台主机上真的不可用：
// 如果裸 socket 能用，这个测试就没有验证价值，直接跳过并说明原因。反过来，若裸
// socket 不可用而这台主机依然测出了 ICMP 延迟，那就只可能来自非特权 ping socket
// —— 也就是这条改动要保证的那条路。
func TestICMPWithoutCapabilitiesLive(t *testing.T) {
	if os.Getenv("NEKOMARI_LIVE_PROBE") != "1" {
		t.Skip("设置 NEKOMARI_LIVE_PROBE=1 才跑真实网络探测")
	}

	// 目标写死为一个稳定的公网地址，避免依赖 DNS（DNS 失败会把结论搅浑）。
	const target = "1.1.1.1"

	if _, replied, err := runICMP(target, 2*time.Second, true); err == nil && replied {
		t.Skip("这台主机上裸 socket 可用，验证不到非特权回退路径")
	}

	latency, family, err := icmpPing(target, 3*time.Second)
	if err != nil {
		// 权限问题要能被明确指出来，而不是混成一句「失败」。
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "permission") || strings.Contains(msg, "not permitted") {
			t.Fatalf("裸 socket 被拒，非特权回退也没成功：%v", err)
		}
		t.Skipf("这台主机连不出去，验证不到回退路径：%v", err)
	}
	if family != "ipv4" {
		t.Fatalf("family = %q, want ipv4", family)
	}
	if latency < 0 {
		t.Fatalf("latency = %d, want >= 0", latency)
	}
	t.Logf("非特权回退生效：ICMP 往返 %d ms（%s）", latency, family)
}
