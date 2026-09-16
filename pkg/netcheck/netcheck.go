// Package netcheck 探测一个目标的可达性与「协议适配」。
//
// 它回答的问题是：这个目标该用 icmp 还是 tcp？该填什么端口？
//
// 为什么存在（源自一次真实事故）：某教育网地址只响应 TCP:443、完全不响应 ICMP，
// 而另一个地址恰好相反。把监测任务的 ICMP 目标换成前者后，全部机器 100% 丢包，
// 排查很久才发现是协议不匹配。监控任务的探测类型（icmp/tcp/http）是每个任务一个，
// 选错了就必然全灭 —— 所以配置前应当先探测。
//
// 同一份逻辑同时驱动：
//   - CLI：  nekomari netcheck <host>
//   - 面板： admin:netcheck  （新建/编辑监测任务时的目标预检）
package netcheck

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	ping "github.com/prometheus-community/pro-bing"
)

// Verdict 是机器可读的判定结果，前端可据此决定提示样式。
type Verdict string

const (
	VerdictBothOK       Verdict = "both_ok"        // ICMP 与 TCP 都可达
	VerdictICMPOnly     Verdict = "icmp_only"      // 仅 ICMP 可达
	VerdictTCPOnly      Verdict = "tcp_only"       // 仅 TCP 可达（不响应 ICMP）
	VerdictTCPUntested  Verdict = "tcp_ok_icmp_untested" // TCP 通，但 ICMP 没测成（权限不足）
	VerdictICMPDenied   Verdict = "icmp_denied"    // ICMP 因权限失败
	VerdictUnreachable  Verdict = "unreachable"    // 都不可达
	VerdictDNSFailure   Verdict = "dns_failure"    // 域名解析失败
)

// ICMPStat 是一次 ICMP 探测的统计。
type ICMPStat struct {
	Sent    int     `json:"sent"`
	Recv    int     `json:"recv"`
	LossPct float64 `json:"loss_pct"`
	MinMs   float64 `json:"min_ms"`
	AvgMs   float64 `json:"avg_ms"`
	MaxMs   float64 `json:"max_ms"`
	Err     string  `json:"err,omitempty"`
	// Denied 表示「因为没有权限而没测成」，**不是**「目标不可达」。
	// 这个区分很关键：非特权环境下的 ICMP 失败会得出完全相反的结论。
	Denied bool `json:"denied,omitempty"`
}

// TCPStat 是单个端口的 TCP 握手结果。
type TCPStat struct {
	Port  int    `json:"port"`
	Open  bool   `json:"open"`
	RttMs int64  `json:"rtt_ms,omitempty"`
	Err   string `json:"err,omitempty"`
}

// Report 是一次完整探测的结果。
type Report struct {
	Target  string    `json:"target"`
	Host    string    `json:"host"`
	IPsV4   []string  `json:"ips_v4"`
	IPsV6   []string  `json:"ips_v6"`
	DNSErr  string    `json:"dns_error,omitempty"`
	ICMP    ICMPStat  `json:"icmp"`
	TCP     []TCPStat `json:"tcp"`
	Verdict Verdict   `json:"verdict"`
	Summary string    `json:"summary"` // 一句话判定（中文）
	Advice  string    `json:"advice"`  // 给配置者的建议
}

// Options 控制探测行为。
type Options struct {
	Ports    []int         // 要测的 TCP 端口；为空则用 DefaultPorts
	Count    int           // ICMP 次数；<=0 则用 3
	Timeout  time.Duration // 单次超时；<=0 则用 3s
	SkipICMP bool          // 跳过 ICMP（例如已知环境无权限）
}

// DefaultPorts 是未指定端口时测试的常用端口。
var DefaultPorts = []int{22, 80, 443}

func (o Options) withDefaults() Options {
	if len(o.Ports) == 0 {
		o.Ports = append([]int(nil), DefaultPorts...)
	}
	if o.Count <= 0 {
		o.Count = 3
	}
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Second
	}
	return o
}

// Run 对 host 执行一次探测。host 可以是域名、IPv4、IPv6（可带 [..] 包裹）。
// 注意：探测在**调用方所在机器**上进行，因此结论反映的是该机器的可达性。
func Run(host string, opt Options) Report {
	opt = opt.withDefaults()
	host = strings.TrimSpace(host)
	// 允许目标写成 host:port —— 端口会被并入待测端口列表
	extraPort := 0
	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		if v, err := strconv.Atoi(p); err == nil {
			extraPort = v
		}
	}
	host = strings.Trim(host, "[]")

	rep := Report{Target: host, Host: host}

	// DNS
	ips, err := net.LookupIP(host)
	if err != nil {
		rep.DNSErr = err.Error()
	} else {
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				rep.IPsV4 = append(rep.IPsV4, v4.String())
			} else {
				rep.IPsV6 = append(rep.IPsV6, ip.String())
			}
		}
		sort.Strings(rep.IPsV4)
		sort.Strings(rep.IPsV6)
	}

	// ICMP
	if !opt.SkipICMP && rep.DNSErr == "" {
		rep.ICMP = probeICMP(host, opt.Count, opt.Timeout)
	}

	// TCP
	if rep.DNSErr == "" {
		portSet := map[int]bool{}
		for _, p := range opt.Ports {
			if p > 0 && p <= 65535 {
				portSet[p] = true
			}
		}
		if extraPort > 0 {
			portSet[extraPort] = true
		}
		ports := make([]int, 0, len(portSet))
		for p := range portSet {
			ports = append(ports, p)
		}
		sort.Ints(ports)
		for _, p := range ports {
			rep.TCP = append(rep.TCP, probeTCP(host, p, opt.Timeout))
		}
	}

	rep.Verdict, rep.Summary, rep.Advice = decide(rep, opt)
	return rep
}

func probeICMP(host string, count int, timeout time.Duration) ICMPStat {
	st := ICMPStat{Sent: count}
	// 先试非特权（Linux 上放开 ping_group_range 时可用），失败再试特权。
	p, err := ping.NewPinger(host)
	if err == nil {
		p.Count = count
		p.Timeout = timeout * time.Duration(count+1)
		p.SetPrivileged(false)
		if err = p.Run(); err == nil {
			fill(&st, p)
			return st
		}
	}
	p2, err2 := ping.NewPinger(host)
	if err2 != nil {
		st.Err = err2.Error()
		return st
	}
	p2.Count = count
	p2.Timeout = timeout * time.Duration(count+1)
	p2.SetPrivileged(true)
	if err3 := p2.Run(); err3 != nil {
		st.Err = err3.Error()
		if isPermissionError(err3) {
			// 没测成 ≠ 测得 0 丢包。把 Sent 归零，避免出现
			// "sent=3 recv=0 loss=0" 这种自相矛盾的数据。
			st.Denied = true
			st.Sent = 0
			st.Recv = 0
			st.LossPct = 0
		}
		return st
	}
	fill(&st, p2)
	return st
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	for _, s := range []string{"permission", "operation not permitted", "privilege", "access is denied", "not permitted"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

func fill(st *ICMPStat, p *ping.Pinger) {
	s := p.Statistics()
	st.Sent = s.PacketsSent
	st.Recv = s.PacketsRecv
	st.LossPct = s.PacketLoss
	st.MinMs = float64(s.MinRtt.Microseconds()) / 1000.0
	st.AvgMs = float64(s.AvgRtt.Microseconds()) / 1000.0
	st.MaxMs = float64(s.MaxRtt.Microseconds()) / 1000.0
}

// probeTCP 对 host:port 做一次 TCP 握手，成功则记录真实握手耗时。
func probeTCP(host string, port int, timeout time.Duration) TCPStat {
	st := TCPStat{Port: port}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		st.Err = err.Error()
		return st
	}
	_ = conn.Close()
	st.Open = true
	st.RttMs = time.Since(start).Milliseconds()
	if st.RttMs < 1 {
		st.RttMs = 1
	}
	return st
}

func decide(rep Report, opt Options) (Verdict, string, string) {
	if rep.DNSErr != "" {
		return VerdictDNSFailure, "DNS 解析失败",
			"目标域名无法解析，请检查域名拼写与 DNS 设置。"
	}
	icmpOK := rep.ICMP.Sent > 0 && rep.ICMP.Recv > 0 && rep.ICMP.LossPct < 100
	icmpTested := !opt.SkipICMP && !rep.ICMP.Denied && rep.ICMP.Err == ""

	var openPorts []int
	for _, t := range rep.TCP {
		if t.Open {
			openPorts = append(openPorts, t.Port)
		}
	}
	portList := joinInts(openPorts)

	switch {
	case icmpOK && len(openPorts) > 0:
		return VerdictBothOK, "ICMP 与 TCP 均可达",
			"两种协议都能用。监测任务选 icmp 即可（无需指定端口）；若需要探测具体服务端口，选 tcp 并在目标里写端口。"
	case icmpOK && len(openPorts) == 0:
		return VerdictICMPOnly, "仅 ICMP 可达",
			"目标响应 ICMP，但测试的 TCP 端口均未开放。监测任务请用 icmp，目标直接写主机名/IP（不要写端口）。"
	case !icmpTested && len(openPorts) > 0:
		return VerdictTCPUntested, "TCP 可达（ICMP 未测试）",
			fmt.Sprintf("TCP:%s 可用。ICMP 本次未测出结果（权限不足或已跳过），因此无法判断该目标是否响应 ICMP。"+
				"若要下结论，请在 Linux 上用 root 重跑，或给二进制 setcap cap_net_raw+ep。"+
				"若确认它不响应 ICMP，监测任务应选 tcp 并写成 host:port（例如 %s:%d）。",
				portList, rep.Host, openPorts[0])
	case !icmpOK && len(openPorts) > 0:
		return VerdictTCPOnly, "仅 TCP 可达（不响应 ICMP）",
			fmt.Sprintf("该目标【不响应 ICMP】，但 TCP:%s 可用。监测任务必须选 tcp，目标写成 host:port（例如 %s:%d），"+
				"否则会 100%% 超时。", portList, rep.Host, openPorts[0])
	case rep.ICMP.Denied:
		return VerdictICMPDenied, "ICMP 未能测试（权限不足）",
			"当前进程没有发送 ICMP 的权限，无法判断 ICMP 可达性。请在 Linux 上用 root（或给二进制 setcap cap_net_raw+ep）后重试。"
	default:
		return VerdictUnreachable, "不可达",
			"ICMP 与测试的 TCP 端口都没有响应。请确认：目标是否在线、是否被防火墙拦截、是否只有特定端口开放（试试更多端口）。"
	}
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}