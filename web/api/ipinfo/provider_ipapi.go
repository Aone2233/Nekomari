package ipinfo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sourceIPAPI 是 ip-api.com 的名字。
const sourceIPAPI = "ip-api.com"

// ipAPIDefaultCooldown 是上游没有给出 X-Ttl 时的默认冷却时长。
const ipAPIDefaultCooldown = 60 * time.Second

// ipAPIMaxCooldown 是冷却时长的上限：万一上游返回一个荒唐的 X-Ttl，
// 也不能让本进程此后一直拒绝查询。
const ipAPIMaxCooldown = time.Hour

// errIPAPIRateLimited 表示 ip-api.com 正处于限流冷却期。
var errIPAPIRateLimited = errors.New("ip-api.com rate limited")

// ipAPIFlags 是 ip-api.com 免费接口能提供的布尔信号。
// 该接口不报告 tor / vpn / abuser / crawler，这几项在上层固定为 false。
type ipAPIFlags struct {
	Query   string
	Hosting bool
	Mobile  bool
	Proxy   bool
}

// ipAPIProvider 调用 http://ip-api.com/json/<ip>?fields=...
//
// 两点必须注意：
//  1. 免费接口只支持明文 HTTP（没有 HTTPS），所以 baseURL 默认是 http://；
//  2. 免费额度约 45 次/分钟，限流时返回 HTTP 429，或在正常响应里给出
//     X-Rl: 0 与 X-Ttl: <秒>。这里记住冷却截止时间，冷却期内直接短路成
//     「不可用」，不再打上游 —— 否则会被一直限流，还会拖慢整个查询。
type ipAPIProvider struct {
	baseURL string
	client  *http.Client
	now     func() time.Time

	mu             sync.Mutex
	cooldownUntil  time.Time
	cooldownReason string
}

func newIPAPIProvider(baseURL string, client *http.Client, now func() time.Time) *ipAPIProvider {
	if now == nil {
		now = time.Now
	}
	return &ipAPIProvider{baseURL: strings.TrimRight(baseURL, "/"), client: client, now: now}
}

func (p *ipAPIProvider) Name() string { return sourceIPAPI }

func (p *ipAPIProvider) lookup(ctx context.Context, ip net.IP) (*ipAPIFlags, error) {
	if remaining, reason, limited := p.cooldown(); limited {
		return nil, fmt.Errorf("%w: %s; %s remaining", errIPAPIRateLimited, reason, remaining.Round(time.Second))
	}

	url := fmt.Sprintf("%s/json/%s?fields=status,message,query,hosting,mobile,proxy", p.baseURL, ip.String())
	req, err := newJSONRequest(ctx, url)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Query   string `json:"query"`
		Hosting bool   `json:"hosting"`
		Mobile  bool   `json:"mobile"`
		Proxy   bool   `json:"proxy"`
	}
	resp, err := doJSON(ctx, p.client, req, &payload)
	if resp != nil {
		// 无论成败都先看限流头：429 会带 X-Ttl，正常响应可能带 X-Rl: 0。
		p.applyRateLimitHeaders(resp)
	}
	if err != nil {
		return nil, fmt.Errorf("ip-api.com: %w", err)
	}
	if payload.Status != "success" {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "lookup failed"
		}
		return nil, fmt.Errorf("ip-api.com: %s", message)
	}

	return &ipAPIFlags{
		Query:   payload.Query,
		Hosting: payload.Hosting,
		Mobile:  payload.Mobile,
		Proxy:   payload.Proxy,
	}, nil
}

// cooldown 返回剩余冷却时间与原因；limited=false 表示可以正常查询。
func (p *ipAPIProvider) cooldown() (time.Duration, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cooldownUntil.IsZero() {
		return 0, "", false
	}
	remaining := p.cooldownUntil.Sub(p.now())
	if remaining <= 0 {
		p.cooldownUntil = time.Time{}
		p.cooldownReason = ""
		return 0, "", false
	}
	return remaining, p.cooldownReason, true
}

// cooldownUntilTime 供测试与诊断读取当前冷却截止时间。
func (p *ipAPIProvider) cooldownUntilTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cooldownUntil
}

func (p *ipAPIProvider) applyRateLimitHeaders(resp *http.Response) {
	ttl := parseHeaderSeconds(resp.Header.Get("X-Ttl"))

	if resp.StatusCode == http.StatusTooManyRequests {
		p.startCooldown(ttl, "upstream returned HTTP 429")
		return
	}
	// X-Rl 是「本窗口剩余可用次数」，为 0 说明下一次就会被限流，提前进入冷却。
	if strings.TrimSpace(resp.Header.Get("X-Rl")) == "0" {
		p.startCooldown(ttl, "upstream reported no remaining quota (X-Rl: 0)")
	}
}

func (p *ipAPIProvider) startCooldown(seconds int, reason string) {
	duration := time.Duration(seconds) * time.Second
	if duration <= 0 {
		duration = ipAPIDefaultCooldown
	}
	if duration > ipAPIMaxCooldown {
		duration = ipAPIMaxCooldown
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	until := p.now().Add(duration)
	// 已经在更晚的冷却期内就不要缩短它。
	if until.After(p.cooldownUntil) {
		p.cooldownUntil = until
		p.cooldownReason = reason
	}
}

// parseHeaderSeconds 解析上游的秒数响应头，非法值返回 0。
func parseHeaderSeconds(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}
