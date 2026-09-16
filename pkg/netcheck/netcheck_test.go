package netcheck

import (
	"testing"
	"time"
)

// decide() 是纯函数，把所有判定分支都钉住。
// 这些分支对应真实世界的语义：目标答应哪种协议，决定监测任务该选什么类型。
func TestDecide(t *testing.T) {
	base := func() Report {
		return Report{
			Target: "example.com",
			Host:   "example.com",
			ICMP:   ICMPStat{Sent: 3, Recv: 3, LossPct: 0, MinMs: 1, AvgMs: 2, MaxMs: 3},
			TCP:    []TCPStat{{Port: 443, Open: true, RttMs: 5}},
		}
	}
	opt := Options{}

	cases := []struct {
		name    string
		mutate  func(*Report)
		want    Verdict
	}{
		{
			name:   "两者都通",
			mutate: func(r *Report) {},
			want:   VerdictBothOK,
		},
		{
			name: "仅 ICMP 可达",
			mutate: func(r *Report) {
				r.TCP = []TCPStat{{Port: 443, Open: false}, {Port: 80, Open: false}}
			},
			want: VerdictICMPOnly,
		},
		{
			// 这是当初导致 9 台机器全灭的那种目标：只答 TCP、不答 ICMP
			name: "仅 TCP 可达（不响应 ICMP）",
			mutate: func(r *Report) {
				r.ICMP = ICMPStat{Sent: 3, Recv: 0, LossPct: 100}
			},
			want: VerdictTCPOnly,
		},
		{
			// 关键：ICMP 因权限没测成，绝不能断言「目标不响应 ICMP」
			name: "ICMP 权限不足但 TCP 通",
			mutate: func(r *Report) {
				r.ICMP = ICMPStat{Sent: 3, Denied: true, Err: "permission denied"}
			},
			want: VerdictTCPUntested,
		},
		{
			name: "DNS 解析失败",
			mutate: func(r *Report) {
				r.DNSErr = "no such host"
				r.ICMP = ICMPStat{}
				r.TCP = nil
			},
			want: VerdictDNSFailure,
		},
		{
			name: "完全不可达",
			mutate: func(r *Report) {
				r.ICMP = ICMPStat{Sent: 3, Recv: 0, LossPct: 100}
				r.TCP = []TCPStat{{Port: 443, Open: false}}
			},
			want: VerdictUnreachable,
		},
		{
			name: "ICMP 权限不足且 TCP 也不通",
			mutate: func(r *Report) {
				r.ICMP = ICMPStat{Sent: 3, Denied: true, Err: "permission denied"}
				r.TCP = []TCPStat{{Port: 443, Open: false}}
			},
			want: VerdictICMPDenied,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base()
			tc.mutate(&r)
			got, summary, advice := decide(r, opt)
			if got != tc.want {
				t.Fatalf("verdict = %q, want %q", got, tc.want)
			}
			if summary == "" {
				t.Error("summary 不应为空")
			}
			if advice == "" {
				t.Error("advice 不应为空")
			}
		})
	}
}

// 建议文案里若含字面量 % 而未转义，Sprintf 会产生 %!x(MISSING)。
// 这里专门钉住这个曾经踩过的坑。
func TestDecideAdviceHasNoFormatVerbs(t *testing.T) {
	r := Report{
		Target: "1.51.3.134",
		Host:   "1.51.3.134",
		ICMP:   ICMPStat{Sent: 3, Recv: 0, LossPct: 100},
		TCP:    []TCPStat{{Port: 443, Open: true, RttMs: 2}},
	}
	_, _, advice := decide(r, Options{})
	for _, bad := range []string{"%!", "(MISSING)"} {
		if contains(advice, bad) {
			t.Fatalf("advice 含未转义的格式串残留 %q: %s", bad, advice)
		}
	}
	// 100% 应当正常显示
	if !contains(advice, "100%") {
		t.Fatalf("advice 应包含 100%% 字面量: %s", advice)
	}
}

// 端到端：对回环地址做一次真实探测（TCP 必须通；ICMP 视权限而定）。
func TestRunLoopback(t *testing.T) {
	rep := Run("127.0.0.1", Options{
		Ports:   []int{1, 9}, // 几乎不可能开放，用来确认「关闭」分支
		Count:   1,
		Timeout: 500 * time.Millisecond,
	})
	if rep.DNSErr != "" {
		t.Fatalf("回环地址不应解析失败: %s", rep.DNSErr)
	}
	if len(rep.IPsV4) == 0 {
		t.Error("应解析出 IPv4 地址")
	}
	if len(rep.TCP) != 2 {
		t.Fatalf("应返回 2 个端口结果，实际 %d", len(rep.TCP))
	}
	// 回环上这两个端口基本必然关闭 → 判定不应是 both_ok
	if rep.Verdict == VerdictBothOK {
		t.Error("回环的 1/9 端口不应被判为都可达")
	}
}

// 端口写在目标里（host:port）时，应被并入待测端口列表。
func TestRunPortInTarget(t *testing.T) {
	rep := Run("127.0.0.1:8080", Options{
		Ports:   []int{9},
		Count:   1,
		Timeout: 300 * time.Millisecond,
		SkipICMP: true,
	})
	found := false
	for _, p := range rep.TCP {
		if p.Port == 8080 {
			found = true
		}
	}
	if !found {
		t.Fatalf("目标里的端口 8080 应被并入待测列表，实际: %+v", rep.TCP)
	}
}

// DNS 失败分支的端到端验证（.invalid 是保留后缀，必然解析失败）。
func TestRunBadDNS(t *testing.T) {
	rep := Run("definitely-not-a-real-host.invalid", Options{Count: 1, Timeout: 300 * time.Millisecond})
	if rep.DNSErr == "" {
		t.Fatal("不存在的域名应产生 DNS 错误")
	}
	if rep.Verdict != VerdictDNSFailure {
		t.Fatalf("verdict = %q, want %q", rep.Verdict, VerdictDNSFailure)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}