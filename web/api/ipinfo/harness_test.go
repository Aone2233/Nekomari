package ipinfo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// testClock 是可手动推进的时钟：TTL、stale 与 ip-api.com 冷却都靠它来测，
// 测试里不出现任何 sleep。
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeUpstreams 用一个 httptest.Server 伪造四个上游。所有测试都只打这个假服务，
// 全程不发真实网络请求。
type fakeUpstreams struct {
	server *httptest.Server

	mu         sync.Mutex
	hits       map[string]int
	geo        http.HandlerFunc
	ripe       http.HandlerFunc
	ipapi      http.HandlerFunc
	globalping http.HandlerFunc
}

func newFakeUpstreams(t *testing.T) *fakeUpstreams {
	t.Helper()

	f := &fakeUpstreams{hits: map[string]int{}}
	f.geo = f.defaultGeo
	f.ripe = f.defaultRipe
	f.ipapi = f.defaultIPAPI
	f.globalping = f.defaultGlobalping

	mux := http.NewServeMux()
	mux.HandleFunc("/ipwhois/", f.route("geo", func() http.HandlerFunc { return f.geoHandler() }))
	mux.HandleFunc("/ripestat/", f.route("ripe", func() http.HandlerFunc { return f.ripeHandler() }))
	mux.HandleFunc("/ipapi/", f.route("ipapi", func() http.HandlerFunc { return f.ipapiHandler() }))
	mux.HandleFunc("/globalping/", f.route("globalping", func() http.HandlerFunc { return f.globalpingHandler() }))

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeUpstreams) route(name string, pick func() http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[name]++
		f.mu.Unlock()
		pick()(w, r)
	}
}

func (f *fakeUpstreams) hitsFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

func (f *fakeUpstreams) setGeo(handler http.HandlerFunc) { f.mu.Lock(); f.geo = handler; f.mu.Unlock() }
func (f *fakeUpstreams) setRipe(handler http.HandlerFunc) {
	f.mu.Lock()
	f.ripe = handler
	f.mu.Unlock()
}
func (f *fakeUpstreams) setIPAPI(handler http.HandlerFunc) {
	f.mu.Lock()
	f.ipapi = handler
	f.mu.Unlock()
}
func (f *fakeUpstreams) setGlobalping(handler http.HandlerFunc) {
	f.mu.Lock()
	f.globalping = handler
	f.mu.Unlock()
}

func (f *fakeUpstreams) geoHandler() http.HandlerFunc { f.mu.Lock(); defer f.mu.Unlock(); return f.geo }
func (f *fakeUpstreams) ripeHandler() http.HandlerFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ripe
}
func (f *fakeUpstreams) ipapiHandler() http.HandlerFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ipapi
}
func (f *fakeUpstreams) globalpingHandler() http.HandlerFunc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.globalping
}

// failWith 生成一个固定错误状态的 handler，用来模拟某个上游挂掉。
func failWith(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"upstream down"}`))
	}
}

// writeJSON 生成一个返回固定 JSON 的 handler。
func writeJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// geoBodyFor 生成指定国家码/国家名的 ipwho.is 响应。
func geoBodyFor(countryName, countryCode string) string {
	return fmt.Sprintf(`{
		"ip":"1.2.3.4","success":true,"type":"IPv4",
		"continent":"North America","continent_code":"NA",
		"country":%q,"country_code":%q,
		"region":"California","region_code":"CA","city":"Los Angeles","postal":"90001",
		"latitude":34.0522,"longitude":-118.2437,
		"timezone":{"id":"America/Los_Angeles"},
		"connection":{"asn":15169,"isp":"Google LLC","org":"Google LLC","domain":"google.com"}
	}`, countryName, countryCode)
}

// ripeWhoisBody 生成 whois 响应，用于切换「注册国家」与 RIR。
//
// 形态对齐 RIPEstat /data/whois 的真实结构：records 是「记录组的数组」，
// 每组里是 {key,value} 列表；ARIN 用 "Country"，APNIC 用小写 "country"。
func ripeWhoisBody(countryCode, rir string) string {
	records := `[{"key":"ASNumber","value":"15169"},{"key":"ASName","value":"TEST-AS"},{"key":"source","value":` +
		strconv.Quote(rir) + `}]`
	if countryCode != "" {
		records = `[{"key":"OrgName","value":"Test Org"},{"key":"Country","value":` + strconv.Quote(countryCode) +
			`},{"key":"source","value":` + strconv.Quote(rir) + `}]`
	}
	return `{"data":{"records":[` + records + `]}}`
}

// defaultGeo 是 ipwho.is 的正常响应；国家码固定 US，与 whois 的 US 相同，
// 因此默认场景下分类为 native。
func (f *fakeUpstreams) defaultGeo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(geoBodyFor("United States", "US")))
}

// defaultRipe 按路径分派 RIPEstat 的接口。
//
// network-info 的 asns 刻意用线上真实的字符串形态（["15169"]）：
// 这里曾经用 []int 解码，遇到真实响应会整段失败。
func (f *fakeUpstreams) defaultRipe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.Contains(r.URL.Path, "network-info"):
		_, _ = w.Write([]byte(`{"data":{"prefix":"1.2.3.0/24","asns":["15169"]}}`))
	case strings.Contains(r.URL.Path, "whois"):
		_, _ = w.Write([]byte(ripeWhoisBody("US", "ARIN")))
	default:
		// 默认没有 PTR 记录：domain 由 ipwho.is 提供。
		_, _ = w.Write([]byte(`{"data":{"name":"","result":[]}}`))
	}
}

func (f *fakeUpstreams) defaultIPAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Rl", "40")
	w.Header().Set("X-Ttl", "60")
	_, _ = w.Write([]byte(`{"status":"success","query":"1.2.3.4","hosting":false,"mobile":false,"proxy":false}`))
}

// defaultGlobalping 复刻线上行为：创建测量返回 202 Accepted + {id, probesCount}，
// 之后轮询返回 finished。
func (f *fakeUpstreams) defaultGlobalping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		// 线上实测是 202 而不是 200：早期实现只认 200，导致延迟面板永远为空。
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"measure-1","probesCount":3}`))
		return
	}
	_, _ = w.Write([]byte(`{"id":"measure-1","status":"finished","results":[
		{"probe":{"asn":15169,"network":"Google LLC","city":"Frankfurt","country":"DE"},
		 "result":{"status":"finished","stats":{"avg":12.34,"min":11,"max":14,"total":3,"rcv":3,"drop":0}}},
		{"probe":{"asn":24940,"network":"Hetzner Online GmbH","city":"Nuremberg","country":"DE"},
		 "result":{"status":"finished","stats":{"avg":0,"min":0,"max":0,"total":3,"rcv":0,"drop":3}}},
		{"probe":{"asn":9009,"network":"M247 Europe SRL","city":"Bucharest","country":"RO"},
		 "result":{"status":"offline"}}
	]}`))
}

// serviceConfig 指向假上游，其余参数保持默认（TTL 用注入时钟推进）。
func (f *fakeUpstreams) serviceConfig(clock *testClock) Config {
	cfg := DefaultConfig()
	cfg.IPWhoIsBaseURL = f.server.URL + "/ipwhois"
	cfg.RIPEstatBaseURL = f.server.URL + "/ripestat"
	cfg.IPAPIBaseURL = f.server.URL + "/ipapi"
	cfg.GlobalpingBaseURL = f.server.URL + "/globalping"
	cfg.GlobalpingPollInterval = 2 * time.Millisecond
	cfg.GlobalpingMaxWait = 200 * time.Millisecond
	// DefaultConfig 会读环境变量，这里显式打开，避免受运行环境影响。
	cfg.GlobalLatencyDisabled = false
	cfg.Now = clock.Now
	return cfg
}

func newTestHandler(t *testing.T) (*Handler, *fakeUpstreams, *testClock) {
	t.Helper()
	f := newFakeUpstreams(t)
	clock := newTestClock()
	return &Handler{svc: NewService(f.serviceConfig(clock))}, f, clock
}

// newTestHandlerWithConfig 允许单个用例改写配置（例如缩短 Globalping 预算）。
func newTestHandlerWithConfig(t *testing.T, mutate func(*Config)) (*Handler, *fakeUpstreams, *testClock) {
	t.Helper()
	f := newFakeUpstreams(t)
	clock := newTestClock()
	cfg := f.serviceConfig(clock)
	if mutate != nil {
		mutate(&cfg)
	}
	return &Handler{svc: NewService(cfg)}, f, clock
}

// newTestRouter 复刻 router.go 里的注册方式（含管理接口的 RequireRole 位置），
// 但不引入真实路由表，避免测试依赖主题静态资源。
func newTestRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/public/ip-info/v1/status", h.Status)
	r.GET("/api/public/ip-info/v1/lookup", h.Lookup)
	r.GET("/api/public/ip-info/v1/latency", h.Latency)
	r.POST("/api/admin/ip-info/v1/refresh", h.Refresh)
	return r
}

func doRequest(t *testing.T, r *gin.Engine, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)
	return recorder
}

// --- 契约断言工具 ---

func decodeSuccess(t *testing.T, recorder *httptest.ResponseRecorder) (map[string]any, map[string]any, map[string]any) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	var root map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, recorder.Body.String())
	}
	if ok, _ := root["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %v", root["ok"])
	}

	data, ok := root["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data to be an object, got %T", root["data"])
	}
	meta, ok := root["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta to be an object, got %T", root["meta"])
	}
	return root, data, meta
}

func decodeError(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	if recorder.Code < 400 {
		t.Fatalf("expected non-2xx status, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var root map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &root); err != nil {
		t.Fatalf("error response is not valid JSON: %v (%s)", err, recorder.Body.String())
	}
	body, ok := root["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %T (%s)", root["error"], recorder.Body.String())
	}
	message, ok := body["message"].(string)
	if !ok || message == "" {
		t.Fatalf("expected error.message to be a non-empty string, got %v", body["message"])
	}
	return message
}

func requireKeys(t *testing.T, obj map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, exists := obj[key]; !exists {
			t.Errorf("missing required key %q", key)
		}
	}
}

func asObject(t *testing.T, obj map[string]any, key string) map[string]any {
	t.Helper()
	value, exists := obj[key]
	if !exists {
		t.Fatalf("missing required object %q", key)
	}
	child, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%q must be an object, got %T", key, value)
	}
	return child
}

func asArray(t *testing.T, obj map[string]any, key string) []any {
	t.Helper()
	value, exists := obj[key]
	if !exists {
		t.Fatalf("missing required array %q", key)
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%q must be an array, got %T", key, value)
	}
	return items
}

func asString(t *testing.T, obj map[string]any, key string) string {
	t.Helper()
	value, exists := obj[key]
	if !exists {
		t.Fatalf("missing required string %q", key)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("%q must be a string, got %T", key, value)
	}
	return text
}

func asBool(t *testing.T, obj map[string]any, key string) bool {
	t.Helper()
	value, exists := obj[key]
	if !exists {
		t.Fatalf("missing required boolean %q", key)
	}
	flag, ok := value.(bool)
	if !ok {
		t.Fatalf("%q must be a boolean, got %T", key, value)
	}
	return flag
}

func asNumber(t *testing.T, obj map[string]any, key string) float64 {
	t.Helper()
	value, exists := obj[key]
	if !exists {
		t.Fatalf("missing required number %q", key)
	}
	number, ok := value.(float64)
	if !ok {
		t.Fatalf("%q must be a number, got %T", key, value)
	}
	return number
}

// asNullableString 用于 excluded_reason / meta.warning 这类 string | null 字段。
func asNullableString(t *testing.T, obj map[string]any, key string) *string {
	t.Helper()
	value, exists := obj[key]
	if !exists {
		t.Fatalf("missing required nullable string %q", key)
	}
	if value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("%q must be a string or null, got %T", key, value)
	}
	return &text
}

// assertMeta 校验 meta 的字段名、类型与 RFC3339 时间戳。
func assertMeta(t *testing.T, meta map[string]any) {
	t.Helper()
	requireKeys(t, meta, "cache", "stale", "updated_at", "expires_at", "stale_until", "warning")

	cache := asString(t, meta, "cache")
	if cache != CacheHit && cache != CacheMiss {
		t.Errorf("meta.cache must be %q or %q, got %q", CacheHit, CacheMiss, cache)
	}
	asBool(t, meta, "stale")
	asNullableString(t, meta, "warning")

	for _, key := range []string{"updated_at", "expires_at", "stale_until"} {
		raw := asString(t, meta, key)
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			t.Errorf("meta.%s must be RFC3339, got %q: %v", key, raw, err)
		}
	}
}

// assertAddress 校验 address.value / address.family 的硬约束。
func assertAddress(t *testing.T, data map[string]any, wantValue string, wantFamily int) {
	t.Helper()
	address := asObject(t, data, "address")
	requireKeys(t, address, "value", "family")

	if got := asString(t, address, "value"); got != wantValue {
		t.Errorf("address.value = %q, want %q", got, wantValue)
	}
	family := asNumber(t, address, "family")
	if family != 4 && family != 6 {
		t.Errorf("address.family must be 4 or 6, got %v", family)
	}
	if int(family) != wantFamily {
		t.Errorf("address.family = %v, want %d", family, wantFamily)
	}
}

// assertClassification 校验 classification 的四个字段与取值约束。
func assertClassification(t *testing.T, classification map[string]any, wantType, wantLabel string) {
	t.Helper()
	requireKeys(t, classification, "type", "label", "geolocated_country_code", "registered_country_code")

	if got := asString(t, classification, "type"); got != wantType {
		t.Errorf("classification.type = %q, want %q", got, wantType)
	}
	if got := asString(t, classification, "label"); got != wantLabel {
		t.Errorf("classification.label = %q, want %q", got, wantLabel)
	}
	asString(t, classification, "geolocated_country_code")
	asString(t, classification, "registered_country_code")
}

// upstreamPathsOf 便于断言「请求确实打到了假上游、且目标地址正确」。
func jsonBody(value any) io.Reader {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return bytes.NewReader(encoded)
}

// containsString 判断字符串切片里是否有目标值。
func containsString(values []any, want string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == want {
			return true
		}
	}
	return false
}
