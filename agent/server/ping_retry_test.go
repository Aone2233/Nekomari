package server

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
)

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
		{"real-retransmit", "tcp", 1247, 230, true},
		{"both-slow", "tcp", 1289, 1278, false},
		{"drop-too-small", "tcp", 1276, 853, false},
		{"exactly-800", "tcp", 1100, 300, false}, // 差值必须严格大于阈值
		{"just-over-800", "tcp", 1101, 300, true},
		{"icmp-is-not-tcp", "icmp", 1247, 230, false},
		{"http-is-not-tcp", "http", 1247, 230, false},
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

// measureResult 是 stubMeasure 的脚本项。
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
		{latency: 1247},
		{latency: 230},
	})

	latency, ok := measureWithRetries(23, "tcp", measure)

	if !ok {
		t.Fatal("a completed handshake was reported as packet loss")
	}
	if latency != 230 {
		t.Errorf("latency = %d, want 230 (the retry, not the retransmitted first attempt)", latency)
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
				{latency: 238},
			},
			want: 238, wantOK: true,
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
				{latency: 1276},
				{latency: 853},
			},
			want: 853, wantOK: true,
		},
		{
			name: "every-attempt-stays-high-is-slow-not-lost",
			results: []measureResult{
				{latency: 1289},
				{latency: 1278},
				{latency: 1300},
				{latency: 1290},
			},
			want: 1290, wantOK: true,
		},
		{
			name: "retry-itself-fails",
			results: []measureResult{
				{latency: 1247},
				{latency: -1, err: errors.New("connection refused")},
			},
			want: -1, wantOK: false,
		},
		{
			name:     "icmp-slow-first-attempt-takes-the-retry",
			pingType: "icmp",
			results: []measureResult{
				{latency: 1500},
				{latency: 240},
			},
			want: 240, wantOK: true,
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
		{latency: 1289},
		{latency: 1278},
		{latency: 1300},
		{latency: 1290},
	})

	latency, ok := measureWithRetries(77, "http", measure)

	if !ok {
		t.Fatal("四次探测都成功却被上报为丢包（P1-6）：慢不等于丢")
	}
	if latency <= 0 {
		t.Fatalf("latency = %d；上报给面板的值必须为正——面板把 Value<0 记为 ping.loss=1", latency)
	}
	if latency != 1290 {
		t.Errorf("latency = %d, want 1290（最后一次成功探测的延迟，与 family 同源）", latency)
	}
	if want := 1 + pingHighLatencyRetries; *calls != want {
		t.Errorf("measure called %d times, want %d", *calls, want)
	}
}

// TestMeasureWithRetriesStillReportsLossOnRealFailure 是上一条的对照：放宽「慢」的
// 判定不能把「真失败」一起洗掉。首次与三次重试都慢、最后一次超时 → 仍然是丢包。
func TestMeasureWithRetriesStillReportsLossOnRealFailure(t *testing.T) {
	measure, _ := stubMeasure(t, []measureResult{
		{latency: 1500},
		{latency: 1600},
		{latency: 1700},
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
// runPingTask（task.go:527-538）把 measureWithRetries 的返回值放进 ping 结果载荷的
// value 字段，面板经 web/api/client/ingest.go:73-82 存成 PingRecord.Value。
//
// 面板规则（internal/metricstore/ping_records.go:61-64）只在 Value<0 时记
// ping.loss=1，internal/sla/availability.go:258 又以 loss>=0.5 记一次故障事件。
// 所以「慢但每次都成功」的探测必须以正数延迟出现在线缆上 —— 这条测试就是钉住它。
func TestSlowButSuccessfulProbeReachesThePanelAsLatencyNotLoss(t *testing.T) {
	measure, _ := stubMeasure(t, []measureResult{
		{latency: 1500},
		{latency: 1400},
		{latency: 1450},
		{latency: 1420},
	})

	// 与 runPingTask 的映射一致：只有 ok == false 才保留 -1（丢包）。
	pingResult := -1
	if latency, ok := measureWithRetries(79, "http", measure); ok {
		pingResult = int(latency)
	}

	raw, err := json.Marshal(v2.BuildPingResultPayloadWithRoleAndFamily(79, "http", "", "ipv4", pingResult, time.Now()))
	if err != nil {
		t.Fatalf("marshal ping payload: %v", err)
	}
	var wire struct {
		Method string `json:"method"`
		Params struct {
			Value int `json:"value"`
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
	if wire.Params.Value != pingResult || pingResult <= 0 {
		t.Fatalf("value = %d, pingResult = %d；必须上报正的真实延迟", wire.Params.Value, pingResult)
	}
}
