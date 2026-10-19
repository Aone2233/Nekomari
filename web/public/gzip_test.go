package public

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 静态资源出口的 gzip 压缩（serveAsset / serveAdminAsset → gzip.go 的 serveAssetContent）。
//
// 这条路径此前只有 identity：面板自己的入口包与按需加载的 Monaco 块都是几百 KB 到数 MB 的
// JavaScript，而它们每一个字节都要穿过 Cloudflare 再进浏览器。压缩本身没有风险，风险在它的
// 边界上 —— 一件压缩过的响应如果还带着按原文算出的 Content-Length，客户端会提前截断；
// 一个 Range 请求如果被压缩，返回的字节区间与客户端要求的偏移完全对不上；一个不支持 gzip
// 的客户端如果拿到 gzip 正文，它看到的是一个解不开的文件，而服务端日志里是一次 200。
//
// 所以这里一次钉住五件事：往返一致、没有 Accept-Encoding 时是 identity、Range 走 identity、
// 小文件不压、以及 Vary 的存在 —— Vary 是前四条能在缓存层成立的前提。
//
// 主题/独立页面的资源与后台的 /admin/* 走的是两条并行的出口（缓存判定各不相同），但正文都
// 交给同一个 serveAssetContent —— 两边各有一条用例，因为「只接了一条出口」这种遗漏恰好不
// 改变资源本身是否可用，只会让它一直全量传输。

const (
	gzipTextPath  = "assets/gzip-text-0badc0de.js"
	gzipSmallPath = "assets/gzip-small-0badc0de.js"
	gzipImagePath = "assets/gzip-image-0badc0de.png"

	// 后台构建产物在归档里带 admin/ 前缀，URL 也是 /admin/...（servePanelPath 直接映射），
	// 而那条路径走的是 serveAdminAsset。
	gzipAdminAssetName = "gzip-admin-0badc0de.js"
	gzipAdminPath      = AdminDistDir + "/assets/" + gzipAdminAssetName
)

// gzipSmallBody 是小于阈值的那个文件：刻意只有一行。
const gzipSmallBody = "// gzip-small-marker\n"

// gzipLargeText 生成压缩用例的正文：约 150KB，每行都不同所以能压缩但不会被压到很小。
//
// 两个量级都是有意的：原文远大于 gzipMinAssetSize（要真的走压缩），压缩后仍大于 net/http
// 的 2KB 自动长度阈值（要真的走分块传输）—— 面板里的入口包与 Monaco 块正好都是这个量级。
func gzipLargeText(marker string) []byte {
	var builder strings.Builder
	for index := 0; builder.Len() < 150_000; index++ {
		builder.WriteString("// gzip fixture ")
		builder.WriteString(marker)
		builder.WriteString(" line ")
		builder.WriteString(strconv.Itoa(index))
		builder.WriteString(": a line the browser should not have to download raw\n")
	}
	return []byte(builder.String())
}

var (
	// 主题/独立页面那一条出口的正文。
	gzipTextBody = gzipLargeText("theme")
	// 后台那一条出口的正文：内容不同，能证明解码出来的是 admin/ 下这一份。
	gzipAdminBody = gzipLargeText("admin")
)

// gzipFixtureArchive 是一份最小的可压缩归档：一个大的 .js、一个小的 .js、一个二进制的 .png，
// 外加面板自己的后台构建（admin/ 子树）。
//
// 用合成归档而不是真实归档，是因为这些用例断言的是**服务方式**，而真实归档的构成会随前端
// 构建变化（同样的理由见 pwa_assets_test.go 的 syntheticPWAArchive）。
func gzipFixtureArchive() map[string][]byte {
	image := make([]byte, 4096)
	for index := range image {
		image[index] = byte(index % 251)
	}
	return map[string][]byte{
		IndexFile:             []byte(`<!doctype html><html><head><title>Nekomari Monitor</title></head><body><div id="root"></div></body></html>`),
		".vite/manifest.json": []byte(`{"src/main.tsx":{"file":"` + gzipTextPath + `","isEntry":true}}`),
		gzipTextPath:          gzipTextBody,
		gzipSmallPath:         []byte(gzipSmallBody),
		gzipImagePath:         image,

		// 后台自己的构建清单与资源树：/admin/* 走 serveAdminAsset，它读的是 admin/.vite/manifest.json
		// 而不是主题的清单（见 public.go 的 adminHashedAssets）。
		"admin/.vite/manifest.json": []byte(`{"admin.html":{"file":"assets/` + gzipAdminAssetName + `","isEntry":true}}`),
		gzipAdminPath:               gzipAdminBody,
	}
}

// gzipTestRouter 构造一个用合成归档提供静态资源的 public 路由。
func gzipTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	router := newRouter(t)
	withSyntheticArchive(t, gzipFixtureArchive())
	return router
}

// gzipFetch 发一次 GET，headers 是附加的请求头。
func gzipFetch(t *testing.T, router *gin.Engine, requestPath string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, requestPath, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// 压缩过的响应必须能被同一个 gzip 解码器还原成归档里的字节，且不再带按原文算的 Content-Length。
func TestACompressibleAssetRoundTripsThroughGzip(t *testing.T) {
	router := gzipTestRouter(t)

	response := gzipFetch(t, router, "/"+gzipTextPath, map[string]string{"Accept-Encoding": "gzip"})
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", gzipTextPath, response.Code)
	}
	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip: a client that asked for gzip got identity", got)
	}
	// ServeContent 会按文件长度写 Content-Length；压缩后的字节数不一样，留着它客户端会
	// 按原长度读，多出来的部分被截掉或被当成传输错误。
	if got := response.Header().Get("Content-Length"); got != "" {
		t.Errorf("a compressed response kept Content-Length %q, but the gzip body has a different length", got)
	}
	// 同一个 URL 有两种表示，缓存必须按 Accept-Encoding 分开存。
	if got := response.Header().Get("Vary"); !strings.Contains(strings.ToLower(got), "accept-encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", got)
	}
	// 压缩不改变校验值：ETag 仍然按原文长度与修改时间算，条件请求照旧。
	if response.Header().Get("ETag") == "" {
		t.Error("a compressed response carries no ETag, so every revalidation re-downloads")
	}

	reader, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatalf("the response body is not a gzip stream: %v", err)
	}
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the gzip stream: %v", err)
	}
	if !bytes.Equal(decoded, gzipTextBody) {
		t.Fatalf("the decoded body is %d bytes, want the archive's %d", len(decoded), len(gzipTextBody))
	}
	if response.Body.Len() >= len(gzipTextBody) {
		t.Errorf("the compressed body is %d bytes against %d raw: the fixture and gzip are not doing their job",
			response.Body.Len(), len(gzipTextBody))
	}
}

// 没有 Accept-Encoding 的请求拿到 identity，字节与归档逐字节相同，长度头也照旧。
func TestAnAssetWithoutAcceptEncodingStaysIdentity(t *testing.T) {
	router := gzipTestRouter(t)

	response := gzipFetch(t, router, "/"+gzipTextPath, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", gzipTextPath, response.Code)
	}
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q on a request that never offered gzip", got)
	}
	if body := response.Body.Bytes(); !bytes.Equal(body, gzipTextBody) {
		t.Fatalf("identity body is %d bytes, want the archive's %d", len(body), len(gzipTextBody))
	}
	if got, want := response.Header().Get("Content-Length"), strconv.Itoa(len(gzipTextBody)); got != want {
		t.Errorf("Content-Length = %q, want %q: an identity response still knows its length", got, want)
	}
	// Vary 与是否真的压缩无关：这个 URL 的响应形状取决于请求头，缓存必须知道。
	if got := response.Header().Get("Vary"); !strings.Contains(strings.ToLower(got), "accept-encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding even on the identity branch", got)
	}
}

// Range 请求一律 identity：206 的字节区间是原文件里的偏移，压缩后这个偏移不再成立。
func TestARangeRequestIsServedIdentity(t *testing.T) {
	router := gzipTestRouter(t)

	response := gzipFetch(t, router, "/"+gzipTextPath, map[string]string{
		"Accept-Encoding": "gzip",
		"Range":           "bytes=0-3",
	})
	if response.Code != http.StatusPartialContent {
		t.Fatalf("GET with Range = %d, want 206", response.Code)
	}
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("a range response carries Content-Encoding %q, so the byte range no longer points at the file", got)
	}
	if body := response.Body.String(); body != "// g" {
		t.Errorf("range body = %q, want the file's first four bytes", body)
	}
	if got := response.Header().Get("Vary"); !strings.Contains(strings.ToLower(got), "accept-encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding on the range branch too", got)
	}
}

// 小于阈值的文件不压：压完可能更大，而且每次请求都要付一份 gzip 的固定开销。
func TestASmallAssetStaysIdentity(t *testing.T) {
	router := gzipTestRouter(t)

	response := gzipFetch(t, router, "/"+gzipSmallPath, map[string]string{"Accept-Encoding": "gzip"})
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", gzipSmallPath, response.Code)
	}
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("a %d-byte asset was compressed (%q); below %d bytes compression costs more than it saves",
			len(gzipSmallBody), got, gzipMinAssetSize)
	}
	if body := response.Body.String(); body != gzipSmallBody {
		t.Errorf("small asset body = %q, want the archive's bytes", body)
	}
}

// 已经是压缩格式的二进制不压，也不必声明 Vary —— 它的响应不随 Accept-Encoding 变化。
func TestABinaryAssetIsNotCompressedAndDoesNotVary(t *testing.T) {
	router := gzipTestRouter(t)

	response := gzipFetch(t, router, "/"+gzipImagePath, map[string]string{"Accept-Encoding": "gzip"})
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", gzipImagePath, response.Code)
	}
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("a PNG was sent with Content-Encoding %q: it is already compressed", got)
	}
	if got := response.Header().Get("Vary"); strings.Contains(strings.ToLower(got), "accept-encoding") {
		t.Errorf("Vary = %q for a type that is never compressed: the cache key would be split for no reason", got)
	}
}

// gzip;q=0 是明确拒绝，不是「没写优先级」。
func TestGzipIsRefusedWhenTheClientAsksForQZero(t *testing.T) {
	router := gzipTestRouter(t)

	response := gzipFetch(t, router, "/"+gzipTextPath, map[string]string{"Accept-Encoding": "gzip;q=0, identity"})
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", gzipTextPath, response.Code)
	}
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q although the client set gzip;q=0", got)
	}
	if body := response.Body.Bytes(); !bytes.Equal(body, gzipTextBody) {
		t.Errorf("body is not the identity file: the client asked not to receive gzip")
	}
}

// 条件请求命中 304 时不能带正文，也不能带 Content-Encoding。
//
// 这一条钉的是压缩实现里唯一的「没有正文」分支：gzip.Writer 在 Close 时会写出头部与尾部，
// 而 304 不允许有正文 —— 收尾条件写错的话，一个 304 会带上 18 字节的 gzip 空流。
func TestAConditionalRequestUnderGzipHasNoBody(t *testing.T) {
	router := gzipTestRouter(t)

	first := gzipFetch(t, router, "/"+gzipTextPath, map[string]string{"Accept-Encoding": "gzip"})
	validator := first.Header().Get("ETag")
	if validator == "" {
		t.Fatal("the compressed response has no ETag, so a conditional request cannot be formed")
	}

	response := gzipFetch(t, router, "/"+gzipTextPath, map[string]string{
		"Accept-Encoding": "gzip",
		"If-None-Match":   validator,
	})
	if response.Code != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", response.Code)
	}
	if response.Body.Len() != 0 {
		t.Errorf("the 304 carried %d bytes, want none", response.Body.Len())
	}
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("the 304 carries Content-Encoding %q, but it has no body to decode", got)
	}
}

// shouldGzipAsset 的判定表：每一条都是上面某个用例的边界，直接钉在函数上便于定位。
func TestShouldGzipAssetConditions(t *testing.T) {
	compressible := "text/javascript; charset=utf-8"
	request := func(method string, mutate func(*http.Request)) *http.Request {
		out := httptest.NewRequest(method, "/assets/x.js", nil)
		out.Header.Set("Accept-Encoding", "gzip")
		if mutate != nil {
			mutate(out)
		}
		return out
	}

	cases := []struct {
		name     string
		request  *http.Request
		header   http.Header
		mimeType string
		size     int64
		want     bool
	}{
		{name: "a long compressible asset offered gzip", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: compressible, size: 4096, want: true},
		{name: "no Accept-Encoding at all", request: request(http.MethodGet, func(r *http.Request) {
			r.Header.Del("Accept-Encoding")
		}), header: http.Header{}, mimeType: compressible, size: 4096, want: false},
		{name: "gzip;q=0", request: request(http.MethodGet, func(r *http.Request) {
			r.Header.Set("Accept-Encoding", "gzip;q=0")
		}), header: http.Header{}, mimeType: compressible, size: 4096, want: false},
		{name: "a low but non-zero q", request: request(http.MethodGet, func(r *http.Request) {
			r.Header.Set("Accept-Encoding", "br, gzip;q=0.2")
		}), header: http.Header{}, mimeType: compressible, size: 4096, want: true},
		{name: "only a wildcard, no explicit gzip", request: request(http.MethodGet, func(r *http.Request) {
			r.Header.Set("Accept-Encoding", "*")
		}), header: http.Header{}, mimeType: compressible, size: 4096, want: false},
		{name: "a Range request", request: request(http.MethodGet, func(r *http.Request) {
			r.Header.Set("Range", "bytes=0-3")
		}), header: http.Header{}, mimeType: compressible, size: 4096, want: false},
		{name: "an If-Range request", request: request(http.MethodGet, func(r *http.Request) {
			r.Header.Set("If-Range", "etag")
		}), header: http.Header{}, mimeType: compressible, size: 4096, want: false},
		{name: "the response already carries an encoding",
			request: request(http.MethodGet, nil),
			header:  http.Header{"Content-Encoding": []string{"br"}}, mimeType: compressible, size: 4096, want: false},
		{name: "exactly at the size floor", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: compressible, size: gzipMinAssetSize, want: true},
		{name: "one byte below the size floor", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: compressible, size: gzipMinAssetSize - 1, want: false},
		{name: "an image", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: "image/png", size: 4096, want: false},
		{name: "a wasm module", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: "application/wasm", size: 4096, want: true},
		{name: "a PWA manifest", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: "application/manifest+json", size: 4096, want: true},
		{name: "an SVG", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: "image/svg+xml", size: 4096, want: true},
		{name: "a stylesheet", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: "text/css; charset=utf-8", size: 4096, want: true},
		{name: "no Content-Type at all", request: request(http.MethodGet, nil),
			header: http.Header{}, mimeType: "", size: 4096, want: false},
		{name: "a HEAD request", request: request(http.MethodHead, nil),
			header: http.Header{}, mimeType: compressible, size: 4096, want: false},
		{name: "a POST request", request: request(http.MethodPost, nil),
			header: http.Header{}, mimeType: compressible, size: 4096, want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := shouldGzipAsset(testCase.request, testCase.header, testCase.mimeType, testCase.size); got != testCase.want {
				t.Errorf("shouldGzipAsset() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// Accept-Encoding 的解析，包括那些看着像接受、实际是拒绝的写法。
func TestGzipAcceptable(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"identity", false},
		{"gzip", true},
		{"GZIP", true},
		{" gzip ", true},
		{"deflate, gzip", true},
		{"gzip;q=0", false},
		{"gzip; q=0.0", false},
		{"gzip;q=0.001", true},
		{"br;q=1, gzip;q=0.5", true},
		{"br", false},
		{"*", false},
		{"x-gzip", false},
		// 同一份头里出现两次时，任一条允许就允许。
		{"gzip;q=0, gzip", true},
	}
	for _, testCase := range cases {
		if got := gzipAcceptable(testCase.header); got != testCase.want {
			t.Errorf("gzipAcceptable(%q) = %v, want %v", testCase.header, got, testCase.want)
		}
	}
}

// 类型判定：带参数、大小写混排的实际值都要认。
func TestIsCompressibleAssetType(t *testing.T) {
	cases := []struct {
		mimeType string
		want     bool
	}{
		{"text/javascript; charset=utf-8", true},
		{"text/css", true},
		{"text/html; charset=utf-8", true},
		{"TEXT/PLAIN", true},
		{"application/javascript", true},
		{"application/json", true},
		{"application/manifest+json", true},
		{"image/svg+xml", true},
		{"application/wasm", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"font/woff2", false},
		{"", false},
		// 不是合法 MIME 的值不猜。
		{"not a mime type", false},
	}
	for _, testCase := range cases {
		if got := isCompressibleAssetType(testCase.mimeType); got != testCase.want {
			t.Errorf("isCompressibleAssetType(%q) = %v, want %v", testCase.mimeType, got, testCase.want)
		}
	}
}

// addVary 不能覆盖已有的 Vary，也不能重复添加。
func TestAddVary(t *testing.T) {
	header := http.Header{}
	addVary(header, "Accept-Encoding")
	addVary(header, "Accept-Encoding")
	if got := header.Values("Vary"); len(got) != 1 || got[0] != "Accept-Encoding" {
		t.Errorf("Vary = %v, want a single Accept-Encoding", got)
	}

	combined := http.Header{"Vary": []string{"Cookie, accept-encoding"}}
	addVary(combined, "Accept-Encoding")
	if got := combined.Values("Vary"); len(got) != 1 {
		t.Errorf("Vary = %v, want the existing value left alone (case-insensitive match)", got)
	}

	other := http.Header{"Vary": []string{"Cookie"}}
	addVary(other, "Accept-Encoding")
	if got := other.Values("Vary"); len(got) != 2 || got[0] != "Cookie" || got[1] != "Accept-Encoding" {
		t.Errorf("Vary = %v, want Cookie preserved and Accept-Encoding appended", got)
	}
}

// 保底：压缩路径不能把响应变成「带 gzip 头但没有完整 gzip 流」。
func TestTheGzipBodyIsACompleteStream(t *testing.T) {
	router := gzipTestRouter(t)
	response := gzipFetch(t, router, "/"+gzipTextPath, map[string]string{"Accept-Encoding": "gzip"})

	reader, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	// ReadAll 会读到 gzip 的尾部并校验 CRC；流被截断时这里返回 ErrUnexpectedEOF。
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("the gzip stream is truncated: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("the gzip stream's checksum does not match: %v", err)
	}
}

// 真实的 net/http 服务器上再验一遍：httptest.ResponseRecorder 不会模拟传输层的分块编码，
// 而「删掉 Content-Length」这个动作的意义正好在传输层 —— 带错误的长度时，服务器会把它写进
// 响应头，客户端按那个长度截断压缩流。
func TestTheGzipResponseOnARealServerIsDecodable(t *testing.T) {
	router := gzipTestRouter(t)
	server := httptest.NewServer(router)
	defer server.Close()

	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/"+gzipTextPath, nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", gzipTextPath, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}

	wire, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the wire body: %v", err)
	}
	// 长度只能是压缩后的长度，绝不可能是原文长度 —— 服务端删掉 Content-Length 的意思就是
	// 「我不知道压完多长」。net/http 对小响应会自己补一个正确的长度，大响应走分块；
	// 两种都行，错的是把原文长度留着（客户端会按它截断压缩流）。
	if response.ContentLength >= 0 && response.ContentLength != int64(len(wire)) {
		t.Errorf("ContentLength = %d but the wire body is %d bytes: the raw asset length leaked into the response",
			response.ContentLength, len(wire))
	}
	if response.ContentLength < 0 {
		// 长度未知时必须走分块传输 —— 这正是删掉 Content-Length 之后 net/http 的落点。
		if len(response.TransferEncoding) == 0 || response.TransferEncoding[0] != "chunked" {
			t.Errorf("TransferEncoding = %v, want chunked when the length is unknown", response.TransferEncoding)
		}
	}
	t.Logf("wire body %d bytes (raw %d), ContentLength=%d, TransferEncoding=%v",
		len(wire), len(gzipTextBody), response.ContentLength, response.TransferEncoding)
	if len(wire) >= len(gzipTextBody) {
		t.Errorf("the wire body is %d bytes against %d raw: the server did not compress it", len(wire), len(gzipTextBody))
	}

	reader, err := gzip.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("the wire body is not a gzip stream: %v", err)
	}
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the gzip stream off the wire: %v", err)
	}
	if !bytes.Equal(decoded, gzipTextBody) {
		t.Errorf("the decoded wire body is %d bytes, want the archive's %d", len(decoded), len(gzipTextBody))
	}
}

// 后台资源同样要被压缩。/admin/* 下是后台入口包与按需加载的 Monaco 块 —— 面板里最大的那批
// 资源 —— 而它走的是 serveAdminAsset（另一套缓存判定），不是 serveAsset。
//
// 这条用例是「只接了一条出口」这种遗漏唯一能暴露的地方：资源本身照常可用、状态码正常、
// 缓存头也正确，唯一的表现是它一直全量传输。压缩与缓存是两件事：这里只断言正文形态，
// 缓存头仍归 serveAdminAsset 管（adminHashedAssets 有 sync.Map 缓存，不在本用例里钉具体值）。
func TestAdminAssetsAreGzippedThroughTheSharedExit(t *testing.T) {
	router := gzipTestRouter(t)

	compressed := gzipFetch(t, router, "/"+gzipAdminPath, map[string]string{"Accept-Encoding": "gzip"})
	if compressed.Code != http.StatusOK {
		t.Fatalf("GET /%s = %d, want 200", gzipAdminPath, compressed.Code)
	}
	if got := compressed.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip: /admin/* is a static asset path too", got)
	}
	if got := compressed.Header().Get("Content-Length"); got != "" {
		t.Errorf("a compressed admin asset kept Content-Length %q, but the gzip body has a different length", got)
	}
	if got := compressed.Header().Get("Vary"); !strings.Contains(strings.ToLower(got), "accept-encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding on the admin exit", got)
	}

	reader, err := gzip.NewReader(bytes.NewReader(compressed.Body.Bytes()))
	if err != nil {
		t.Fatalf("the admin response body is not a gzip stream: %v", err)
	}
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the admin gzip stream: %v", err)
	}
	if !bytes.Equal(decoded, gzipAdminBody) {
		t.Fatalf("the decoded admin body is %d bytes, want the archive's admin asset (%d)", len(decoded), len(gzipAdminBody))
	}
	if compressed.Body.Len() >= len(gzipAdminBody) {
		t.Errorf("the compressed admin body is %d bytes against %d raw: it was not actually compressed",
			compressed.Body.Len(), len(gzipAdminBody))
	}

	// 同一个 URL 在客户端没提 gzip 时仍然是 identity：压缩不能变成默认行为。
	identity := gzipFetch(t, router, "/"+gzipAdminPath, nil)
	if identity.Code != http.StatusOK {
		t.Fatalf("GET /%s without Accept-Encoding = %d, want 200", gzipAdminPath, identity.Code)
	}
	if got := identity.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q on a request that never offered gzip", got)
	}
	if !bytes.Equal(identity.Body.Bytes(), gzipAdminBody) {
		t.Errorf("the identity admin body is %d bytes, want the archive's %d",
			identity.Body.Len(), len(gzipAdminBody))
	}
	// 正文出口改成共享之后，缓存头仍必须由 serveAdminAsset 设置（只是确认它没被这次重构带走）。
	if got := identity.Header().Get("Cache-Control"); got == "" {
		t.Error("the admin asset lost its Cache-Control when the body exit became shared")
	}
}
