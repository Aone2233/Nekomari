package cmd

// netcheck —— CLI 入口：探测目标的可达性与「协议适配」。
// 探测逻辑在 pkg/netcheck，与面板里的 admin:netcheck 共用同一份实现。

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Aone2233/nekomari/pkg/netcheck"
	"github.com/spf13/cobra"
)

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

典型用途：在配置延迟监测任务前，先确认目标答应哪种协议。
注意：结论反映的是【当前这台机器】的可达性。`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var ports []int
		for _, s := range strings.Split(ncPorts, ",") {
			if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v > 0 && v <= 65535 {
				ports = append(ports, v)
			}
		}
		rep := netcheck.Run(args[0], netcheck.Options{
			Ports:    ports,
			Count:    ncCount,
			Timeout:  ncTimeout,
			SkipICMP: ncNoICMP,
		})
		if ncJSON {
			b, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(b))
			return
		}
		printReport(rep)
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

func printReport(rep netcheck.Report) {
	w := os.Stdout
	line := strings.Repeat("─", 62)
	fmt.Fprintf(w, "\nNekomari netcheck —— 目标可达性与协议适配\n")
	fmt.Fprintf(w, "目标: %s\n%s\n", rep.Target, line)

	fmt.Fprintf(w, "DNS\n")
	if rep.DNSErr != "" {
		fmt.Fprintf(w, "  ✗ 解析失败: %s\n", rep.DNSErr)
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

	fmt.Fprintf(w, "\n%s\n判定: %s\n建议: %s\n\n", line, rep.Summary, wrap(rep.Advice, 58, "      "))
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