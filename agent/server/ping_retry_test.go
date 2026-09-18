package server

import (
	"errors"
	"testing"
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

// TestMeasureWithRetriesOtherPaths 覆盖其余分支，确认重构没有改变原有语义。
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
			name: "every-attempt-stays-high",
			results: []measureResult{
				{latency: 1289},
				{latency: 1278},
				{latency: 1300},
				{latency: 1290},
			},
			want: -1, wantOK: false,
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
