package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/agent/dnsresolver"
	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/ws"
	ping "github.com/prometheus-community/pro-bing"
)

func NewTask(task_id, command string) {
	if task_id == "" {
		return
	}
	if strings.TrimSpace(command) == "" {
		uploadTaskResult(task_id, "No command provided", 0, time.Now())
		return
	}
	if flags.DisableWebSsh {
		uploadTaskResult(task_id, "Remote control is disabled.", -1, time.Now())
		return
	}
	log.Printf("Executing task %s with command: %s", task_id, command)
	result, exitCode := runTaskCommand(command)
	uploadTaskResult(task_id, result, exitCode, time.Now())
}

func runTaskCommand(command string) (string, int) {
	cmd, cleanup, err := buildTaskCommand(command)
	if err != nil {
		return err.Error(), -1
	}
	defer cleanup()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	result := stdout.String()
	if stderr.Len() > 0 {
		result = appendErrorResult(result, stderr.String())
	}
	result = strings.ReplaceAll(result, "\r\n", "\n")
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			result = appendErrorResult(result, err.Error())
			exitCode = -1
		}
	}

	return result, exitCode
}

func buildTaskCommand(command string) (*exec.Cmd, func(), error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		scriptFile, err := os.CreateTemp("", "komari-task-*.ps1")
		if err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			_ = os.Remove(scriptFile.Name())
		}
		if _, err := scriptFile.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
			_ = scriptFile.Close()
			cleanup()
			return nil, func() {}, err
		}
		script := "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\n" + command
		if _, err := scriptFile.WriteString(script); err != nil {
			_ = scriptFile.Close()
			cleanup()
			return nil, func() {}, err
		}
		if err := scriptFile.Close(); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		cmd = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptFile.Name())
		return cmd, cleanup, nil
	} else {
		cmd = exec.Command("sh", "-s")
		cmd.Stdin = strings.NewReader(command)
	}
	return cmd, func() {}, nil
}

func appendErrorResult(result, err string) string {
	if result == "" {
		return err
	}
	return result + "\n" + err
}

func uploadTaskResult(taskID, result string, exitCode int, finishedAt time.Time) {
	payload := v2.Request{
		JSONRPC: v2.Version,
		Method:  v2.MethodAgentTaskResult,
		Params: v2.TaskResultParams{
			TaskID:     taskID,
			Result:     result,
			ExitCode:   exitCode,
			FinishedAt: finishedAt,
		},
	}
	if err := postV2RPC(payload); err != nil {
		log.Printf("Failed to upload task result: %v", err)
	}
}

// probeUnavailableError marks a local inability to start a measurement.
// It must not be interpreted as remote packet loss.
type probeUnavailableError struct {
	reason string
	err    error
}

func (e *probeUnavailableError) Error() string { return e.reason + ": " + e.err.Error() }
func (e *probeUnavailableError) Unwrap() error { return e.err }
func unavailableReason(err error) string {
	var unavailable *probeUnavailableError
	if errors.As(err, &unavailable) {
		return unavailable.reason
	}
	return ""
}

// resolveIP 解析域名到 IP 地址，排除 DNS 查询时间
func resolveIP(target string) (string, error) {
	return resolveIPPreference(target, "")
}

func resolveIPPreference(target, preference string) (string, error) {
	// 如果已经是 IP 地址，直接返回
	if ip := net.ParseIP(target); ip != nil {
		return target, nil
	}
	// 解析域名到 IP
	addrs, err := net.LookupHost(target)
	if err != nil || len(addrs) == 0 {
		if err == nil {
			err = errors.New("no addresses")
		}
		return "", &probeUnavailableError{reason: "dns_failure", err: err}
	}
	return selectPreferredIP(addrs, preference), nil
}

// A preference selects the first matching address, falling back to DNS order
// when that family is unavailable. It does not pin the address family.
func selectPreferredIP(addrs []string, preference string) string {
	family := map[string]string{"4": "ipv4", "6": "ipv6"}[preference]
	if family != "" {
		for _, addr := range addrs {
			if addressFamilyOf(addr) == family {
				return addr
			}
		}
	}
	return addrs[0]
}

// addressFamilyOf 返回一个 IP 字面量所属的地址族，供上报使用。
//
// 为什么需要它：目标是域名时，解析结果由各节点自己的解析器决定，双栈域名在不同
// 节点上可能落到不同族。两条路径的延迟与丢包不可比，如果都按「同一个任务」汇总，
// 图上就会出现一条把两条路径混在一起的曲线（实测 HK04 读 12.6% 丢包、另外两个
// v4-only 节点读 0.0%，而它们描述的根本不是同一条路）。
//
// 空字符串表示无法判断：解析失败，或拿到的是域名。
func addressFamilyOf(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	// To4 对 "::ffff:1.2.3.4" 这类 IPv4-mapped 地址也返回非 nil，
	// 那实际上是 IPv4 目标，按 IPv4 上报。
	if parsed.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}

// 三个 ping 实现都返回实际使用的地址族：族取自它们【真正拨向】的那个 IP，
// 而不是事后再解析一次 —— 双栈域名两次解析可能给出不同的结果。
//
// 延迟单位统一为**微秒**（见 highLatencyThreshold 的说明）：毫秒精度会把同机房/loopback
// 的往返压成 0 或 1，模型上就是一条阶梯；而内核与 go-ping 本来就提供微秒分辨率。
// 上报时再折算成旧字段的毫秒值，面板侧不需要任何改变。
func icmpPing(target string, timeout time.Duration) (int64, string, error) {
	return icmpPingPreference(target, timeout, "")
}

func icmpPingPreference(target string, timeout time.Duration, preference string) (int64, string, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	// For ICMP, we only need the host/IP, port is irrelevant.
	// If the host is an IPv6 literal, it might be wrapped in brackets.
	host = strings.Trim(host, "[]")

	// 先解析 IP 地址
	ip, err := resolveIPPreference(host, preference)
	if err != nil {
		return -1, "", err
	}
	family := addressFamilyOf(ip)

	// 优先用裸 socket（与改动前一致），拿不到权限时退回内核的非特权 ping socket。
	//
	// 为什么要这条退路：裸 socket 需要 root 或 CAP_NET_RAW，而内核另外提供一种非特权
	// ICMP socket（SOCK_DGRAM，由 net.ipv4.ping_group_range 控制），测回显延迟完全够用。
	// 没有它，一个不想给 agent root、又装不了 setcap 的节点（Alpine 就是）就没法做 ICMP
	// 任务 —— 只能选择以 root 运行，或者接受面板上一条恒定的假丢包。
	//
	// 顺序是「先裸后非特权」而不是反过来：车队里已有节点的历史曲线都来自裸 socket，
	// 而两种 socket 的测量在边界上未必逐字相同。在没有权限问题的节点上保持原路径，
	// 就不会为了少用一个已经在手的权限，去动几年的历史可比性。
	latency, replied, err := runICMP(ip, timeout, true)
	if err != nil {
		// 裸 socket 连开都没开起来（权限、或平台不支持）。这才值得再试一次 —— 目标
		// 不回包时 Run 不返回错误，所以这里不会把一次超时变成两次探测。
		latency, replied, err = runICMP(ip, timeout, false)
		if err != nil {
			if isPermissionErr(err) {
				return -1, family, &probeUnavailableError{reason: "permission_denied", err: err}
			}
			return -1, family, &probeUnavailableError{reason: "socket_unavailable", err: err}
		}
	}
	if !replied {
		return -1, family, errors.New("no packets received")
	}
	return latency, family, nil
}

// runICMP 发一次回显并返回平均 RTT（微秒）。
//
// 返回值区分两种失败：err 非空表示 socket 根本没建起来；err 为空而 replied 为 false
// 表示 socket 正常、目标没回包。上层要靠这个区别决定要不要换一种 socket 再试一次 ——
// 把「目标不回」也当成「本地没权限」会让每次丢包都多探一轮。
func runICMP(ip string, timeout time.Duration, privileged bool) (latency int64, replied bool, err error) {
	pinger, err := ping.NewPinger(ip)
	if err != nil {
		return -1, false, err
	}
	pinger.Count = 1
	pinger.Timeout = timeout
	pinger.SetPrivileged(privileged)
	if err := pinger.Run(); err != nil {
		return -1, false, err
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return -1, false, nil
	}
	return stats.AvgRtt.Microseconds(), true, nil
}

func tcpPing(target string, timeout time.Duration) (int64, string, error) {
	return tcpPingPreference(target, timeout, "")
}

func tcpPingPreference(target string, timeout time.Duration, preference string) (int64, string, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		// No port, assume port 80
		host = target
		port = "80"
	}

	// If the host is an IPv6 literal, it might be wrapped in brackets.
	host = strings.Trim(host, "[]")

	ip, err := resolveIPPreference(host, preference)
	if err != nil {
		return -1, "", err
	}
	family := addressFamilyOf(ip)

	targetAddr := net.JoinHostPort(ip, port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", targetAddr, timeout)
	if err != nil {
		if isPermissionErr(err) {
			return -1, family, &probeUnavailableError{reason: "permission_denied", err: err}
		}
		return -1, family, err
	}
	defer conn.Close()
	// 连接已经建立，以对端地址为准：这就是本次测量真正走过的族。
	if remote, ok := conn.RemoteAddr().(*net.TCPAddr); ok && remote != nil {
		if actual := addressFamilyOf(remote.IP.String()); actual != "" {
			family = actual
		}
	}
	return time.Since(start).Microseconds(), family, nil
}

func httpPing(target string, timeout time.Duration) (int64, string, error) {
	return httpPingWith(target, timeout, resolveIP, net.DialTimeout)
}

func httpPingWith(target string, timeout time.Duration, resolve func(string) (string, error), dial func(string, string, time.Duration) (net.Conn, error)) (int64, string, error) {
	// Handle raw IPv6 address for URL
	if strings.Contains(target, ":") && !strings.Contains(target, "[") {
		// check if it's a valid IP to avoid wrapping hostnames
		if ip := net.ParseIP(target); ip != nil && ip.To4() == nil {
			target = "[" + target + "]"
		}
	}

	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}

	// The transport can finish a dial after Client.Timeout returns. Protect the
	// snapshot read at return even though each probe owns its own transport.
	var familyMu sync.Mutex
	var dialedFamily string
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// 在 Dial 之前解析 IP，排除 DNS 时间
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := resolve(host)
			if err != nil {
				return nil, err
			}
			familyMu.Lock()
			dialedFamily = addressFamilyOf(ip)
			familyMu.Unlock()
			return dial(network, net.JoinHostPort(ip, port), timeout)
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	start := time.Now()
	resp, err := client.Get(target)
	latency := time.Since(start).Microseconds()
	familyMu.Lock()
	family := dialedFamily
	familyMu.Unlock()
	if err != nil {
		return -1, family, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return latency, family, nil
	}
	return latency, family, errors.New("http status not ok")
}

// httpProbeOutcome is one HTTP attempt with its phase breakdown. The legacy
// value/loss decision is derived separately so status-aware and legacy modes can
// share the same measurement.
type httpProbeOutcome struct {
	totalUS   int64
	family    string
	obs       *v2.HTTPProbeReport
	err       error
	reachable bool
}

// httpProbe measures one HTTP GET and records DNS/TCP/TLS/TTFB/total phases.
// expectedStatus is a comma-separated list of codes/ranges; empty means the
// legacy 200-399 policy. statusAware=false keeps 4xx/5xx as a failed probe.
func httpProbe(target string, timeout time.Duration, expectedStatus string, statusAware bool) httpProbeOutcome {
	return httpProbePreference(target, timeout, expectedStatus, statusAware, "")
}

func httpProbePreference(target string, timeout time.Duration, expectedStatus string, statusAware bool, preference string) httpProbeOutcome {
	if strings.Contains(target, ":") && !strings.Contains(target, "[") {
		if ip := net.ParseIP(target); ip != nil && ip.To4() == nil {
			target = "[" + target + "]"
		}
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}

	var phaseMu sync.Mutex
	var dnsUS, tcpUS int64
	var tcpSet bool
	var firstFamily string
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			dnsStart := time.Now()
			ip, err := resolveIPPreference(host, preference)
			dnsElapsed := time.Since(dnsStart).Microseconds()
			phaseMu.Lock()
			dnsUS += dnsElapsed
			if firstFamily == "" {
				firstFamily = addressFamilyOf(ip)
			}
			phaseMu.Unlock()
			if err != nil {
				return nil, err
			}
			tcpStart := time.Now()
			conn, err := net.DialTimeout(network, net.JoinHostPort(ip, port), timeout)
			if err != nil && isPermissionErr(err) {
				err = &probeUnavailableError{reason: "permission_denied", err: err}
			}
			tcpElapsed := time.Since(tcpStart).Microseconds()
			phaseMu.Lock()
			tcpUS += tcpElapsed
			tcpSet = true
			phaseMu.Unlock()
			return conn, err
		},
	}
	defer transport.CloseIdleConnections()

	var tlsStart time.Time
	var tlsUS int64
	var ttfbUS int64
	var ttfbSet bool
	var requestWritten time.Time
	var connected bool
	var redirects int
	start := time.Now()
	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { phaseMu.Lock(); tlsStart = time.Now(); phaseMu.Unlock() },
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			phaseMu.Lock()
			if !tlsStart.IsZero() {
				tlsUS = time.Since(tlsStart).Microseconds()
			}
			phaseMu.Unlock()
		},
		GetConn: func(string) {
			phaseMu.Lock()
			connected = false
			requestWritten = time.Time{}
			ttfbSet = false
			phaseMu.Unlock()
		},
		GotConn: func(httptrace.GotConnInfo) {
			phaseMu.Lock()
			connected = true
			phaseMu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			phaseMu.Lock()
			if info.Err == nil {
				requestWritten = time.Now()
			}
			phaseMu.Unlock()
		},
		GotFirstResponseByte: func() {
			phaseMu.Lock()
			if !requestWritten.IsZero() {
				ttfbUS = time.Since(requestWritten).Microseconds()
				ttfbSet = true
			}
			phaseMu.Unlock()
		},
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			redirects++
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return httpProbeOutcome{err: err}
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := client.Do(req)
	totalUS := time.Since(start).Microseconds()
	phaseMu.Lock()
	family := firstFamily
	tcpKnown := tcpSet
	wasConnected := connected
	dnsValue, tcpValue, tlsValue, ttfbValue, hasTTFB := dnsUS, tcpUS, tlsUS, ttfbUS, ttfbSet
	phaseMu.Unlock()
	out := httpProbeOutcome{totalUS: totalUS, family: family, reachable: wasConnected}
	obs := &v2.HTTPProbeReport{
		DNSUS: &dnsValue, TotalUS: &totalUS, Redirects: &redirects,
	}
	if tcpKnown {
		obs.TCPUS = &tcpValue
	}
	if tlsValue > 0 {
		obs.TLSUS = &tlsValue
	}
	if hasTTFB {
		obs.TTFBUS = &ttfbValue
	}
	if resp != nil {
		defer resp.Body.Close()
		status := resp.StatusCode
		available := httpStatusAllowed(status, expectedStatus)
		obs.StatusCode = &status
		obs.Reachable = &out.reachable
		obs.Available = &available
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			days := time.Until(resp.TLS.PeerCertificates[0].NotAfter).Hours() / 24
			obs.TLSExpiryDays = &days
		}
		if !statusAware && !available {
			out.err = errors.New("http status not ok")
		}
	} else if tcpKnown {
		obs.Reachable = &out.reachable
	}
	out.obs = obs
	if err != nil {
		out.err = err
	}
	return out
}

func httpStatusAllowed(code int, spec string) bool {
	if strings.TrimSpace(spec) == "" {
		return code >= 200 && code < 400
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			low, errLow := strconv.Atoi(strings.TrimSpace(bounds[0]))
			high, errHigh := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if errLow == nil && errHigh == nil && code >= low && code <= high {
				return true
			}
			continue
		}
		if exact, err := strconv.Atoi(part); err == nil && code == exact {
			return true
		}
	}
	return false
}

// auto 类型的协议解析（新增）
//
// 背景：监测任务的类型是每个任务一个（icmp/tcp/http）。选错协议的后果是恒定的
// 100% 丢包 —— 看起来像宕机，其实是协议不匹配。auto 让 agent 自己先探一次，
// 选一个该目标真正答应的协议，从而不需要人工判断。
//
// 判定顺序：
//  1. 先试 ICMP（便宜，一次探测）；
//  2. ICMP 不通则试 TCP —— 目标自带端口就用它，否则依次试 443/80；
//  3. 都不通时保持 icmp 并如实报告失败（不假装成功）。
//
// 结果按目标缓存在进程内，避免每个上报周期都重新探测。

type autoDecision struct {
	kind   string // 实际采用的协议："icmp" 或 "tcp"
	target string // 实际使用的目标（tcp 时可能被补上端口）
	at     time.Time
}

var (
	autoMu    sync.Mutex
	autoCache = map[string]autoDecision{}
)

// autoCacheTTL 决定多久重新解析一次。目标开放的协议通常很稳定，
// 但偶尔会变（例如防火墙策略调整），所以不做永久缓存。
const autoCacheTTL = 10 * time.Minute

const autoProbeTimeout = 2 * time.Second

// ResolveAuto 把 auto 解析成具体的 (协议, 目标)。
func ResolveAuto(pingTarget string) (string, string) {
	return resolveAutoPreference(pingTarget, "")
}

func resolveAutoPreference(pingTarget, preference string) (string, string) {
	cacheKey := preference + "\x00" + pingTarget
	autoMu.Lock()
	if d, ok := autoCache[cacheKey]; ok && time.Since(d.at) < autoCacheTTL {
		autoMu.Unlock()
		return d.kind, d.target
	}
	autoMu.Unlock()

	kind, tgt := probeAutoProtocolPreference(pingTarget, preference)

	autoMu.Lock()
	autoCache[cacheKey] = autoDecision{kind: kind, target: tgt, at: time.Now()}
	autoMu.Unlock()
	return kind, tgt
}

// autoProbeResult 汇总一次 auto 探测的原始结果（便于把「决策」与「探测」分开测试）。
type autoProbeResult struct {
	icmpOK     bool   // ICMP 探通了
	icmpDenied bool   // ICMP 是因本地权限不足而失败（≠ 目标不答 ICMP）
	openPort   string // 第一个 TCP 可用的端口（空 = 没有）
}

// decideAuto 是纯函数：给定探测结果，决定用哪个协议、什么目标。
//
// 规则：
//  1. 目标自带端口 —— 尊重用户的显式指定，直接用 tcp（不再探测、不做猜测）；
//  2. 否则 ICMP 通 -> icmp；
//  3. 否则 TCP 443/80 有可用端口 -> tcp + 该端口；
//  4. 都不通 -> 保持 icmp，让上报如实反映失败（不假装成功）。
func decideAuto(target string, r autoProbeResult) (string, string) {
	if _, port, err := net.SplitHostPort(target); err == nil && port != "" {
		return "tcp", target
	}
	if r.icmpOK {
		return "icmp", target
	}
	if r.openPort != "" {
		return "tcp", net.JoinHostPort(target, r.openPort)
	}
	return "icmp", target
}

func probeAutoProtocol(pingTarget string) (string, string) {
	return probeAutoProtocolPreference(pingTarget, "")
}

func probeAutoProtocolPreference(pingTarget, preference string) (string, string) {
	// 目标已显式带端口：直接按 tcp 处理，无需探测
	if _, port, err := net.SplitHostPort(pingTarget); err == nil && port != "" {
		return decideAuto(pingTarget, autoProbeResult{})
	}

	var r autoProbeResult
	if _, _, err := icmpPingPreference(pingTarget, autoProbeTimeout, preference); err == nil {
		r.icmpOK = true
	} else if isPermissionErr(err) {
		// 本地没有发 ICMP 的权限 —— 与「目标不答 ICMP」是两回事，
		// 但无论哪种，icmp 在这台机器上都用不了，所以继续尝试 TCP。
		r.icmpDenied = true
	}
	if !r.icmpOK {
		for _, port := range []string{"443", "80"} {
			if _, _, err := tcpPingPreference(net.JoinHostPort(pingTarget, port), autoProbeTimeout, preference); err == nil {
				r.openPort = port
				break
			}
		}
	}
	return decideAuto(pingTarget, r)
}

// isPermissionErr 判断 ping 失败是否源于本地权限，而不是目标不可达。
// 这个区分很关键：把工具的限制误读成目标的事实，会得出相反的结论。
func isPermissionErr(err error) bool {
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

// NewPingTask 执行一次延迟监测任务。
//
// reference 非空时，会在【同一周期内】额外探测该参考目标并以 role="reference"
// 上报，用于路径归因（主机 vs 网关）：两条曲线并列后，
//
//	· 两者同时变差 -> 问题在本机 / 内网 / 共同上游
//	· 只有主目标变差 -> 问题在主目标那一段路径
//
// 否则只看到一条抖动的曲线，无从判断是哪一段出了问题。
func NewPingTask(conn *ws.SafeConn, taskID uint, pingType, pingTarget, reference string) {
	NewPingTaskWithParams(conn, v2.PingParams{
		TaskID: taskID, Type: pingType, Target: pingTarget, Reference: reference,
	})
}

// NewPingTaskWithParams is the v1.8 entrypoint. Zero/empty additive fields keep
// the legacy single-probe behavior exactly.
func NewPingTaskWithParams(conn *ws.SafeConn, p v2.PingParams) {
	if p.TaskID == 0 {
		log.Printf("Invalid task ID: %d", p.TaskID)
		return
	}
	runPingTaskConfig(conn, p)
	if ref := strings.TrimSpace(p.Reference); ref != "" {
		refParams := p
		refParams.Reference = ""
		runReferenceProbeConfig(conn, refParams, ref)
	}
}

// runReferenceProbe 保留旧签名，供既有测试/调用方使用。
func runReferenceProbe(conn *ws.SafeConn, taskID uint, pingType, reference string, timeout time.Duration) {
	p := v2.PingParams{TaskID: taskID, Type: pingType, Target: reference,
		ProbeTimeoutMS: int(timeout / time.Millisecond)}
	runReferenceProbeConfig(conn, p, reference)
}

// runReferenceProbeConfig 探测参考目标并以 role="reference" 上报。
// 协议沿用主任务的协议（icmp/tcp/http）；不认识的协议按 icmp 处理。
func runReferenceProbeConfig(conn *ws.SafeConn, p v2.PingParams, reference string) {
	kind := p.Type
	switch kind {
	case "tcp", "http", "icmp":
	default:
		kind = "icmp"
	}
	if kind == "http" {
		runHTTPProbeConfig(conn, p, reference, "reference")
		return
	}
	timeout := probeTimeout(p)
	measure := func() (int64, string, error) {
		switch kind {
		case "tcp":
			return tcpPingPreference(reference, timeout, p.IPVersionPreference)
		default:
			return icmpPingPreference(reference, timeout, p.IPVersionPreference)
		}
	}
	attempts, spacing := probeAttempts(p)
	if attempts <= 1 {
		latencyUS, family, err := measure()
		if err != nil {
			log.Printf("reference probe task %d [%s] target=%s failed: %v", p.TaskID, kind, reference, err)
		}
		value, valueUS := pingResultValues(latencyUS, err == nil)
		if reason := unavailableReason(err); reason != "" {
			uploadPingResult(conn, kind, v2.BuildPingResultPayloadWithStats(p.TaskID, kind, "reference", family, value, valueUS, v2.PingProbeStats{ProbeStatus: "unknown", ProbeReason: reason}, time.Now()))
			return
		}
		uploadPingResult(conn, kind, v2.BuildPingResultPayloadWithRoleFamilyAndMicros(
			p.TaskID, kind, "reference", family, value, valueUS, time.Now()))
		return
	}
	cycle := runProbeCycle(measure, attempts, spacing)
	value, valueUS := pingResultValues(cycle.firstUS, cycle.success > 0)
	stats := cycle.stats()
	uploadPingResult(conn, kind, v2.BuildPingResultPayloadWithStats(
		p.TaskID, kind, "reference", cycle.family, value, valueUS, stats, time.Now()))
}

// runPingTask 保留旧签名，供既有测试/调用方使用。
func runPingTask(conn *ws.SafeConn, taskID uint, pingType, pingTarget string) {
	runPingTaskConfig(conn, v2.PingParams{TaskID: taskID, Type: pingType, Target: pingTarget})
}

// runPingTaskConfig 是单个目标的实际测量逻辑。
func runPingTaskConfig(conn *ws.SafeConn, p v2.PingParams) {
	if p.TaskID == 0 {
		log.Printf("Invalid task ID: %d", p.TaskID)
		return
	}
	pingType, pingTarget := p.Type, p.Target
	// dual：同一周期内 ICMP 与 TCP 各测 N 次，上报两条结果。
	if pingType == "dual" {
		runDualPingWithConfig(conn, p)
		return
	}
	// auto：先探一次，选一个该目标真正答应的协议；下游逻辑（含上报的 ping_type）
	// 统一使用解析后的值，因此面板上能看到实际采用的协议。
	if pingType == "auto" {
		resolvedKind, resolvedTarget := resolveAutoPreference(p.Target, p.IPVersionPreference)
		log.Printf("ping task %d: auto resolved target=%s -> type=%s target=%s", p.TaskID, p.Target, resolvedKind, resolvedTarget)
		pingType, pingTarget = resolvedKind, resolvedTarget
	}
	if pingType == "http" {
		runHTTPProbeConfig(conn, p, pingTarget, "")
		return
	}

	timeout := probeTimeout(p)
	measure := func() (int64, string, error) {
		switch pingType {
		case "icmp":
			return icmpPingPreference(pingTarget, timeout, p.IPVersionPreference)
		case "tcp":
			return tcpPingPreference(pingTarget, timeout, p.IPVersionPreference)
		case "http":
			return httpPing(pingTarget, timeout)
		default:
			return -1, "", &probeUnavailableError{reason: "unsupported", err: errors.New("unsupported ping type")}
		}
	}

	attempts, spacing := probeAttempts(p)
	if attempts <= 1 {
		// Legacy single-probe path: keep the SYN-retransmit retry and the exact
		// old wire shape. Do not attach v1.8 stats here.
		var family, unavailable string
		latencyUS, ok := measureWithRetries(p.TaskID, pingType, func() (int64, error) {
			latency, used, err := measure()
			if reason := unavailableReason(err); reason != "" {
				unavailable = reason
			}
			if err == nil {
				family = used
			}
			return latency, err
		})
		pingResult, pingResultUS := pingResultValues(latencyUS, ok)
		if unavailable != "" {
			uploadPingResult(conn, pingType, v2.BuildPingResultPayloadWithStats(p.TaskID, pingType, "", family, pingResult, pingResultUS, v2.PingProbeStats{ProbeStatus: "unknown", ProbeReason: unavailable}, time.Now()))
			return
		}
		uploadPingResult(conn, pingType, v2.BuildPingResultPayloadWithRoleFamilyAndMicros(
			p.TaskID, pingType, "", family, pingResult, pingResultUS, time.Now()))
		return
	}

	cycle := runProbeCycle(measure, attempts, spacing)
	value, valueUS := pingResultValues(cycle.firstUS, cycle.success > 0)
	stats := cycle.stats()
	uploadPingResult(conn, pingType, v2.BuildPingResultPayloadWithStats(
		p.TaskID, pingType, "", cycle.family, value, valueUS, stats, time.Now()))
}

// runHTTPProbeConfig measures HTTP with the additive phase breakdown. The
// legacy value/loss decision stays in place for `legacy` mode; `status_aware`
// turns 4xx/5xx into reachable-but-unavailable instead of packet loss.
func runHTTPProbeConfig(conn *ws.SafeConn, p v2.PingParams, target, role string) {
	attempts, spacing := probeAttempts(p)
	timeout := probeTimeout(p)
	expected := p.HTTPExpectedStatus
	statusAware := p.HTTPMode == "status_aware"
	var diagnostic, successful *v2.HTTPProbeReport
	measure := func() (int64, string, error) {
		out := httpProbePreference(target, timeout, expected, statusAware, p.IPVersionPreference)
		if diagnostic == nil {
			diagnostic = out.obs
		}
		if out.err == nil && successful == nil {
			successful = out.obs
		}
		return out.totalUS, out.family, out.err
	}

	if attempts <= 1 {
		out := httpProbePreference(target, timeout, expected, statusAware, p.IPVersionPreference)
		if out.err != nil {
			log.Printf("http ping task %d target=%s failed: %v", p.TaskID, target, out.err)
		}
		value, valueUS := pingResultValues(out.totalUS, out.err == nil)
		stats := v2.PingProbeStats{HTTP: out.obs}
		if reason := unavailableReason(out.err); reason != "" {
			stats.ProbeStatus, stats.ProbeReason = "unknown", reason
		}
		uploadPingResult(conn, "http", v2.BuildPingResultPayloadWithStats(
			p.TaskID, "http", role, out.family, value, valueUS, stats, time.Now()))
		return
	}

	cycle := runProbeCycle(measure, attempts, spacing)
	value, valueUS := pingResultValues(cycle.firstUS, cycle.success > 0)
	stats := cycle.stats()
	stats.HTTP = successful
	if stats.HTTP == nil {
		stats.HTTP = diagnostic
	}
	uploadPingResult(conn, "http", v2.BuildPingResultPayloadWithStats(
		p.TaskID, "http", role, cycle.family, value, valueUS, stats, time.Now()))
}

// probeCycle is the result of N attempts in one scheduled cycle.
type probeCycle struct {
	attempts    int
	success     int
	lossPct     float64
	firstUS     int64
	minUS       *int64
	avgUS       *int64
	maxUS       *int64
	jitterUS    *int64
	family      string
	unavailable string
}

func (c probeCycle) stats() v2.PingProbeStats {
	attempts, success, loss := c.attempts, c.success, c.lossPct
	if c.unavailable != "" {
		return v2.PingProbeStats{ProbeStatus: "unknown", ProbeReason: c.unavailable}
	}
	return v2.PingProbeStats{
		Attempts: &attempts, Success: &success, LossPct: &loss,
		MinUS: c.minUS, AvgUS: c.avgUS, MaxUS: c.maxUS, JitterUS: c.jitterUS,
		ProbeMode: "multi",
	}
}

// runProbeCycle performs attempts with spacing and computes success-only
// statistics. A failed attempt contributes to loss_pct but never to
// min/avg/max/jitter.
func runProbeCycle(measure func() (int64, string, error), attempts, spacingMS int) probeCycle {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 10 {
		attempts = 10
	}
	cycle := probeCycle{attempts: attempts, firstUS: -1}
	var latencies []int64
	var firstFamily string
	for i := 0; i < attempts; i++ {
		if i > 0 && spacingMS > 0 {
			time.Sleep(time.Duration(spacingMS) * time.Millisecond)
		}
		latency, family, err := measure()
		if firstFamily == "" && family != "" {
			firstFamily = family
		}
		if err != nil {
			if reason := unavailableReason(err); reason != "" && cycle.unavailable == "" {
				cycle.unavailable = reason
			}
			continue
		}
		cycle.success++
		if cycle.firstUS < 0 {
			cycle.firstUS = latency
			cycle.family = family
		}
		latencies = append(latencies, latency)
	}
	if cycle.family == "" {
		cycle.family = firstFamily
	}
	cycle.lossPct = float64(cycle.attempts-cycle.success) / float64(cycle.attempts) * 100
	if len(latencies) > 0 {
		minV, maxV := latencies[0], latencies[0]
		var sum int64
		for _, v := range latencies {
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
			sum += v
		}
		avgV := sum / int64(len(latencies))
		cycle.minUS, cycle.avgUS, cycle.maxUS = &minV, &avgV, &maxV
	}
	if len(latencies) >= 2 {
		// RFC 3550 instantaneous jitter over consecutive successful RTTs.
		var jitter float64
		previous := latencies[0]
		seen := 0
		for _, current := range latencies[1:] {
			delta := float64(current - previous)
			if delta < 0 {
				delta = -delta
			}
			jitter += (delta - jitter) / 16
			previous = current
			seen++
		}
		if seen > 0 {
			rounded := int64(math.Round(jitter))
			cycle.jitterUS = &rounded
		}
	}
	return cycle
}

func probeTimeout(p v2.PingParams) time.Duration {
	if p.ProbeTimeoutMS >= 100 && p.ProbeTimeoutMS <= 30000 {
		return time.Duration(p.ProbeTimeoutMS) * time.Millisecond
	}
	return 3 * time.Second
}

func probeAttempts(p v2.PingParams) (int, int) {
	attempts := p.ProbeAttempts
	if attempts <= 0 {
		attempts = 1
	}
	if attempts > 10 {
		attempts = 10
	}
	spacing := p.ProbeSpacingMS
	if spacing <= 0 {
		spacing = 200
	}
	if spacing < 50 {
		spacing = 50
	}
	if spacing > 1000 {
		spacing = 1000
	}
	return attempts, spacing
}

// 首次测量超过这个延迟就重试：Linux 的初始 RTO 是 1s，一次 SYN 重传会把
// 握手时间推到 1s 以上，这个值正好把「被重传撑大的测量」和「真的慢」分开。
//
// 单位是**微秒**，与 measure() 的返回值一致（1_000_000µs = 1000ms）。
const highLatencyThreshold = 1_000_000

// 重试比首次快这么多，基本可以认定首次被 SYN 重传撑大了。
// 800_000µs = SYN/SYN-ACK 首次超时重传 1000ms - 防误判容许 200ms 延迟抖动。
const retryDropThresholdTcping = 800_000

// pingHighLatencyRetries 首次偏慢时的重试次数。
const pingHighLatencyRetries = 3

// tcpRetransmitSuspected 判断「首次偏慢、重试正常」是否属于 TCP 握手重传。
// 它只影响日志措辞 —— 无论结论如何，这次测量都是成功的。
func tcpRetransmitSuspected(pingType string, firstLatency, second int64) bool {
	return pingType == "tcp" && firstLatency-second > retryDropThresholdTcping
}

// formatLatency 把微秒延迟渲染成日志里好读的形式（"1247.0ms"）。
//
// 内部一律用微秒，但运维读日志时想看到的是毫秒 —— 直接把 1247000 写进日志，量级判断
// 反而要靠数位数。失败哨兵值 -1 原样输出，不要伪装成 "0.0ms"。
func formatLatency(micros int64) string {
	if micros < 0 {
		return fmt.Sprintf("%d", micros)
	}
	return fmt.Sprintf("%.1fms", float64(micros)/1000)
}

// pingResultValues 把一次微秒测量折算成上报字段。
//
// 线缆上仍然是两个值，这是刻意的过渡形态：
//   - value 是毫秒（微秒 / 1000，截断），旧面板只认识它，也只用它存 ping 记录；
//   - value_us 是测量到的微秒值，新面板可以用它画真实分辨率。
//
// 失败（真的没连上）时两者都是 -1：这是面板的丢包哨兵（Value<0 → ping.loss=1），
// 微秒字段用同一个值可以让"失败"在两侧都只有一种表示，而不是"缺字段"和"未知"并存。
func pingResultValues(latencyUS int64, ok bool) (valueMillis int, valueUS int64) {
	if !ok {
		return -1, -1
	}
	if latencyUS < 0 {
		return -1, -1
	}
	return int(latencyUS / 1000), latencyUS
}

// measureWithRetries 执行一次测量，首次偏慢时按 highLatencyThreshold 重试，
// 返回最终应当上报的延迟（单位与 measure() 相同：微秒）以及本次是否成功。
//
// 返回值语义（这是与面板的契约，不要靠魔法值推断）：
//   - ok == false **只**表示探测真的失败/超时（measure 返回了 error），此时返回 -1，
//     面板把 Value<0 记为 ping.loss=1，这才是丢包。
//   - ok == true 表示测量成功，返回的延迟来自最后一次成功的 measure 调用（family 由
//     同一次调用写入，两者同源）；延迟多高都不影响这一点。
//
// 这里曾经有两处把「慢」当成「丢」：一处把「判定为 SYN 重传」当失败（虽然握手【已经
// 完成】、重试还测到了真实 RTT），另一处在 4 次测量全部 >1000ms 时也返回 -1 —— 哪怕
// 每一次 measure 都是 err == nil。后果是成功的探测在面板上变成整分钟 100% 丢包并触发
// SLA 误报：实测 OC424 对天津电信显示的 12% 丢包全部来自第一处，而同一目标直连 30 次
// 一次没丢；对 >1s 的 http 任务（httpPing 计的是整次 GET 耗时）或拥塞链路，第二处必然
// 触发。丢包只应表示「真的没连上」，慢就是慢。
func measureWithRetries(taskID uint, pingType string, measure func() (int64, error)) (int64, bool) {
	latency, err := measure()
	if err != nil {
		log.Printf("Ping task %d failed: %v", taskID, err)
		return -1, false
	}
	if latency <= highLatencyThreshold {
		return latency, true
	}

	firstLatency := latency
	lastLatency := latency
	for i := 0; i < pingHighLatencyRetries; i++ {
		retryLatency, retryErr := measure()
		if retryErr != nil {
			log.Printf("Ping task %d failed: %v", taskID, retryErr)
			return -1, false
		}
		lastLatency = retryLatency
		if retryLatency <= highLatencyThreshold {
			if tcpRetransmitSuspected(pingType, firstLatency, retryLatency) {
				// 第一次的延迟被重传撑大了，不能采信；重试这次是真实的。
				log.Printf("Ping task %d: tcp handshake retransmitted (first=%s retry=%s), reporting the retry",
					taskID, formatLatency(firstLatency), formatLatency(retryLatency))
			}
			return retryLatency, true
		}
	}

	// 重试次数用尽且每一次 measure 都成功了（有 error 的分支都已提前返回）：这是一个
	// 慢但通畅的目标，不是丢包。上报最后一次实测延迟，面板据此记 latency 而不是 loss。
	log.Printf("Ping task %d: latency stayed above %s for %d attempts (first=%s last=%s); reporting the measured latency, not packet loss",
		taskID, formatLatency(highLatencyThreshold), pingHighLatencyRetries+1,
		formatLatency(firstLatency), formatLatency(lastLatency))
	return lastLatency, true
}

// uploadPingResult 统一上报一条 ping 结果（WebSocket 优先，否则退回 POST）。
func uploadPingResult(conn *ws.SafeConn, pingType string, payload interface{}) {
	if conn == nil {
		if err := postV2RPC(payload); err != nil {
			log.Printf("Failed to upload %s ping result over POST: %v", pingType, err)
		}
		return
	}
	if err := conn.WriteJSON(payload); err != nil {
		log.Printf("Failed to write %s ping JSON to WebSocket: %v", pingType, err)
	}
}

// dualTCPTarget 决定 dual 任务的 TCP 腿用哪个目标地址。
// 目标自带端口就尊重它；否则优先复用 auto 的缓存解析结果，最后才现场探 443/80。
func dualTCPTarget(target string) string {
	return dualTCPTargetPreference(target, "")
}

func dualTCPTargetPreference(target, preference string) string {
	if _, port, err := net.SplitHostPort(target); err == nil && port != "" {
		return target
	}
	autoMu.Lock()
	d, ok := autoCache[preference+"\x00"+target]
	autoMu.Unlock()
	if ok && d.kind == "tcp" && d.target != "" {
		return d.target
	}
	for _, port := range []string{"443", "80"} {
		cand := net.JoinHostPort(target, port)
		if _, _, err := tcpPingPreference(cand, autoProbeTimeout, preference); err == nil {
			return cand
		}
	}
	return net.JoinHostPort(target, "443")
}

// runDualPing 保留旧签名，供既有测试/调用方使用。
func runDualPing(conn *ws.SafeConn, taskID uint, pingTarget string, timeout time.Duration) {
	runDualPingWithConfig(conn, v2.PingParams{
		TaskID: taskID, Type: "dual", Target: pingTarget,
		ProbeTimeoutMS: int(timeout / time.Millisecond),
	})
}

// runDualPingWithConfig 执行「双协议并列探测」：同一周期内用 ICMP 和 TCP 各测
// N 次目标，分别上报两条结果（同一 task_id、不同 ping_type）。
//
// 为什么需要它：任务的探测协议是每个任务一个，选错就会得到恒定的 100% 丢包，
// 看起来像宕机，实际是协议不匹配。并列探测后，面板上会同时出现
// 「ICMP 100%」和「TCP 2ms」两组数据 —— 一眼就能区分「目标不答这个协议」
// 和「目标真的不可达」，不再需要人来推断。
func runDualPingWithConfig(conn *ws.SafeConn, p v2.PingParams) {
	timeout := probeTimeout(p)
	attempts, spacing := probeAttempts(p)
	legs := []struct {
		kind   string
		target string
	}{
		{"icmp", p.Target},
		{"tcp", dualTCPTargetPreference(p.Target, p.IPVersionPreference)},
	}

	for _, leg := range legs {
		measure := func() (int64, string, error) {
			switch leg.kind {
			case "icmp":
				return icmpPingPreference(leg.target, timeout, p.IPVersionPreference)
			case "tcp":
				return tcpPingPreference(leg.target, timeout, p.IPVersionPreference)
			default:
				return -1, "", errors.New("unsupported dual leg")
			}
		}
		if attempts <= 1 {
			latencyUS, family, err := measure()
			if err != nil {
				log.Printf("dual ping task %d [%s] target=%s failed: %v", p.TaskID, leg.kind, leg.target, err)
			}
			value, valueUS := pingResultValues(latencyUS, err == nil)
			if reason := unavailableReason(err); reason != "" {
				uploadPingResult(conn, leg.kind, v2.BuildPingResultPayloadWithStats(p.TaskID, leg.kind, "", family, value, valueUS, v2.PingProbeStats{ProbeStatus: "unknown", ProbeReason: reason}, time.Now()))
				continue
			}
			uploadPingResult(conn, leg.kind, v2.BuildPingResultPayloadWithRoleFamilyAndMicros(
				p.TaskID, leg.kind, "", family, value, valueUS, time.Now()))
			continue
		}
		cycle := runProbeCycle(measure, attempts, spacing)
		value, valueUS := pingResultValues(cycle.firstUS, cycle.success > 0)
		uploadPingResult(conn, leg.kind, v2.BuildPingResultPayloadWithStats(
			p.TaskID, leg.kind, "", cycle.family, value, valueUS, cycle.stats(), time.Now()))
	}
}

func postV2RPC(payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := v2RPCEndpoint()
	compressed := false
	if !flags.DisableCompression {
		if gz, err := gzipBytes(body); err == nil {
			body = gz
			compressed = true
		}
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setV2Auth(req)
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}
	client := dnsresolver.GetHTTPClientWithPreference(30*time.Second, flags.PreferIPVersion)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(respBody)}
	}
	if len(bytes.TrimSpace(respBody)) > 0 {
		if _, err := parseV2Response(respBody); err != nil {
			return err
		}
	}
	return nil
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
