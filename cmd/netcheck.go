package cmd

// netcheck —— 探测目标的可达性与「协议适配」，直接回答：
//   这个目标该用 icmp 还是 tcp？该填什么端口？
//
// 为什么加这个命令（源自一次真实踩坑）：
//   某教育网地址 1.51.3.134 只响应 TCP:443、完全不响应 ICMP；而另一个
//   202.112.0.33 恰好相反（只答 ICMP）。当时把监测任务的 ICMP 目标从后者
//   换成前者，结果全机房 100% 丢包，排查了很久才发现是协议不匹配。
//   本命令把一个目标在「当前机器上」的 ICMP / TCP 可达性一次测清楚，
//   并直接给出该用哪种任务类型的建议，避免同类误判。
//
// 用法：
//   Komari netcheck 1.51.3.134
//   Komari netcheck example.com -p 22,80,443,8080
//   Komari netcheck 1.1.1.1 --json

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	ping "github.com/prometheus-community/pro-bing"
	"github.com/spf13/cobra"
)

type probeStat struct {
	Sent    int     `json:"sent"`
	Recv    int     `json:"recv"`
	LossPct float64 `json:"loss_pct"`
	MinMs   float64 `json:"min_ms"`
	AvgMs   float64 `json:"avg_ms"`
	MaxMs   float64 `json:"max_ms"`
	Err     string  `json:"err,omitempty"`
	Denied  bool    `json:"denied,omitempty"` // 因权限不足跳过，而非目标不可达
}

type tcpStat struct {
	Port   int    `json:"port"`
	Open   bool   `json:"open"`
	RttMs  int64  `json:"rtt_ms,omitempty"`
	Err    string `json:"err,omitempty"`
}

type report struct {
	Target   string   `json:"target"`
	Host     string   `json:"host"`
	IPsV4    []string `json:"ips_v4"`
	IPsV6    []string `json:"ips_v6"`
	DNSFail  string   `json:"dns_error,omitempty"`
	ICMP     probeStat `json:"icmp"`
	TCP      []tcpStat `json:"tcp"`
	Verdict  string   `json:"verdict"`
	Advice   string   `json:"advice"`
}

var (
	ncPorts   string
	ncCount   int
	ncTimeout time.Duration
	ncJSON    bool
	ncNoICMP  bool
)

var NetcheckCmd = &cobra.Command{
	Use:   "netcheck <host[:port]>",
	Short: "探测目标的 ICMP/TCP 可达性并给出监测任务类型建议",
	Long: `netcheck 会在当前机器上对一个目标做多协议探测：
  · DNS 解析（A / AAAA）
  · ICMP ping
  · TCP 连接（默认 22/80/443）
然后给出结论：该目标适合用 icmp、还是必须用 tcp（以及端口）。

典型用途：在配置 Komari 的延迟监测任务前，先确认目标答应哪种协议。`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		host := strings.TrimSpace(args[0])
		extraPort := 0
		// 允许把端口直接写在目标里，如 1.1.1.1:443
		if h, p, err := net.SplitHostPort(host); err == nil {
			host = h
			if v, err := strconv.Atoi(p); err == nil {
				extraPort = v
			}
		}

		rep := runNetcheck(host, extraPort)
		if ncJSON {
			b, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(b))
		} else {
			printReport(rep)
		}
	},
}

func init() {
	NetcheckCmd.Flags().StringVarP(&ncPorts, "ports", "p", "22,80,443", "要测试的 TCP 端口，逗号分隔")
	NetcheckCmd.Flags().IntVarP(&ncCount, "count", "n", 3, "ICMP 探测次数")
	NetcheckCmd.Flags().DurationVarP(&ncTimeout, "timeout", "w", 3*time.Second, "单次探测超时")
	NetcheckCmd.Flags().BoolVar(&ncJSON, "json", false, "以 JSON 输出")
	NetcheckCmd.Flags().BoolVar(&ncNoICMP, "no-icmp", false, "跳过 ICMP 探测")
	RootCmd.AddCommand(NetcheckCmd)
}

func runNetcheck(host string, extraPort int) report {
	rep := report{Target: host, Host: host}

	// ---- DNS ----
	ips, err := net.LookupIP(host)
	if err != nil {
		rep.DNSFail = err.Error()
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

	// ---- ICMP ----
	if !ncNoICMP && rep.DNSFail == "" {
		rep.ICMP = icmpProbe(host, ncCount, ncTimeout)
	}

	// ---- TCP ----
	portSet := map[int]bool{}
	for _, s := range strings.Split(ncPorts, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 65535 {
			portSet[v] = true
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
	if rep.DNSFail == "" {
		for _, p := range ports {
			rep.TCP = append(rep.TCP, tcpProbe(host, p, ncTimeout))
		}
	}

	// ---- 判定 ----
	rep.Verdict, rep.Advice = decide(rep)
	return rep
}

func icmpProbe(host string, count int, timeout time.Duration) probeStat {
	st := probeStat{Sent: count}
	pinger, err := ping.NewPinger(host)
	if err != nil {
		st.Err = err.Error()
		return st
	}
	pinger.Count = count
	pinger.Timeout = timeout * time.Duration(count+1)
	// 先尝试非特权（依赖系统允许），失败再尝试特权
	pinger.SetPrivileged(false)
	if err := pinger.Run(); err != nil {
		pinger2, err2 := ping.NewPinger(host)
		if err2 != nil {
			st.Err = err.Error()
			return st
		}
		pinger2.Count = count
		pinger2.Timeout = timeout * time.Duration(count+1)
		pinger2.SetPrivileged(true)
		if err3 := pinger2.Run(); err3 != nil {
			st.Err = err3.Error()
			// 权限类错误单独标记，避免误判成“目标不可达”
			low := strings.ToLower(err3.Error())
			if strings.Contains(low, "permission") || strings.Contains(low, "operation not permitted") ||
				strings.Contains(low, "privilege") || strings.Contains(low, "access is denied") {
				st.Denied = true
			}
			return st
		}
		applyStats(&st, pinger2)
		return st
	}
	applyStats(&st, pinger)
	return st
}

func applyStats(st *probeStat, p *ping.Pinger) {
	s := p.Statistics()
	st.Sent = s.PacketsSent
	st.Recv = s.PacketsRecv
	st.LossPct = s.PacketLoss
	st.MinMs = float64(s.MinRtt.Microseconds()) / 1000.0
	st.AvgMs = float64(s.AvgRtt.Microseconds()) / 1000.0
	st.MaxMs = float64(s.MaxRtt.Microseconds()) / 1000.0
}

func tcpProbe(host string, port int, timeout time.Duration) tcpStat {
	st := tcpStat{Port: port}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		st.Err = err.Error()
		return st
	}
	conn.Close()
	st.Open = true
	st.RttMs = time.Since(start).Milliseconds()
	if st.RttMs < 1 {
		st.RttMs = 1
	}
	return st
}

func decide(rep report) (string, string) {
	if rep.DNSFail != "" {
		return "DNS 解析失败", "目标域名无法解析，请检查域名拼写与 DNS。"
	}
	icmpOK := rep.ICMP.Sent > 0 && rep.ICMP.Recv > 0 && rep.ICMP.LossPct < 100
	icmpTested := !ncNoICMP && !rep.ICMP.Denied && rep.ICMP.Err == ""
	var openPorts []int
	for _, t := range rep.TCP {
		if t.Open {
			openPorts = append(openPorts, t.Port)
		}
	}

	switch {
	case icmpOK && len(openPorts) > 0:
		return "ICMP 与 TCP 均可达",
			"两种协议都能用。监测任务选 icmp 即可（无需指定端口）；" +
				"若需要探测具体服务端口，选 tcp 并在目标里写端口。"
	case icmpOK && len(openPorts) == 0:
		return "仅 ICMP 可达",
			"目标响应 ICMP，但测试的 TCP 端口均未开放。" +
				"监测任务请用 icmp，目标直接写主机名/IP（不要写端口）。"
	case !icmpTested && len(openPorts) > 0:
		// ICMP 没能测（权限不足或跳过），但 TCP 通 —— 不能断言“不响应 ICMP”
		return "TCP 可达（ICMP 未测试）",
			fmt.Sprintf("TCP:%s 可用。ICMP 本次未测出结果（权限不足或已跳过），"+
				"因此无法判断该目标是否响应 ICMP。若要下结论，请在 Linux 上用 root 重跑，"+
				"或给二进制 setcap cap_net_raw+ep。若确认它不响应 ICMP，"+
				"监测任务应选 tcp 并写成 host:port（例如 %s:%d）。",
				joinInts(openPorts), rep.Host, openPorts[0])
	case !icmpOK && len(openPorts) > 0:
		return "仅 TCP 可达（不响应 ICMP）",
			fmt.Sprintf("该目标【不响应 ICMP】，但 TCP:%s 可用。"+
				"监测任务必须选 tcp，目标写成 host:port（例如 %s:%d），否则会 100%% 超时。",
				joinInts(openPorts), rep.Host, openPorts[0])
	case rep.ICMP.Denied:
		return "ICMP 未能测试（权限不足）",
			"当前进程没有发送 ICMP 的权限，无法判断 ICMP 可达性。" +
				"请在 Linux 上用 root（或给二进制 setcap cap_net_raw+ep）后重试。"
	default:
		return "不可达",
			"ICMP 与测试的 TCP 端口都没有响应。请确认：目标是否在线、是否被防火墙拦截、" +
				"是否只有特定端口开放（用 -p 指定更多端口试试）。"
	}
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

func printReport(rep report) {
	w := os.Stdout
	fmt.Fprintf(w, "\nKomari netcheck —— 目标可达性与协议适配\n")
	fmt.Fprintf(w, "目标: %s\n", rep.Target)
	fmt.Fprintf(w, "%s\n", strings.Repeat("─", 62))

	fmt.Fprintf(w, "DNS\n")
	if rep.DNSFail != "" {
		fmt.Fprintf(w, "  ✗ 解析失败: %s\n", rep.DNSFail)
	} else {
		fmt.Fprintf(w, "  A    : %s\n", orNone(rep.IPsV4))
		fmt.Fprintf(w, "  AAAA : %s\n", orNone(rep.IPsV6))
	}

	if !ncNoICMP {
		fmt.Fprintf(w, "\nICMP 探测 (%d 次, 单次超时 %s)\n", ncCount, ncTimeout)
		switch {
		case rep.ICMP.Denied:
			fmt.Fprintf(w, "  ! 权限不足，未能测试（不是目标不可达）\n")
		case rep.ICMP.Err != "":
			fmt.Fprintf(w, "  ✗ 错误: %s\n", rep.ICMP.Err)
		case rep.ICMP.Recv > 0:
			fmt.Fprintf(w, "  ✓ 收 %d/%d  丢包 %.1f%%  延迟 min/avg/max = %.1f/%.1f/%.1f ms\n",
				rep.ICMP.Recv, rep.ICMP.Sent, rep.ICMP.LossPct,
				rep.ICMP.MinMs, rep.ICMP.AvgMs, rep.ICMP.MaxMs)
		default:
			fmt.Fprintf(w, "  ✗ 收 0/%d  丢包 100%%  —— 目标不响应 ICMP\n", rep.ICMP.Sent)
		}
	}

	fmt.Fprintf(w, "\nTCP 探测 (单次超时 %s)\n", ncTimeout)
	if len(rep.TCP) == 0 {
		fmt.Fprintf(w, "  (已跳过)\n")
	}
	for _, t := range rep.TCP {
		if t.Open {
			fmt.Fprintf(w, "  ✓ %-6d 开放   (握手 %d ms)\n", t.Port, t.RttMs)
		} else {
			fmt.Fprintf(w, "  ✗ %-6d 不可达\n", t.Port)
		}
	}

	fmt.Fprintf(w, "\n%s\n", strings.Repeat("─", 62))
	fmt.Fprintf(w, "判定: %s\n", rep.Verdict)
	fmt.Fprintf(w, "建议: %s\n\n", wrap(rep.Advice, 58, "      "))
}

func orNone(v []string) string {
	if len(v) == 0 {
		return "(无)"
	}
	return strings.Join(v, ", ")
}

func wrap(s string, width int, indent string) string {
	var b strings.Builder
	line := 0
	for _, r := range s {
		if line >= width {
			b.WriteString("\n")
			b.WriteString(indent)
			line = 0
		}
		b.WriteRune(r)
		line++
	}
	return b.String()
}
