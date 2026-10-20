package server

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

// 本文件里的延迟一律是**微秒**：measure()/measureWithRetries() 的契约就是微秒。
// 注释中出现的毫秒值只是现场实测的对照，便于把数字读成"人类单位"。

// TestTcpRetransmitSuspected 固定住重传判定的三个边界。
//
// 这几个数字来自真实测量（OC424 -> 天津电信 IPv6，40 次连接）：
//
//	first=1247ms retry=230ms   重传，降幅 1017ms
//	first=1289ms retry=1278ms  两次都慢，不是重传
//	first=1276ms retry=853ms   降幅 423ms，够不到 800ms 的判定线
func TestTcpRetransmitSuspected(t *testing.T) {
	cases := []struct {
		name     string
		pingType string
		first    int64
		second   int64
		want     bool
	}{
		{"real-retransmit", "tcp", 1_247_000, 230_000, true},
		{"both-slow", "tcp", 1_289_000, 1_278_000, false},
		{"drop-too-small", "tcp", 1_276_000, 853_000, false},
		{"exactly-800", "tcp", 1_100_000, 300_000, false}, // 差值必须严格大于阈值
		{"just-over-800", "tcp", 1_101_000, 300_000, true},
		{"icmp-is-not-tcp", "icmp", 1_247_000, 230_000, false},
		{"http-is-not-tcp", "http", 1_247_000, 230_000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tcpRetransmitSuspected(tc.pingType, tc.first, tc.second); got != tc.want {
				t.Errorf("tcpRetransmitSuspected(%q, %d, %d) = %v, want %v",
					tc.pingType, tc.first, tc.second, got, tc.want)
			}
		})
	}
}

// measureResult 是 stubMeasure 的脚本项（延迟单位：微秒）。
type measureResult struct {
	latency int64
	err     error
}

// stubMeasure 按脚本返回结果，并把调用次数记下来。
func stubMeasure(t *testing.T, results []measureResult) (func() (int64, error), *int) {
	t.Helper()
	calls := 0
	return func() (int64, error) {
		if calls >= len(results) {
			t.Fatalf("measure called %d times, only %d results scripted", calls+1, len(results))
		}
		r := results[calls]
		calls++
		return r.latency, r.err
	}, &calls
}

// TestMeasureWithRetriesReportsSuccessAfterRetransmit 是本文件存在的理由。
//
// 一次已经完成的 TCP 握手被判定为 SYN 重传时，必须上报重试测到的 RTT，而不是
// 上报 -1（丢包）。曾经的实现上报 -1，于是海外探针到天津目标的每一次 SYN 重传
// 都在面板上变成整分钟 100% 丢包 —— OC424 显示的 12% 丢包全部来自这里，而同一
// 目标直连 30 次一次没丢。
func TestMeasureWithRetriesReportsSuccessAfterRetransmit(t *testing.T) {
	measure, calls := stubMeasure(t, []measureResult{
		{latency: 1_247_000},
		{latency: 230_000},
	})

	latency, ok := measureWithRetries(23, "tcp", measure)

	if !ok {
		t.Fatal("a completed handshake was reported as packet loss")
	}
	if latency != 230_000 {
		t.Errorf("latency = %d, want 230000 (the retry, not the retransmitted first attempt)", latency)
	}
	if *calls != 2 {
		t.Errorf("measure called %d times, want 2", *calls)
	}
}

// TestMeasureWithRetriesOtherPaths 覆盖其余分支。
//
// 注意 every-attempt-stays-high 一例的期望值已被 P1-6 修正：这一例原先期望
// (-1, false)，即把所有探测都成功、只是全部 >1000ms 的目标上报成丢包；那条断言
// 把缺陷固化成了契约。现在的期望是最后一次实测延迟（慢就是慢，丢包只表示没连上）。
func TestMeasureWithRetriesOtherPaths(t *testing.T) {
	cases := []struct {
		name     string
		pingType string
		results  []measureResult
		want     int64
		wantOK   bool
	}{
		{
			name: "fast-first-attempt-needs-no-retry",
			results: []measureResult{
				{latency: 238_000},
			},
			want: 238_000, wantOK: true,
		},
		{
			name: "first-attempt-fails",
			results: []measureResult{
				{latency: -1, err: errors.New("dial tcp: i/o timeout")},
			},
			want: -1, wantOK: false,
		},
		{
			name: "retry-succeeds-without-looking-like-a-retransmit",
			results: []measureResult{
				{latency: 1_276_000},
				{latency: 853_000},
			},
			want: 853_000, wantOK: true,
		},
		{
			name: "every-attempt-stays-high-is-slow-not-lost",
			results: []measureResult{
				{latency: 1_289_000},
				{latency: 1_278_000},
				{latency: 1_300_000},
				{latency: 1_290_000},
			},
			want: 1_290_000, wantOK: true,
		},
		{
			name: "retry-itself-fails",
			results: []measureResult{
				{latency: 1_247_000},
				{latency: -1, err: errors.New("connection refused")},
			},
			want: -1, wantOK: false,
		},
		{
			name:     "icmp-slow-first-attempt-takes-the-retry",
			pingType: "icmp",
			results: []measureResult{
				{latency: 1_500_000},
				{latency: 240_000},
			},
			want: 240_000, wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pingType := tc.pingType
			if pingType == "" {
				pingType = "tcp"
			}
			measure, _ := stubMeasure(t, tc.results)
			latency, ok := measureWithRetries(1, pingType, measure)
			if ok != tc.wantOK || latency != tc.want {
				t.Errorf("measureWithRetries = (%d, %v), want (%d, %v)", latency, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestMeasureWithRetriesNeverReportsLossWhenEveryProbeSucceeded 钉住 P1-6 的契约：
// 首次 + 3 次重试全部 err == nil 时，返回的必须是真实延迟（>0），而不是 -1。
//
// 面板侧后果：internal/metricstore/ping_records.go:61-64 把 Value<0 记为
// ping.loss=1，internal/sla/availability.go:258 又以 loss>=0.5 记一次故障事件。
// 所以这里返回 -1 就等于对一个完全通畅、只是慢的目标报 100% 丢包并触发 SLA 误报。
// 这不是罕见路径：httpPing 计的是整次 GET 的耗时，超过 1s 很常见。
//
// 全部用 stub，不需要 root、不需要网络，也不会被 CI 的
// -skip 'TestICMPPing|TestTCPPing|TestHTTPPing' 跳过。
func TestMeasureWithRetriesNeverReportsLossWhenEveryProbeSucceeded(t *testing.T) {
	measure, calls := stubMeasure(t, []measureResult{
		{latency: 1_289_000},
		{latency: 1_278_000},
		{latency: 1_300_000},
		{latency: 1_290_000},
	})

	latency, ok := measureWithRetries(77, "http", measure)

	if !ok {
		t.Fatal("四次探测都成功却被上报为丢包（P1-6）：慢不等于丢")
	}
	if latency <= 0 {
		t.Fatalf("latency = %d；上报给面板的值必须为正——面板把 Value<0 记为 ping.loss=1", latency)
	}
	if latency != 1_290_000 {
		t.Errorf("latency = %d, want 1290000（最后一次成功探测的延迟，与 family 同源）", latency)
	}
	if want := 1 + pingHighLatencyRetries; *calls != want {
		t.Errorf("measure called %d times, want %d", *calls, want)
	}
}

// TestMeasureWithRetriesStillReportsLossOnRealFailure 是上一条的对照：放宽「慢」的
// 判定不能把「真失败」一起洗掉。首次与三次重试都慢、最后一次超时 → 仍然是丢包。
func TestMeasureWithRetriesStillReportsLossOnRealFailure(t *testing.T) {
	measure, _ := stubMeasure(t, []measureResult{
		{latency: 1_500_000},
		{latency: 1_600_000},
		{latency: 1_700_000},
		{latency: -1, err: errors.New("dial tcp: i/o timeout")},
	})

	latency, ok := measureWithRetries(78, "http", measure)

	if ok {
		t.Fatalf("measure 返回 error（真超时）时必须上报丢包, got (%d, %v)", latency, ok)
	}
	if latency != -1 {
		t.Fatalf("latency = %d, want -1（丢包哨兵值）", latency)
	}
}

// TestSlowButSuccessfulProbeReachesThePanelAsLatencyNotLoss 把契约一路带到上报载荷上：
// runPingTask 把 measureWithRetries 的返回值放进 ping 结果载荷，面板把它存成
// PingRecord.Value（毫秒）。这里同时断言新加的 value_us：它是同一次测量的微秒值，
// 旧面板忽略它，新面板才能画出亚毫秒分辨率。
func TestSlowButSuccessfulProbeReachesThePanelAsLatencyNotLoss(t *testing.T) {
	measure, _ := stubMeasure(t, []measureResult{
		{latency: 1_500_000},
		{latency: 1_400_000},
		{latency: 1_450_000},
		{latency: 1_420_000},
	})

	// 与 runPingTask 的映射一致：测量是微秒，上报时同时给出毫秒与微秒。
	latencyUS, ok := measureWithRetries(79, "http", measure)
	value, valueUS := pingResultValues(latencyUS, ok)

	raw, err := json.Marshal(v2.BuildPingResultPayloadWithRoleFamilyAndMicros(79, "http", "", "ipv4", value, valueUS, time.Now()))
	if err != nil {
		t.Fatalf("marshal ping payload: %v", err)
	}
	var wire struct {
		Method string `json:"method"`
		Params struct {
			Value   int   `json:"value"`
			ValueUS int64 `json:"value_us"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal ping payload: %v", err)
	}
	if wire.Method != v2.MethodAgentPingResult {
		t.Fatalf("method = %q, want %q", wire.Method, v2.MethodAgentPingResult)
	}

	// 面板的换算规则，原样照抄以便在这里断言后果。
	loss := 0.0
	if wire.Params.Value < 0 {
		loss = 1
	}
	if loss != 0 {
		t.Fatalf("慢但成功的探测在线缆上是 value=%d，面板会记成 ping.loss=%v 并触发 SLA 故障事件", wire.Params.Value, loss)
	}
	if wire.Params.Value != 1420 || wire.Params.Value < 0 {
		t.Fatalf("value = %d，必须上报正的真实延迟（毫秒）", wire.Params.Value)
	}
	if wire.Params.ValueUS != 1_420_000 {
		t.Fatalf("value_us = %d, want 1420000（同一次测量的微秒值）", wire.Params.ValueUS)
	}
}

// 丢包时两个字段都用同一个 -1 哨兵：微秒字段不能靠"缺字段"表达失败，否则新面板要同时
// 处理"未知"和"丢包"两种含义。旧面板只读 value，行为与改动前完全一致。
func TestFailedProbeReportsTheLossSentinelInBothFields(t *testing.T) {
	measure, _ := stubMeasure(t, []measureResult{
		{latency: -1, err: errors.New("dial tcp: i/o timeout")},
	})
	latencyUS, ok := measureWithRetries(80, "tcp", measure)
	if ok || latencyUS != -1 {
		t.Fatalf("measureWithRetries = (%d, %v), want the loss sentinel", latencyUS, ok)
	}

	value, valueUS := pingResultValues(latencyUS, ok)
	if value != -1 || valueUS != -1 {
		t.Fatalf("pingResultValues = (%d, %d), want (-1, -1)", value, valueUS)
	}

	raw, err := json.Marshal(v2.BuildPingResultPayloadWithRoleFamilyAndMicros(80, "tcp", "", "ipv4", value, valueUS, time.Now()))
	if err != nil {
		t.Fatalf("marshal ping payload: %v", err)
	}
	var wire struct {
		Params struct {
			Value   int   `json:"value"`
			ValueUS int64 `json:"value_us"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal ping payload: %v", err)
	}
	if wire.Params.Value != -1 || wire.Params.ValueUS != -1 {
		t.Fatalf("loss payload = {value:%d value_us:%d}, want both -1", wire.Params.Value, wire.Params.ValueUS)
	}
}

// 微秒到毫秒的折算只做截断、不四舍五入：4.999ms 记 4ms，与改动前的 Milliseconds()
// 语义一致 —— 同一份历史不该因为换单位而"变好看"。
func TestPingResultValuesTruncateMicroseconds(t *testing.T) {
	cases := []struct {
		latencyUS   int64
		ok          bool
		wantValue   int
		wantValueUS int64
	}{
		{1_420_000, true, 1420, 1_420_000},
		{4_999, true, 4, 4_999},
		{999, true, 0, 999},
		{0, true, 0, 0},
		{-1, true, -1, -1},
		{1_234, false, -1, -1},
	}
	for _, tc := range cases {
		value, valueUS := pingResultValues(tc.latencyUS, tc.ok)
		if value != tc.wantValue || valueUS != tc.wantValueUS {
			t.Errorf("pingResultValues(%d, %v) = (%d, %d), want (%d, %d)",
				tc.latencyUS, tc.ok, value, valueUS, tc.wantValue, tc.wantValueUS)
		}
	}
}

// 日志用毫秒呈现，但内部计算一律微秒：这条断言防止 formatLatency 把量级写错。
func TestFormatLatencyRendersMicrosecondsAsMilliseconds(t *testing.T) {
	if got := formatLatency(1_247_000); got != "1247.0ms" {
		t.Fatalf("formatLatency(1247000) = %q, want 1247.0ms", got)
	}
	if got := formatLatency(-1); got != "-1" {
		t.Fatalf("formatLatency(-1) = %q, want the raw sentinel", got)
	}
}
