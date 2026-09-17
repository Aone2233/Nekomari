package ipinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// sourceGlobalping 是 Globalping 的名字。
const sourceGlobalping = "globalping.io"

// errLatencyBudgetExceeded 表示在预算内没等到测量完成。
var errLatencyBudgetExceeded = errors.New("globalping: measurement did not finish within the time budget")

// globalpingProbe 是测量结果里的单个探针。
type globalpingProbe struct {
	ASN     int
	Network string
	City    string
	Country string
	Status  string
	AvgMS   float64
	HasStat bool
}

// globalpingProvider 调用 Globalping 公共 API 做全球 ping 测量。
//
// 公共 API 可能限流、也可能要求 token（配置里可带），因此这里的任何失败都
// 只作为「延迟不可用」处理，绝不向上抛出成 HTTP 错误。
type globalpingProvider struct {
	baseURL      string
	client       *http.Client
	token        string
	pollInterval time.Duration
	maxWait      time.Duration
}

func newGlobalpingProvider(baseURL string, client *http.Client, token string, pollInterval, maxWait time.Duration) *globalpingProvider {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	if maxWait <= 0 {
		maxWait = 20 * time.Second
	}
	return &globalpingProvider{
		baseURL:      strings.TrimRight(baseURL, "/"),
		client:       client,
		token:        strings.TrimSpace(token),
		pollInterval: pollInterval,
		maxWait:      maxWait,
	}
}

func (p *globalpingProvider) Name() string { return sourceGlobalping }

// globalpingMeasurement 是创建/轮询接口的响应子集。
type globalpingMeasurement struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Results []struct {
		Probe struct {
			ASN     int    `json:"asn"`
			Network string `json:"network"`
			City    string `json:"city"`
			Country string `json:"country"`
		} `json:"probe"`
		Result struct {
			Status string `json:"status"`
			Stats  struct {
				Avg   float64 `json:"avg"`
				Min   float64 `json:"min"`
				Max   float64 `json:"max"`
				Total int     `json:"total"`
				Rcv   int     `json:"rcv"`
				Drop  int     `json:"drop"`
			} `json:"stats"`
			Timings []struct {
				TTL int     `json:"ttl"`
				RTT float64 `json:"rtt"`
			} `json:"timings"`
		} `json:"result"`
	} `json:"results"`
}

// measure 发起一次全球 ping 测量并轮询到结束，返回各探针结果。
// 整个过程受 ctx 与 maxWait 双重约束，超时返回已有结果之外的错误。
func (p *globalpingProvider) measure(ctx context.Context, ip net.IP) ([]globalpingProbe, error) {
	deadline := time.Now().Add(p.maxWait)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	measurement, err := p.create(ctx, ip)
	if err != nil {
		return nil, err
	}
	if measurement.Status == "finished" {
		return probesFromMeasurement(measurement), nil
	}

	for {
		if !time.Now().Before(deadline) {
			return nil, errLatencyBudgetExceeded
		}
		// 用 timer + select 而不是 time.Tick：不需要后台 goroutine，也不会泄漏。
		timer := time.NewTimer(p.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}

		current, err := p.fetch(ctx, measurement.ID)
		if err != nil {
			return nil, err
		}
		switch current.Status {
		case "finished":
			return probesFromMeasurement(current), nil
		case "failed":
			return nil, fmt.Errorf("globalping: measurement %s failed", measurement.ID)
		}
	}
}

func (p *globalpingProvider) create(ctx context.Context, ip net.IP) (*globalpingMeasurement, error) {
	body, err := json.Marshal(map[string]any{
		"type":   "ping",
		"target": ip.String(),
		"locations": []map[string]string{
			{"magic": "World"},
		},
		"limit": 10,
		"measurementOptions": map[string]any{
			"packets": 3,
		},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/measurements", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.authorize(req)

	var measurement globalpingMeasurement
	if _, err := doJSON(ctx, p.client, req, &measurement); err != nil {
		return nil, fmt.Errorf("globalping create: %w", err)
	}
	if measurement.ID == "" {
		return nil, errors.New("globalping create: response has no measurement id")
	}
	return &measurement, nil
}

func (p *globalpingProvider) fetch(ctx context.Context, id string) (*globalpingMeasurement, error) {
	req, err := newJSONRequest(ctx, p.baseURL+"/v1/measurements/"+id)
	if err != nil {
		return nil, err
	}
	p.authorize(req)

	var measurement globalpingMeasurement
	if _, err := doJSON(ctx, p.client, req, &measurement); err != nil {
		return nil, fmt.Errorf("globalping poll: %w", err)
	}
	return &measurement, nil
}

func (p *globalpingProvider) authorize(req *http.Request) {
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
}

// probesFromMeasurement 把 Globalping 的结果映射成内部探针结构。
func probesFromMeasurement(measurement *globalpingMeasurement) []globalpingProbe {
	probes := make([]globalpingProbe, 0, len(measurement.Results))
	for _, item := range measurement.Results {
		probe := globalpingProbe{
			ASN:     item.Probe.ASN,
			Network: item.Probe.Network,
			City:    item.Probe.City,
			Country: strings.ToUpper(strings.TrimSpace(item.Probe.Country)),
			Status:  item.Result.Status,
		}
		// avg 是首选；部分测量只给 min/max，再退到 timings 的平均值。
		switch {
		case item.Result.Stats.Avg > 0:
			probe.AvgMS, probe.HasStat = item.Result.Stats.Avg, true
		case item.Result.Stats.Min > 0:
			probe.AvgMS, probe.HasStat = item.Result.Stats.Min, true
		case len(item.Result.Timings) > 0:
			total := 0.0
			count := 0
			for _, timing := range item.Result.Timings {
				if timing.RTT > 0 {
					total += timing.RTT
					count++
				}
			}
			if count > 0 {
				probe.AvgMS, probe.HasStat = total/float64(count), true
			}
		}
		probes = append(probes, probe)
	}
	return probes
}

// latencyNodes 把探针结果映射成契约里的节点列表。
//
// 状态语义（契约硬要求，只能是这三个值）：
//   - ok：拿到了延迟；
//   - timeout：探针跑了但没收到回包；
//   - unavailable：探针没跑起来（offline / failed / 结果缺失）。
func latencyNodes(probes []globalpingProbe) []LatencyNode {
	nodes := make([]LatencyNode, 0, len(probes))
	for index, probe := range probes {
		node := LatencyNode{
			ID:   probeNodeID(probe, index),
			Name: probeNodeName(probe),
			City: probe.City,
			// Globalping 的 probe.country 是小写的 ISO 3166-1 alpha-2（"de"），
			// 而契约要求大写（主题用它拼国旗与地区名）。上游映射那一层已经归一过，
			// 但这里是「外部数据 -> 对外结构」的真正边界，直接构造 probe 的调用方
			// 会绕过那一层，所以在边界上再归一一次（幂等，不担心重复）。
			CountryCode: normalizeCountryCode(probe.Country),
			Status:      LatencyStatusUnavailable,
		}
		switch {
		case probe.HasStat && probe.AvgMS > 0:
			node.Status = LatencyStatusOK
			node.LatencyMS = roundLatency(probe.AvgMS)
		case probe.Status == "finished":
			// 跑完了却没有可用延迟 —— 全部丢包，按超时处理。
			node.Status = LatencyStatusTimeout
		default:
			node.Status = LatencyStatusUnavailable
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// roundLatency 把浮点延迟取整成毫秒。
//
// 契约示例给的是整数，为了同时兼容 number 与 integer 两种 schema，这里统一取整；
// 但取整后不能变成 0 —— 否则「ok 且 0ms」会看起来像没有数据。
func roundLatency(value float64) int {
	rounded := int(value + 0.5)
	if rounded == 0 {
		return 1
	}
	return rounded
}

// probeNodeID 优先用探针 ASN 作为稳定 ID，其次用探针序号。
func probeNodeID(probe globalpingProbe, index int) string {
	if probe.ASN > 0 {
		return fmt.Sprintf("AS%d", probe.ASN)
	}
	if probe.Network != "" {
		return probe.Network
	}
	return fmt.Sprintf("probe-%d", index)
}

// probeNodeName 给节点一个可读名称。
func probeNodeName(probe globalpingProbe) string {
	if probe.Network != "" {
		return probe.Network
	}
	if probe.City != "" {
		return probe.City
	}
	return probe.Country
}
