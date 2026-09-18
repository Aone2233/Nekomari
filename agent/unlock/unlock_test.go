package unlock

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fixtureTransport 按 host+path 返回预置响应，让探测逻辑可以脱离网络测试。
type fixtureTransport struct {
	routes map[string]func() (*http.Response, error)
	calls  map[string]int
}

func (f *fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.Host + req.URL.Path
	f.calls[key]++
	handler, ok := f.routes[key]
	if !ok {
		return nil, errors.New("no fixture for " + key)
	}
	return handler()
}

func bodyResponse(status int, body string) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}
}

func errResponse(message string) func() (*http.Response, error) {
	return func() (*http.Response, error) { return nil, errors.New(message) }
}

func newFixture(routes map[string]func() (*http.Response, error)) (*http.Client, *fixtureTransport) {
	transport := &fixtureTransport{routes: routes, calls: map[string]int{}}
	return &http.Client{Transport: transport}, transport
}

// traceBody 是 Cloudflare /cdn-cgi/trace 的真实形状（截取自实测响应）。
func traceBody(ip, loc string) string {
	return "fl=962f6\nh=chatgpt.com\nip=" + ip + "\nts=1789742997.000\n" +
		"visit_scheme=https\ncolo=SIN\nhttp=http/1.1\nloc=" + loc + "\ntls=TLSv1.3\nwarp=off\n"
}

// --- Netflix ------------------------------------------------------------------

func netflixRoutes(original, catalog func() (*http.Response, error)) map[string]func() (*http.Response, error) {
	return map[string]func() (*http.Response, error){
		"www.netflix.com/title/81280792": original,
		"www.netflix.com/title/80018499": catalog,
	}
}

// TestProbeNetflixFullUnlock 两个标题页都能看到 -> 完整解锁。
func TestProbeNetflixFullUnlock(t *testing.T) {
	client, _ := newFixture(netflixRoutes(
		bodyResponse(200, `<script>{"id":81280792,"title":"..."}</script>`),
		bodyResponse(200, `<script>{"id":80018499,"title":"..."}</script>`),
	))
	got := probeNetflix(context.Background(), client)
	if got.Status != Unlocked {
		t.Errorf("status = %q, want %q (detail: %s)", got.Status, Unlocked, got.Detail)
	}
	if got.Basis != BasisProbe {
		t.Errorf("basis = %q, want %q", got.Basis, BasisProbe)
	}
}

// TestProbeNetflixOriginalsOnly 只有自制剧可见 -> 部分解锁。
func TestProbeNetflixOriginalsOnly(t *testing.T) {
	client, _ := newFixture(netflixRoutes(
		bodyResponse(200, `<script>{"id":81280792}</script>`),
		bodyResponse(404, "<html>Not Found</html>"),
	))
	got := probeNetflix(context.Background(), client)
	if got.Status != Partial {
		t.Errorf("status = %q, want %q (detail: %s)", got.Status, Partial, got.Detail)
	}
}

// TestProbeNetflixBlocked 两个都看不到 -> 未解锁。
func TestProbeNetflixBlocked(t *testing.T) {
	client, _ := newFixture(netflixRoutes(
		bodyResponse(404, "<html>Not Found</html>"),
		bodyResponse(404, "<html>Not Found</html>"),
	))
	got := probeNetflix(context.Background(), client)
	if got.Status != Blocked {
		t.Errorf("status = %q, want %q (detail: %s)", got.Status, Blocked, got.Detail)
	}
}

// TestProbeNetflixTransientFailureIsUnknown 是这次实现里最容易搞错的一点。
//
// 实测 Netflix 的标题页会直接失败（连接被重置，只有 51 字节）—— 一次失败说明的是
// 网络，不是「未解锁」。所以请求失败必须报 unknown，重试之后仍然失败也一样。
func TestProbeNetflixTransientFailureIsUnknown(t *testing.T) {
	client, transport := newFixture(netflixRoutes(
		errResponse("read: connection reset by peer"),
		errResponse("read: connection reset by peer"),
	))
	got := probeNetflix(context.Background(), client)
	if got.Status != Unknown {
		t.Errorf("status = %q, want %q — a failed request is not evidence of a block", got.Status, Unknown)
	}
	if transport.calls["www.netflix.com/title/81280792"] != netflixAttempts {
		t.Errorf("original title fetched %d times, want %d retries",
			transport.calls["www.netflix.com/title/81280792"], netflixAttempts)
	}
}

// TestProbeNetflixRetriesThenSucceeds 一次失败、随后成功时不应误判。
func TestProbeNetflixRetriesThenSucceeds(t *testing.T) {
	attempt := 0
	flaky := func() (*http.Response, error) {
		attempt++
		if attempt == 1 {
			return nil, errors.New("read: connection reset by peer")
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"id":81280792}`)),
			Header:     make(http.Header),
		}, nil
	}
	client, _ := newFixture(netflixRoutes(
		flaky,
		bodyResponse(200, `<script>{"id":80018499}</script>`),
	))
	if got := probeNetflix(context.Background(), client); got.Status != Unlocked {
		t.Errorf("status = %q, want %q (detail: %s)", got.Status, Unlocked, got.Detail)
	}
}

// --- YouTube Premium ----------------------------------------------------------

// 这两个样本是实测抓到的：未封锁出口 ad-free 出现 17 次且没有那句提示，
// 被封锁出口 ad-free 只剩 4 次并出现提示一次。
const youtubeUnlockedBody = `{"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,` +
	`"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,` +
	`"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,"countryCode":"SG"}`

const youtubeBlockedBody = `{"ad-free":1,"ad-free":1,"ad-free":1,"ad-free":1,` +
	`"message":"Premium is not available in your country"}`

func TestProbeYouTubePremiumUnlocked(t *testing.T) {
	client, _ := newFixture(map[string]func() (*http.Response, error){
		"www.youtube.com/premium": bodyResponse(200, youtubeUnlockedBody),
	})
	got := probeYouTubePremium(context.Background(), client)
	if got.Status != Unlocked {
		t.Errorf("status = %q, want %q (detail: %s)", got.Status, Unlocked, got.Detail)
	}
	if got.Region != "SG" {
		t.Errorf("region = %q, want SG", got.Region)
	}
}

func TestProbeYouTubePremiumBlocked(t *testing.T) {
	client, _ := newFixture(map[string]func() (*http.Response, error){
		"www.youtube.com/premium": bodyResponse(200, youtubeBlockedBody),
	})
	got := probeYouTubePremium(context.Background(), client)
	if got.Status != Blocked {
		t.Errorf("status = %q, want %q (detail: %s)", got.Status, Blocked, got.Detail)
	}
}

// TestProbeYouTubePremiumMarkerIsNotAdFreeCount 守住那条踩过的坑：按 ad-free 出现
// 次数判断会判错，因为被封锁的页面里它同样存在（只是少）。只有那句明确提示算数。
func TestProbeYouTubePremiumMarkerIsNotAdFreeCount(t *testing.T) {
	// 内容里完全没有 ad-free，也没有封锁提示 —— 仍是可用。
	client, _ := newFixture(map[string]func() (*http.Response, error){
		"www.youtube.com/premium": bodyResponse(200, `{"countryCode":"US"}`),
	})
	if got := probeYouTubePremium(context.Background(), client); got.Status != Unlocked {
		t.Errorf("status = %q, want %q", got.Status, Unlocked)
	}
}

// --- 只判断地区的服务 -----------------------------------------------------------

// TestProbeRegionOnlyMarksBasisRegion 面板必须能区分「实测到了」与「只是地区符合」，
// 所以这两个服务的 Basis 只能是 region。
func TestProbeRegionOnlyMarksBasisRegion(t *testing.T) {
	client, _ := newFixture(map[string]func() (*http.Response, error){
		"chatgpt.com/cdn-cgi/trace": bodyResponse(200, traceBody("203.0.113.9", "SG")),
	})
	got := probeChatGPT(context.Background(), client, "")
	if got.Status != Unlocked || got.Basis != BasisRegion || got.Region != "SG" {
		t.Errorf("got status=%q basis=%q region=%q, want unlocked/region/SG", got.Status, got.Basis, got.Region)
	}
	if !strings.Contains(got.Detail, "未验证") {
		t.Errorf("detail should say the reachability was not verified, got %q", got.Detail)
	}
}

// TestProbeRegionOnlyFallsBackToEgressRegion trace 读不到时用整体出口地区兜底。
func TestProbeRegionOnlyFallsBackToEgressRegion(t *testing.T) {
	client, _ := newFixture(map[string]func() (*http.Response, error){
		"claude.ai/cdn-cgi/trace": errResponse("dial tcp: i/o timeout"),
	})
	got := probeClaude(context.Background(), client, "JP")
	if got.Status != Unlocked || got.Region != "JP" {
		t.Errorf("got status=%q region=%q, want unlocked/JP", got.Status, got.Region)
	}
}

// --- 整轮探测 -----------------------------------------------------------------

// TestProbeReportsEgressAddress 出口地址必须随结果一起上报。
//
// 本机群里有主机是经由另一台节点出网的（实测某台中国的探针 trace 回来的 ip 是另一台
// 美国节点的地址），不带上出口地址，面板就会把结果算到错误的节点上。
func TestProbeReportsEgressAddress(t *testing.T) {
	client, _ := newFixture(map[string]func() (*http.Response, error){
		"chatgpt.com/cdn-cgi/trace":      bodyResponse(200, traceBody("192.0.2.55", "US")),
		"www.netflix.com/title/81280792": bodyResponse(200, `{"id":81280792}`),
		"www.netflix.com/title/80018499": bodyResponse(200, `{"id":80018499}`),
		"www.youtube.com/premium":        bodyResponse(200, youtubeUnlockedBody),
		"claude.ai/cdn-cgi/trace":        bodyResponse(200, traceBody("192.0.2.55", "US")),
	})
	report := Probe(context.Background(), client)

	if report.EgressIP != "192.0.2.55" || report.EgressRegion != "US" {
		t.Errorf("egress = %q/%q, want 192.0.2.55/US", report.EgressIP, report.EgressRegion)
	}
	if len(report.Results) != 4 {
		t.Fatalf("got %d results, want 4", len(report.Results))
	}
	for _, result := range report.Results {
		if result.Status == "" {
			t.Errorf("result %s has an empty status", result.ID)
		}
		if result.Kind != Media && result.Kind != AI {
			t.Errorf("result %s has kind %q", result.ID, result.Kind)
		}
	}
	if report.ProbedAt.IsZero() {
		t.Error("ProbedAt was not set")
	}
}
