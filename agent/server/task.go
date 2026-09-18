package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
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

// resolveIP 解析域名到 IP 地址，排除 DNS 查询时间
func resolveIP(target string) (string, error) {
	// 如果已经是 IP 地址，直接返回
	if ip := net.ParseIP(target); ip != nil {
		return target, nil
	}
	// 解析域名到 IP
	addrs, err := net.LookupHost(target)
	if err != nil || len(addrs) == 0 {
		return "", errors.New("failed to resolve target")
	}
	return addrs[0], nil // 返回第一个解析的 IP
}

func icmpPing(target string, timeout time.Duration) (int64, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	// For ICMP, we only need the host/IP, port is irrelevant.
	// If the host is an IPv6 literal, it might be wrapped in brackets.
	host = strings.Trim(host, "[]")

	// 先解析 IP 地址
	ip, err := resolveIP(host)
	if err != nil {
		return -1, err
	}

	pinger, err := ping.NewPinger(ip)
	if err != nil {
		return -1, err
	}
	pinger.Count = 1
	pinger.Timeout = timeout
	pinger.SetPrivileged(true)
	err = pinger.Run()
	if err != nil {
		return -1, err
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return -1, errors.New("no packets received")
	}
	return stats.AvgRtt.Milliseconds(), nil
}

func tcpPing(target string, timeout time.Duration) (int64, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		// No port, assume port 80
		host = target
		port = "80"
	}

	// If the host is an IPv6 literal, it might be wrapped in brackets.
	host = strings.Trim(host, "[]")

	ip, err := resolveIP(host)
	if err != nil {
		return -1, err
	}

	targetAddr := net.JoinHostPort(ip, port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", targetAddr, timeout)
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	return time.Since(start).Milliseconds(), nil
}

func httpPing(target string, timeout time.Duration) (int64, error) {
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

	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// 在 Dial 之前解析 IP，排除 DNS 时间
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := resolveIP(host)
			if err != nil {
				return nil, err
			}
			return net.DialTimeout(network, net.JoinHostPort(ip, port), timeout)
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	start := time.Now()
	resp, err := client.Get(target)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return latency, nil
	}
	return latency, errors.New("http status not ok")
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
	autoMu.Lock()
	if d, ok := autoCache[pingTarget]; ok && time.Since(d.at) < autoCacheTTL {
		autoMu.Unlock()
		return d.kind, d.target
	}
	autoMu.Unlock()

	kind, tgt := probeAutoProtocol(pingTarget)

	autoMu.Lock()
	autoCache[pingTarget] = autoDecision{kind: kind, target: tgt, at: time.Now()}
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
	// 目标已显式带端口：直接按 tcp 处理，无需探测
	if _, port, err := net.SplitHostPort(pingTarget); err == nil && port != "" {
		return decideAuto(pingTarget, autoProbeResult{})
	}

	var r autoProbeResult
	if _, err := icmpPing(pingTarget, autoProbeTimeout); err == nil {
		r.icmpOK = true
	} else if isPermissionErr(err) {
		// 本地没有发 ICMP 的权限 —— 与「目标不答 ICMP」是两回事，
		// 但无论哪种，icmp 在这台机器上都用不了，所以继续尝试 TCP。
		r.icmpDenied = true
	}
	if !r.icmpOK {
		for _, port := range []string{"443", "80"} {
			if _, err := tcpPing(net.JoinHostPort(pingTarget, port), autoProbeTimeout); err == nil {
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
	if taskID == 0 {
		log.Printf("Invalid task ID: %d", taskID)
		return
	}
	runPingTask(conn, taskID, pingType, pingTarget)

	if ref := strings.TrimSpace(reference); ref != "" {
		runReferenceProbe(conn, taskID, pingType, ref, 3*time.Second)
	}
}

// runReferenceProbe 探测参考目标并以 role="reference" 上报。
// 协议沿用主任务的协议（icmp/tcp/http）；不认识的协议按 icmp 处理。
func runReferenceProbe(conn *ws.SafeConn, taskID uint, pingType, reference string, timeout time.Duration) {
	// 归一化：auto/dual 这类「主目标侧的策略」对参考点没有意义，退回 icmp。
	kind := pingType
	switch kind {
	case "tcp", "http", "icmp":
	default:
		kind = "icmp"
	}

	value := -1
	var latency int64
	var err error
	switch kind {
	case "tcp":
		latency, err = tcpPing(reference, timeout)
	case "http":
		latency, err = httpPing(reference, timeout)
	default:
		latency, err = icmpPing(reference, timeout)
	}
	if err == nil {
		value = int(latency)
	} else {
		log.Printf("reference probe task %d [%s] target=%s failed: %v", taskID, kind, reference, err)
	}
	uploadPingResult(conn, kind, v2.BuildPingResultPayloadWithRole(taskID, kind, "reference", value, time.Now()))
}

// runPingTask 是单个目标的实际测量逻辑（原 NewPingTask 主体）。
func runPingTask(conn *ws.SafeConn, taskID uint, pingType, pingTarget string) {
	if taskID == 0 {
		log.Printf("Invalid task ID: %d", taskID)
		return
	}
	// dual：同一周期内 ICMP 与 TCP 各测一次，上报两条结果。
	// 这条路径不参与单协议的测量/重试逻辑，直接返回。
	if pingType == "dual" {
		runDualPing(conn, taskID, pingTarget, 3*time.Second)
		return
	}
	// auto：先探一次，选一个该目标真正答应的协议；下游逻辑（含上报的 ping_type）
	// 统一使用解析后的值，因此面板上能看到实际采用的协议。
	if pingType == "auto" {
		resolvedKind, resolvedTarget := ResolveAuto(pingTarget)
		log.Printf("ping task %d: auto resolved target=%s -> type=%s target=%s", taskID, pingTarget, resolvedKind, resolvedTarget)
		pingType, pingTarget = resolvedKind, resolvedTarget
	}

	timeout := 3 * time.Second // 默认超时时间

	measure := func() (int64, error) {
		switch pingType {
		case "icmp":
			return icmpPing(pingTarget, timeout)
		case "tcp":
			return tcpPing(pingTarget, timeout)
		case "http":
			return httpPing(pingTarget, timeout)
		default:
			return -1, errors.New("unsupported ping type")
		}
	}

	pingResult := -1
	if latency, ok := measureWithRetries(taskID, pingType, measure); ok {
		pingResult = int(latency)
	}
	finishedAt := time.Now()
	wsPayload := v2.BuildPingResultPayload(taskID, pingType, pingResult, finishedAt)
	// https://github.com/komari-monitor/komari/commit/eb87a4fc330b7d1c407fa4ff70177615a4f50a1f
	// -1 代表丢包，服务端计算
	//if pingResult == -1 {
	//	return
	//}
	uploadPingResult(conn, pingType, wsPayload)
}

// 首次测量超过这个延迟就重试：Linux 的初始 RTO 是 1s，一次 SYN 重传会把
// 握手时间推到 1s 以上，这个值正好把「被重传撑大的测量」和「真的慢」分开。
const highLatencyThreshold = 1000

// 重试比首次快这么多，基本可以认定首次被 SYN 重传撑大了。
// 800ms = SYN/SYN-ACK 首次超时重传 1000ms - 防误判容许 200ms 延迟抖动。
const retryDropThresholdTcping = 800

// pingHighLatencyRetries 首次偏慢时的重试次数。
const pingHighLatencyRetries = 3

// tcpRetransmitSuspected 判断「首次偏慢、重试正常」是否属于 TCP 握手重传。
// 它只影响日志措辞 —— 无论结论如何，这次测量都是成功的。
func tcpRetransmitSuspected(pingType string, firstLatency, second int64) bool {
	return pingType == "tcp" && firstLatency-second > retryDropThresholdTcping
}

// measureWithRetries 执行一次测量，首次偏慢时按 highLatencyThreshold 重试，
// 返回最终应当上报的延迟以及本次是否成功。
//
// 这里曾经把「判定为 SYN 重传」当成失败（上报 -1 = 丢包）。那是错的：走到那个
// 分支时握手【已经完成】，重试还测到了真实 RTT。后果是成功的握手在面板上变成
// 整分钟 100% 丢包 —— 实测 OC424 对天津电信显示的 12% 丢包全部来自这一支，
// 而同一目标直连 30 次一次没丢。丢包只应表示「真的没连上」。
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
	for i := 0; i < pingHighLatencyRetries; i++ {
		second, retryErr := measure()
		if retryErr != nil {
			log.Printf("Ping task %d failed: %v", taskID, retryErr)
			return -1, false
		}
		if second <= highLatencyThreshold {
			if tcpRetransmitSuspected(pingType, firstLatency, second) {
				// 第一次的延迟被重传撑大了，不能采信；重试这次是真实的。
				log.Printf("Ping task %d: tcp handshake retransmitted (first=%dms retry=%dms), reporting the retry",
					taskID, firstLatency, second)
			}
			return second, true
		}
		if i == pingHighLatencyRetries-1 { // 最后一次仍然高
			log.Printf("Ping task %d failed: latency remains high after retries", taskID)
			return -1, false
		}
	}
	return -1, false
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
	if _, port, err := net.SplitHostPort(target); err == nil && port != "" {
		return target
	}
	autoMu.Lock()
	d, ok := autoCache[target]
	autoMu.Unlock()
	if ok && d.kind == "tcp" && d.target != "" {
		return d.target
	}
	for _, port := range []string{"443", "80"} {
		cand := net.JoinHostPort(target, port)
		if _, err := tcpPing(cand, autoProbeTimeout); err == nil {
			return cand
		}
	}
	return net.JoinHostPort(target, "443")
}

// runDualPing 执行「双协议并列探测」：同一周期内用 ICMP 和 TCP 各测一次目标，
// 分别上报两条结果（同一 task_id、不同 ping_type）。
//
// 为什么需要它：任务的探测协议是每个任务一个，选错就会得到恒定的 100% 丢包，
// 看起来像宕机，实际是协议不匹配。并列探测后，面板上会同时出现
// 「ICMP 100%」和「TCP 2ms」两组数据 —— 一眼就能区分「目标不答这个协议」
// 和「目标真的不可达」，不再需要人来推断。
func runDualPing(conn *ws.SafeConn, taskID uint, pingTarget string, timeout time.Duration) {
	legs := []struct {
		kind   string
		target string
	}{
		{"icmp", pingTarget},
		{"tcp", dualTCPTarget(pingTarget)},
	}

	for _, leg := range legs {
		value := -1
		var latency int64
		var err error
		switch leg.kind {
		case "icmp":
			latency, err = icmpPing(leg.target, timeout)
		case "tcp":
			latency, err = tcpPing(leg.target, timeout)
		}
		if err == nil {
			value = int(latency)
		} else {
			log.Printf("dual ping task %d [%s] target=%s failed: %v", taskID, leg.kind, leg.target, err)
		}
		// -1 表示丢包，由服务端统一换算丢包率
		uploadPingResult(conn, leg.kind, v2.BuildPingResultPayload(taskID, leg.kind, value, time.Now()))
	}
}

func postV2RPC(payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimSuffix(flags.Endpoint, "/") + "/api/clients/v2/rpc?token=" + flags.Token
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
