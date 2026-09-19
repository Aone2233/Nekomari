// Package unlock probes whether a host's outbound path can reach region-gated
// services, for the "流媒体解锁 / AI 解锁" section of the IP info panel.
//
// What this can and cannot answer
// -------------------------------
// Two different questions get called "unlock":
//
//  1. Is the exit region supported by the service at all? This is reliable.
//     Cloudflare's /cdn-cgi/trace answers from any host with loc=XX and is not behind
//     the bot rule, so it is the one region signal worth trusting.
//
//  2. Can this particular IP actually use the service? Not reliably answerable from a
//     plain HTTP client. chatgpt.com and claude.ai return 403 to a datacenter IP
//     regardless of country (Cloudflare bot protection), and a challenge 403 is
//     indistinguishable from a country block without driving a browser.
//
// So every result carries the evidence that produced it, and anything undetermined is
// reported as unknown rather than guessed. The services kept here are the ones where a
// real signal exists -- verified against both an unblocked and a blocked probe:
//
//	Netflix           title-page id present or absent. The request fails outright often
//	                  enough that a single failure proves nothing, hence the retries.
//	YouTube Premium   "countryCode":"XX" plus the explicit "Premium is not available"
//	                  marker. The unblocked probe had ad-free x17 and no marker; the
//	                  blocked one had ad-free x4 and the marker exactly once.
//	ChatGPT / Claude  exit region from Cloudflare trace, which is a policy signal
//	                  rather than a reachability test -- reported as such.
//
// The exit IP travels with the results because it is not always the node's own
// address: one host in this fleet reaches the internet through another node, and
// without the egress address the panel would credit the result to the wrong place.
package unlock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Status is the verdict for one service.
type Status string

const (
	// Unlocked 有证据表明该服务在此出口可用。
	Unlocked Status = "unlocked"
	// Partial 部分可用（目前只有 Netflix「仅自制剧」会得到这个结论）。
	Partial Status = "partial"
	// Blocked 有证据表明不可用。
	Blocked Status = "blocked"
	// Unknown 无法判断 —— 请求失败、被风控拦截，或该服务没有可用信号。
	Unknown Status = "unknown"
)

// Kind 把服务分成流媒体与 AI 两组，面板按组显示。
type Kind string

const (
	Media Kind = "media"
	AI    Kind = "ai"
)

// Basis 说明结论是怎么来的，面板据此提示可信度。
const (
	// BasisProbe 真的请求到了服务本身，并读到了明确信号。
	BasisProbe = "probe"
	// BasisRegion 只判断了出口地区，没有验证该 IP 是否真的能用。
	BasisRegion = "region"
)

// Result 是单个服务的结论。
type Result struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   Kind   `json:"kind"`
	Status Status `json:"status"`
	Region string `json:"region,omitempty"`
	Basis  string `json:"basis"`
	Detail string `json:"detail,omitempty"`
}

// Report 是一次完整探测的结果。
type Report struct {
	// EgressIP 是本次探测实际使用的出口地址，取自 Cloudflare trace 的 ip=。
	// 它不一定等于节点自己的地址：本机群里有主机是经由另一台节点出网的。
	EgressIP string `json:"egress_ip,omitempty"`
	// EgressRegion 是出口地区码，取自同一个 trace 的 loc=。
	EgressRegion string    `json:"egress_region,omitempty"`
	ProbedAt     time.Time `json:"probed_at"`
	Results      []Result  `json:"results"`
}

const (
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	requestTimeout = 15 * time.Second
	// netflixAttempts 是 Netflix 单页的重试次数。实测该请求会直接失败（连接被重置，
	// 只有 51 字节），一次失败不能当作「未解锁」，所以必须重试后再下结论。
	netflixAttempts = 3
	maxBodyBytes    = 2 << 20
)

var locPattern = regexp.MustCompile(`(?m)^loc=(\S+)`)
var ipPattern = regexp.MustCompile(`(?m)^ip=(\S+)`)
var countryCodePattern = regexp.MustCompile(`"countryCode":"([A-Z]{2})"`)

// NewClient 返回探测用的 HTTP 客户端。
func NewClient() *http.Client {
	return &http.Client{Timeout: requestTimeout}
}

// get 取一个 URL，返回状态码与正文。非 2xx 不当作错误 —— 403 本身就是信息。
func get(ctx context.Context, client *http.Client, url string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(body), nil
}

// trace 读 Cloudflare 的 /cdn-cgi/trace，返回出口地址与地区码。
// 这是唯一一个不设防、任何出口都能拿到地区信息的端点。
func trace(ctx context.Context, client *http.Client, host string) (ip, loc string, err error) {
	status, body, err := get(ctx, client, "https://"+host+"/cdn-cgi/trace")
	if err != nil {
		return "", "", err
	}
	if status != http.StatusOK {
		return "", "", fmt.Errorf("%s/cdn-cgi/trace returned %d", host, status)
	}
	if m := ipPattern.FindStringSubmatch(body); m != nil {
		ip = m[1]
	}
	if m := locPattern.FindStringSubmatch(body); m != nil {
		loc = m[1]
	}
	if loc == "" {
		return ip, "", errors.New("trace response carried no loc=")
	}
	return ip, loc, nil
}

// Probe 依次探测所有服务。ctx 控制整体预算；单个服务失败只影响它自己。
func Probe(ctx context.Context, client *http.Client) Report {
	report := Report{ProbedAt: time.Now().UTC(), Results: make([]Result, 0, 4)}

	// 出口信息用 ChatGPT 的 trace 读一次：它属于被风控的那批域名，但 trace 端点本身
	// 不设防，所以既能拿到真实出口，又不会因为 403 而失败。
	if ip, loc, err := trace(ctx, client, "chatgpt.com"); err == nil {
		report.EgressIP, report.EgressRegion = ip, loc
	} else if ip, loc, err := trace(ctx, client, "www.cloudflare.com"); err == nil {
		report.EgressIP, report.EgressRegion = ip, loc
	}

	report.Results = append(report.Results,
		probeNetflix(ctx, client),
		probeYouTubePremium(ctx, client),
		probeChatGPT(ctx, client, report.EgressRegion),
		probeClaude(ctx, client, report.EgressRegion),
	)
	return report
}

// probeNetflix 用两个标题页判断：81280792 是自制剧，80018499 是授权片库。
// 只有自制剧可见 -> 部分解锁（社区惯例里的「仅自制剧」）。
func probeNetflix(ctx context.Context, client *http.Client) Result {
	result := Result{ID: "netflix", Name: "Netflix", Kind: Media, Basis: BasisProbe}

	original, originalErr := netflixTitleVisible(ctx, client, "81280792")
	catalog, catalogErr := netflixTitleVisible(ctx, client, "80018499")

	switch {
	case originalErr != nil || catalogErr != nil:
		result.Status = Unknown
		result.Detail = "标题页请求失败，无法判断"
	case original && catalog:
		result.Status = Unlocked
		result.Detail = "自制剧与片库均可访问"
	case original:
		result.Status = Partial
		result.Detail = "仅自制剧可访问"
	default:
		result.Status = Blocked
		result.Detail = "标题页不可访问"
	}
	return result
}

// netflixTitleVisible 判断某个标题页是否真的返回了该标题。
// 只有重试都失败才返回错误 —— 实测这个请求会直接失败，一次失败不能当证据。
func netflixTitleVisible(ctx context.Context, client *http.Client, titleID string) (bool, error) {
	url := "https://www.netflix.com/title/" + titleID
	var lastErr error
	for attempt := 0; attempt < netflixAttempts; attempt++ {
		status, body, err := get(ctx, client, url)
		if err != nil {
			lastErr = err
			continue
		}
		if status == http.StatusOK {
			return strings.Contains(body, titleID), nil
		}
		// 404 是明确答复：该标题在这个出口不可用，不是网络失败。把 404 当失败去重试，
		// 会把「被封锁」误报成「无法判断」—— 这正是本函数此前的行为，也是这两个用例
		// 一直失败、而 CI 从不运行它们所掩盖的问题。
		if status == http.StatusNotFound {
			return false, nil
		}
		lastErr = fmt.Errorf("status %d", status)
	}
	return false, lastErr
}

// probeYouTubePremium 读 /premium：地区码来自 "countryCode":"XX"，是否可用来自
// 明确写出的 "Premium is not available"。
//
// 这两个标记是对着真实样本定的：未封锁的出口 ad-free 出现 17 次且没有该句，被封锁
// 的出口 ad-free 只剩 4 次并出现该句一次。只按 ad-free 计数会判错。
func probeYouTubePremium(ctx context.Context, client *http.Client) Result {
	result := Result{ID: "youtube", Name: "YouTube Premium", Kind: Media, Basis: BasisProbe}

	status, body, err := get(ctx, client, "https://www.youtube.com/premium")
	if err != nil {
		result.Status = Unknown
		result.Detail = "请求失败：" + err.Error()
		return result
	}
	if m := countryCodePattern.FindStringSubmatch(body); m != nil {
		result.Region = m[1]
	}
	switch {
	case status != http.StatusOK:
		result.Status = Unknown
		result.Detail = fmt.Sprintf("返回 %d，无法判断", status)
	case strings.Contains(body, "Premium is not available"):
		result.Status = Blocked
		result.Detail = "该地区不提供 Premium"
	default:
		result.Status = Unlocked
		result.Detail = "可访问 Premium 页面"
	}
	return result
}

// probeChatGPT 与 probeClaude 只能判断出口地区，所以 Basis 标成 region 而不是
// probe：面板要能区分「实测到了」和「只是地区符合」。风控 403 不当作封锁 —— 数据中心
// 出口几乎必然拿到它，那说明的是 Cloudflare 而不是这个 IP 能不能用 ChatGPT。
func probeChatGPT(ctx context.Context, client *http.Client, fallbackRegion string) Result {
	return probeRegionOnly(ctx, client, "chatgpt.com", "chatgpt", "ChatGPT", AI, fallbackRegion)
}

func probeClaude(ctx context.Context, client *http.Client, fallbackRegion string) Result {
	return probeRegionOnly(ctx, client, "claude.ai", "claude", "Claude", AI, fallbackRegion)
}

func probeRegionOnly(ctx context.Context, client *http.Client, host, id, name string, kind Kind, fallback string) Result {
	result := Result{ID: id, Name: name, Kind: kind, Basis: BasisRegion}

	_, loc, err := trace(ctx, client, host)
	if err != nil {
		if fallback == "" {
			result.Status = Unknown
			result.Detail = "读不到出口地区"
			return result
		}
		loc = fallback
	}
	result.Region = loc
	result.Status = Unlocked
	result.Detail = fmt.Sprintf("出口地区 %s（按地区判断，未验证该 IP 是否真的可用）", loc)
	return result
}
