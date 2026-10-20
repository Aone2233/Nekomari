package netstatic

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

/*
速率感知的增量上限

判断"一个增量是否物理上可信"时，原先对所有网卡一律假设 1 Gbps。这对写这段代码时的 VPS 成立，
对 10G/25G 节点则完全错误 —— 每个繁忙区间都会超过假定上限而被丢弃，账本里就少了真实流量，而
丢弃是静默的（只有一条被限流的日志）。现在优先读取内核在 /sys/class/net/<iface>/speed 报告的
速率；读不到时（很多网桥、虚拟设备不报告速率，返回 -1）仍然沿用 1 Gbps 的假定值与其突发余量，
也就是"不知道速率时保持原有的宽松，知道速率时按物理上限判断"。
*/

const (
	// sysClassNetPath 是内核暴露每个网卡属性的目录。
	sysClassNetPath = "/sys/class/net"

	// assumedLinkBytesPerSecond 是读不到真实速率时使用的假定上限（1 Gbps，单位字节/秒）。
	assumedLinkBytesPerSecond = 1e9 / 8

	// assumedLinkHeadroom 只作用于"假定速率"：那是一个猜测，而便宜 VPS 会突发到标称值以上，
	// 网桥/聚合口也可能把多条链路合起来而不报告总和，所以必须留余量。30 秒 × 1 Gbps × 3 约
	// 11.25 GB，仍然足以拒绝现场那次 59.5 GB 的重启幻影增量。
	assumedLinkHeadroom = 3.0

	// measuredLinkHeadroom 作用于内核真实报告的速率。已知链路速率时没有理由再给三倍宽容：
	// 超过"速率 × 流逝时间"的增量不是突发，而是计数错误（或者计数器被换掉了），收下它只会污染
	// 账本。所以 10 Gbps 节点上 7 GiB/min 通过，而同样的增量在 1 Gbps 链路上会被拒绝。
	measuredLinkHeadroom = 1.0
)

// linkSpeedMbps 是增量上限用来询问网卡协商速率的 seam。测试替换它来固定 1 Gbps / 10 Gbps，
// 或模拟没有 speed 属性的设备。
var linkSpeedMbps = readLinkSpeedMbps

// readLinkSpeedMbps 读取 /sys/class/net/<iface>/speed，内核以 Mbps 报告。
//
// 该属性在虚拟设备上通常不存在或返回 -1（"没有固定速率"），两种情况都返回错误，由调用方回退到
// 假定速率。name 来自内核自己的网卡列表，这里仍然拒绝路径分隔符：路径是拼接出来的，不该把
// 一个畸形名字变成对任意路径的读取。
func readLinkSpeedMbps(iface string) (int, error) {
	if iface == "" || iface == "." || iface == ".." || strings.ContainsAny(iface, "/\\") {
		return 0, fmt.Errorf("invalid interface name %q", iface)
	}
	path := filepath.Join(sysClassNetPath, iface, "speed")
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	mbps, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if mbps <= 0 {
		return 0, fmt.Errorf("%s reports %d Mbps, which is not a usable rate", path, mbps)
	}
	return mbps, nil
}

// linkRate 是一张网卡判增量时使用的速率上限，连同它的来源，好让拒绝日志说清是哪种情况。
type linkRate struct {
	// bytesPerSecond 是标称速率（字节/秒）。
	bytesPerSecond float64
	// headroom 是叠加在标称速率上的余量。
	headroom float64
	// mbps 是真实速率（Mbps）；0 表示速率是假定的。
	mbps int
	// source 说明这个数字从哪来，直接进拒绝日志。
	source string
}

// linkRateFor 解析一张网卡的上限速率：优先内核报告的真实速率，否则回退到假定值。
func linkRateFor(iface string) linkRate {
	if mbps, err := linkSpeedMbps(iface); err == nil && mbps > 0 {
		return linkRate{
			bytesPerSecond: float64(mbps) * 1e6 / 8,
			headroom:       measuredLinkHeadroom,
			mbps:           mbps,
			source:         "measured from " + filepath.Join(sysClassNetPath, iface, "speed"),
		}
	}
	return linkRate{
		bytesPerSecond: assumedLinkBytesPerSecond,
		headroom:       assumedLinkHeadroom,
		source:         fmt.Sprintf("assumed %.0f Mbps, the link speed is unavailable", assumedLinkBytesPerSecond*8/1e6),
	}
}

// String 把速率与来源渲染成日志里的一句话，例如 "1 Gbps (measured from ...)"。
func (r linkRate) String() string {
	if r.mbps > 0 {
		return fmt.Sprintf("%s (%s)", formatMbps(r.mbps), r.source)
	}
	return fmt.Sprintf("%s (%s)", formatMbps(int(assumedLinkBytesPerSecond*8/1e6)), r.source)
}

// ceiling 返回该速率在一段时间内物理上可能承载的字节数上限。
func (r linkRate) ceiling(elapsed time.Duration) uint64 {
	if elapsed <= 0 {
		return 0
	}
	return uint64(elapsed.Seconds() * r.bytesPerSecond * r.headroom)
}

// ceilingBytes 返回"假定 1 Gbps"链路在 elapsed 内的上限。
//
// 保留这个入口是因为台账里只有它不依赖具体网卡：没有网卡名就没有可读的速率，而调用方（以及
// 检查"上限随时长增长"的测试）只需要这个基准值。真实判据走 linkRateFor(name).ceiling(...)。
func ceilingBytes(elapsed time.Duration) uint64 {
	return linkRate{bytesPerSecond: assumedLinkBytesPerSecond, headroom: assumedLinkHeadroom}.ceiling(elapsed)
}

// formatMbps 把 Mbps 渲染成好读的速率，例如 1000 -> "1 Gbps"。
func formatMbps(mbps int) string {
	if mbps >= 1000 && mbps%1000 == 0 {
		return fmt.Sprintf("%d Gbps", mbps/1000)
	}
	return fmt.Sprintf("%d Mbps", mbps)
}
