package public

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain 把默认主题的磁盘缓存关掉。
//
// 既有用例测的是「缓存不可用 → 内存 map」这条路径 —— 它也是线上只读文件系统走的路径，
// 所以默认关掉既让那些用例保持改动前的形状（它们大多 t.Chdir 到各自的临时目录，没有
// 稳定的 data/ 可放缓存），也顺手保证了测试不会往仓库的 ./data 里解 33 MiB。
// 关心缓存本身的用例自己打开它，见 enableThemeCacheForTest。
func TestMain(m *testing.M) {
	themeCacheEnabled = false
	os.Exit(m.Run())
}

// mustEmbeddedDistFiles 取内嵌归档在内存里的那份副本，解不开就直接失败。
//
// init() 不再无条件解压归档（那是这一批次要消掉的内存与启动开销），所以直接读
// defaultDistFiles 的用例必须经由这里显式要一次。
func mustEmbeddedDistFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := embeddedDistFiles()
	if len(files) == 0 {
		if err := embeddedDistErr; err != nil {
			t.Fatalf("the embedded dist archive does not load: %v", err)
		}
		t.Fatal("the embedded dist archive is empty")
	}
	return files
}

// forgetEmbeddedDistForTest 丢掉内存副本，好让用例观察「这一次请求有没有加载它」。
func forgetEmbeddedDistForTest(t *testing.T) {
	t.Helper()
	previous := defaultDistFiles
	previousErr := embeddedDistErr
	previousLoaded := defaultThemeMemoryLoaded.Load()
	embeddedDistOnce = sync.Once{}
	embeddedDistErr = nil
	defaultDistFiles = nil
	defaultThemeMemoryLoaded.Store(false)
	t.Cleanup(func() {
		embeddedDistOnce = sync.Once{}
		embeddedDistErr = previousErr
		defaultDistFiles = previous
		defaultThemeMemoryLoaded.Store(previousLoaded)
	})
}

// enableThemeCacheForTest 打开默认主题的磁盘缓存，返回它应该出现的根目录，并在用例
// 结束时关闭（并清掉状态）。
//
// 必须在 t.Chdir(t.TempDir()) 之后调用：DataDir 是相对路径，缓存会落到当时的工作目录
// 下；没有切目录就打开它，会把 33 MiB 解进仓库的 ./data。这里直接把它变成一条断言。
func enableThemeCacheForTest(t *testing.T) string {
	t.Helper()
	if wd, err := os.Getwd(); err != nil || !isWithinDirectory(os.TempDir(), wd) {
		t.Fatalf("enableThemeCacheForTest must run after t.Chdir(t.TempDir()): "+
			"working directory %q is not under the system temp dir, so the cache would be written into the repository", wd)
	}
	themeCacheEnabled = true
	resetThemeCache()
	t.Cleanup(func() {
		themeCacheEnabled = false
		resetThemeCache()
	})
	return filepath.Join(DataDir, themeCacheDirName)
}

// isWithinDirectory 报告 target 是否在 base 之内（不含 base 本身）。
func isWithinDirectory(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// themeCacheHTML 是一份最小的、用来替换缓存里 index.html 的内容（完整性校验的用例用）。
const themeCacheHTML = "<!doctype html><html><body><div id=\"root\"></div></body></html>\n"

// 物化之后，磁盘上必须是**整份归档**、逐字节一致，而且一开始什么都不该有 —— 物化必
// 须发生在第一次需要默认主题资源的时候，而不是 init()。
func TestTheDiskCacheMaterialisesTheWholeArchiveByteForByte(t *testing.T) {
	router := newRouter(t)
	cacheRoot := enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	forgetHashedAssetManifests()

	if _, err := os.Stat(cacheRoot); !os.IsNotExist(err) {
		t.Fatalf("the cache root exists before the first default-theme request (stat error %v), so it was materialised eagerly", err)
	}

	started := time.Now()
	response := get(t, router, "/themes/default/dist/index.html")
	elapsed := time.Since(started)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200", response.Code)
	}

	dir, err := defaultThemeCacheDir()
	if err != nil {
		t.Fatalf("the disk cache was not materialised: %v", err)
	}
	if want := filepath.Join(cacheRoot, embeddedDistArchiveKey()); dir != want {
		t.Fatalf("cache directory = %q, want %q (data/.theme-cache/<archive-hash>)", dir, want)
	}

	// 标记文件必须存在，而且描述的就是目录里实际的条目数与字节数。
	marker, err := os.ReadFile(filepath.Join(dir, themeCacheMarker))
	if err != nil {
		t.Fatalf("the completion marker is missing: %v", err)
	}
	var manifest themeCacheManifest
	if err := json.Unmarshal(marker, &manifest); err != nil {
		t.Fatalf("the completion marker is not valid JSON: %v", err)
	}

	// 归档里的每一个条目都要在磁盘上，且逐字节相同。
	raw, err := decodeEmbeddedDistEntries(embeddedDistArchive)
	if err != nil {
		t.Fatalf("the embedded archive does not decode: %v", err)
	}
	var totalBytes int64
	for name, want := range raw {
		totalBytes += int64(len(want))
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("%q is not in the disk cache: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%q differs from the archive (%d bytes cached, %d in the archive)", name, len(got), len(want))
		}
	}
	if manifest.Files != len(raw) {
		t.Errorf("the marker records %d files, the archive holds %d", manifest.Files, len(raw))
	}
	if manifest.Bytes != totalBytes {
		t.Errorf("the marker records %d bytes, the archive holds %d", manifest.Bytes, totalBytes)
	}

	// 没有多余的文件，也没有遗留的暂存目录。
	usage, err := themeCacheDirectoryUsage(dir)
	if err != nil {
		t.Fatalf("measure the cache directory: %v", err)
	}
	if usage.Files != len(raw) || usage.Bytes != totalBytes {
		t.Errorf("the cache directory holds %d files/%d bytes, the archive holds %d/%d",
			usage.Files, usage.Bytes, len(raw), totalBytes)
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatalf("read the cache root: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(dir) {
			t.Errorf("%q was left in %s (a staging directory or a stale cache)", entry.Name(), cacheRoot)
		}
	}

	// 全部由磁盘供给：内存里的那份副本必须始终没有被解出来。
	if defaultDistFiles != nil {
		t.Errorf("the in-memory archive was loaded (%d entries) although every asset came from the disk cache", len(defaultDistFiles))
	}

	t.Logf("materialised %d files / %.2f MiB into %s in %s; the in-memory archive stayed nil (it would be %.2f MiB resident)",
		usage.Files, float64(usage.Bytes)/(1<<20), dir, elapsed, float64(totalBytes)/(1<<20))
}

// 缓存路径与内存路径必须给出同样的字节：同一份资源在 /themes/default/... 与 SPA 回退
// （/assets/...）两条路由上取到的内容，必须等于归档里的内容，也必须彼此相等。
func TestCachedAssetsAreServedIdenticallyOnEveryRoute(t *testing.T) {
	router := newRouter(t)
	enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	forgetHashedAssetManifests()

	raw, err := decodeEmbeddedDistEntries(embeddedDistArchive)
	if err != nil {
		t.Fatalf("the embedded archive does not decode: %v", err)
	}
	manifestRaw, ok := raw[".vite/manifest.json"]
	if !ok {
		t.Fatal("the archive has no .vite/manifest.json, so no build output can be picked for this test")
	}
	// 取最大的那个 .js/.css：gzip 判断有一个 1 KiB 的下限，随便挑一个小 chunk 会让
	// 「压缩出口没有回归」这条断言因为取样而失效。
	var bundle, stylesheet string
	for name := range hashedAssetsFromManifest(manifestRaw) {
		switch {
		case strings.HasSuffix(name, ".js") && len(raw[name]) > len(raw[bundle]):
			bundle = name
		case strings.HasSuffix(name, ".css") && len(raw[name]) > len(raw[stylesheet]):
			stylesheet = name
		}
	}
	if bundle == "" {
		t.Fatal("the archive's manifest lists no JavaScript bundle")
	}
	if len(raw[bundle]) < gzipMinAssetSize {
		t.Fatalf("the largest JavaScript bundle is %d bytes, below the gzip threshold: the compression case would prove nothing", len(raw[bundle]))
	}

	cases := []struct {
		requestPath string
		archiveName string
	}{
		// 主题路由：/themes/default/dist/<产物>
		{"/themes/default/dist/" + bundle, bundle},
		// SPA 回退：/assets/<产物> 由 openAsset 拼成 dist/<产物>
		{"/" + bundle, bundle},
		// 主题根上的文件（不在 dist/ 下）
		{"/themes/default/index.html", "index.html"},
		// 面板自带后台的资源走 admin/ 子树
		{"/admin/index.html", "admin/" + IndexFile},
	}
	if stylesheet != "" {
		cases = append(cases, struct {
			requestPath string
			archiveName string
		}{"/" + stylesheet, stylesheet})
	}

	bodies := map[string][]byte{}
	for _, tc := range cases {
		want, archived := raw[tc.archiveName]
		if !archived {
			t.Fatalf("%q is not in the archive, so the case %s tests nothing", tc.archiveName, tc.requestPath)
		}
		recorder := get(t, router, tc.requestPath)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.requestPath, recorder.Code)
			continue
		}
		if body := recorder.Body.Bytes(); !bytes.Equal(body, want) {
			t.Errorf("GET %s returned %d bytes that are not the archive's %d", tc.requestPath, len(body), len(want))
			continue
		}
		bodies[tc.requestPath] = recorder.Body.Bytes()
		if got := recorder.Header().Get("ETag"); got == "" {
			t.Errorf("GET %s (from the disk cache) has no ETag, so a revalidation cannot be answered with 304", tc.requestPath)
		}
	}
	// 两条路由取到的是同一份字节（它们读的是同一个文件，但历史上这是两条独立分支）。
	if body, ok := bodies["/themes/default/dist/"+bundle]; ok {
		if spa, spaOK := bodies["/"+bundle]; spaOK && !bytes.Equal(body, spa) {
			t.Error("/themes/default/dist/... and /assets/... served different bytes for the same asset")
		}
	}

	// 构建产物的长期缓存头没有因为改成磁盘读取而丢失：它依赖 .vite/manifest.json 被
	// 读到，而那份清单现在也从缓存里来。
	hashed := get(t, router, "/themes/default/dist/"+bundle)
	if got := hashed.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=31536000") {
		t.Errorf("a build output served from the disk cache got Cache-Control %q, want the long-lived one", got)
	}

	// 协商缓存仍然成立（ETag 现在来自磁盘文件的修改时间）。
	etag := hashed.Header().Get("ETag")
	conditional := httptest.NewRequest(http.MethodGet, "/themes/default/dist/"+bundle, nil)
	conditional.Header.Set("If-None-Match", etag)
	conditionalRecorder := httptest.NewRecorder()
	router.ServeHTTP(conditionalRecorder, conditional)
	if conditionalRecorder.Code != http.StatusNotModified {
		t.Errorf("conditional request for a cached build output = %d, want 304", conditionalRecorder.Code)
	}

	// gzip 出口对 *os.File 同样成立：压缩后的正文解开来必须与原文件一致。
	compressed := httptest.NewRequest(http.MethodGet, "/themes/default/dist/"+bundle, nil)
	compressed.Header.Set("Accept-Encoding", "gzip")
	compressedRecorder := httptest.NewRecorder()
	router.ServeHTTP(compressedRecorder, compressed)
	if got := compressedRecorder.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("a JavaScript bundle from the disk cache got Content-Encoding %q, want gzip", got)
	} else {
		reader, err := gzip.NewReader(bytes.NewReader(compressedRecorder.Body.Bytes()))
		if err != nil {
			t.Errorf("the gzip body of a cached bundle does not decode: %v", err)
		} else if body, err := io.ReadAll(reader); err != nil {
			t.Errorf("read the gzip body: %v", err)
		} else if !bytes.Equal(body, raw[bundle]) {
			t.Errorf("the gzip body does not decompress to the archive's bytes (%d vs %d)", len(body), len(raw[bundle]))
		}
	}

	if defaultDistFiles != nil {
		t.Errorf("the in-memory archive was loaded (%d entries) although every asset came from the disk cache", len(defaultDistFiles))
	}
}

// 磁盘缓存命中时，整条请求路径一次都不该碰内存里的那份副本 —— 那 33 MiB 就是这次改动
// 要省掉的东西。单独一条用例把它变成一个显式断言，而不是别的用例的副产品。
func TestServingFromTheDiskCacheNeverLoadsTheInMemoryArchive(t *testing.T) {
	router := newRouter(t)
	enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	forgetHashedAssetManifests()

	for _, requestPath := range []string{"/", "/themes/default/dist/index.html", "/admin/", "/favicon.ico"} {
		if recorder := get(t, router, requestPath); recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", requestPath, recorder.Code)
		}
	}
	if defaultDistFiles != nil {
		t.Fatalf("the in-memory archive was loaded (%d entries): the disk cache path still pays the 33 MiB", len(defaultDistFiles))
	}
	if embeddedDistErr != nil {
		t.Fatalf("loading the in-memory archive failed: %v", embeddedDistErr)
	}
}

// 缓存建不出来（只读文件系统、权限、磁盘满）时必须退回内存副本，页面照常 200 —— 这与
// 改动前的行为完全一致。这里用「data 被一个普通文件占位」来制造同样的失败：data/.theme-cache
// 建不出来，而且不能靠 chmod（Windows 上不可靠）。
func TestAnUnwritableCacheFallsBackToTheInMemoryArchive(t *testing.T) {
	router := newRouter(t)
	enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	forgetHashedAssetManifests()

	if err := os.WriteFile(DataDir, []byte("a regular file where the data directory should be"), 0o644); err != nil {
		t.Fatalf("place a file where the data directory should be: %v", err)
	}

	response := get(t, router, "/themes/default/dist/index.html")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200: a failed cache must fall back to the in-memory archive", response.Code)
	}
	source := mustEmbeddedDistFiles(t)
	if body := response.Body.Bytes(); !bytes.Equal(body, source[IndexFile]) {
		t.Fatalf("the fallback served %d bytes that are not the archive's %d", len(body), len(source[IndexFile]))
	}

	if dir, err := defaultThemeCacheDir(); err == nil || dir != "" {
		t.Fatalf("defaultThemeCacheDir() = (%q, %v), want an error: the failure must be recorded and the fallback used", dir, err)
	}
	if got := ThemeCacheSnapshot(); got.Disk != "failed" || !got.MemoryLoaded {
		t.Fatalf("cache failure snapshot = %+v", got)
	}
	// 这里不能断言「一定不存在」：data 现在是个普通文件，在 Windows 上 stat
	// data/.theme-cache 的错误是 ERROR_DIRECTORY 而不是「文件不存在」。要守住的是
	// 「没有真的建出一个缓存目录」。
	if info, err := os.Stat(filepath.Join(DataDir, themeCacheDirName)); err == nil && info.IsDir() {
		t.Error("a cache directory was created under a path that cannot hold one")
	}

	// 回退之后 SPA 外壳也照常工作（它同样走 getFileContent → openAsset）。
	if recorder := get(t, router, "/dashboard"); recorder.Code != http.StatusOK {
		t.Errorf("GET /dashboard = %d, want 200 from the in-memory fallback", recorder.Code)
	}
}

// KOMARI_THEME_CACHE=0 是运维版本的开关：不物化、不写盘，直接走内存副本。
func TestTheCacheCanBeDisabledByEnvironment(t *testing.T) {
	router := newRouter(t)
	t.Setenv(themeCacheEnvVar, "0")
	themeCacheEnabled = true
	resetThemeCache()
	t.Cleanup(func() {
		themeCacheEnabled = false
		resetThemeCache()
	})

	if themeCacheAllowed() {
		t.Fatalf("%s=0 did not disable the disk cache", themeCacheEnvVar)
	}
	if recorder := get(t, router, "/themes/default/dist/index.html"); recorder.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200", recorder.Code)
	}
	if _, err := os.Stat(filepath.Join(DataDir, themeCacheDirName)); !os.IsNotExist(err) {
		t.Errorf("a cache directory was created although %s=0 (stat error %v)", themeCacheEnvVar, err)
	}
	if defaultDistFiles == nil {
		t.Error("the in-memory archive was not used although the disk cache is disabled")
	}
}

// 开关的解析本身：只认明确写出的关闭值。
func TestThemeCacheEnvironmentParsing(t *testing.T) {
	for _, value := range []string{"0", "false", "FALSE", "off", "No", " 0 "} {
		t.Setenv(themeCacheEnvVar, value)
		if !themeCacheDisabledByEnvironment() {
			t.Errorf("%s=%q was not understood as a request to disable the cache", themeCacheEnvVar, value)
		}
	}
	for _, value := range []string{"", "1", "true", "on", "yes", "anything-else"} {
		t.Setenv(themeCacheEnvVar, value)
		if themeCacheDisabledByEnvironment() {
			t.Errorf("%s=%q disabled the cache; only an explicit off value may", themeCacheEnvVar, value)
		}
	}
}

// 路径穿越必须被拒绝，而且缓存目录之外的文件一个字节都不能泄漏。
func TestTheDiskCacheRejectsPathTraversal(t *testing.T) {
	router := newRouter(t)
	enableThemeCacheForTest(t)
	forgetHashedAssetManifests()

	const secret = "OUTSIDE-THE-THEME-CACHE-MARKER"
	if err := os.WriteFile("secret.txt", []byte(secret), 0o644); err != nil {
		t.Fatalf("write the file the traversal would reach: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(DataDir, themeCacheDirName), 0o755); err != nil {
		t.Fatalf("create the cache root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(DataDir, themeCacheDirName, "secret.txt"), []byte(secret), 0o644); err != nil {
		t.Fatalf("write a file next to the cache directory: %v", err)
	}

	for _, requestPath := range []string{
		"/themes/default/../secret.txt",
		"/themes/default/../../secret.txt",
		"/themes/default/%2e%2e%2fsecret.txt",
		"/themes/default/..%2f..%2fsecret.txt",
	} {
		recorder := get(t, router, requestPath)
		if recorder.Code == http.StatusOK {
			t.Errorf("GET %s = 200, want a rejection: a path outside the theme must never be served", requestPath)
		}
		if strings.Contains(recorder.Body.String(), secret) {
			t.Errorf("GET %s leaked a file from outside the theme root", requestPath)
		}
	}

	// 缓存查找本身（不经过路由）也不能把名字归一到外面去。
	if _, err := defaultThemeCacheDir(); err != nil {
		t.Fatalf("materialise the cache: %v", err)
	}
	for _, name := range []string{
		"../secret.txt",
		"assets/../../secret.txt",
		"..",
		"",
		"/",
		"../.theme-cache/secret.txt",
	} {
		if source, hit, _ := lookupDefaultThemeCachedAsset(name); hit {
			if source.close != nil {
				source.close()
			}
			t.Errorf("the cache lookup for %q resolved a path outside the cache directory", name)
		}
	}
	// 正面的那一半：缓存目录里的文件照常命中，证明上面的拒绝不是「什么都查不到」。
	if source, hit, _ := lookupDefaultThemeCachedAsset("index.html"); !hit {
		t.Error("the cache lookup for index.html missed: the lookup is broken, not just strict")
	} else if source.close != nil {
		source.close()
	}
}

// 半份缓存（没有标记文件）必须被重建，而不是被当成有效缓存用。
func TestAnIncompleteCacheDirectoryIsRebuilt(t *testing.T) {
	router := newRouter(t)
	cacheRoot := enableThemeCacheForTest(t)
	key := embeddedDistArchiveKey()
	dir := filepath.Join(cacheRoot, key)

	// 模拟「上次物化在解压中途被杀」：有目录、有半份文件、没有标记。
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("create the incomplete cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, IndexFile), []byte("TRUNCATED"), 0o644); err != nil {
		t.Fatalf("write the truncated document: %v", err)
	}
	// 另一个 hash 的旧目录：升级之后它再也不会被读到，应当被尽力删除。
	stale := filepath.Join(cacheRoot, "0123456789abcdef")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("create a stale cache directory: %v", err)
	}

	response := get(t, router, "/themes/default/dist/index.html")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), "TRUNCATED") {
		t.Fatal("the truncated document in the incomplete cache was served")
	}

	raw, err := decodeEmbeddedDistEntries(embeddedDistArchive)
	if err != nil {
		t.Fatalf("the embedded archive does not decode: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		t.Fatalf("read the rebuilt document: %v", err)
	}
	if !bytes.Equal(got, raw[IndexFile]) {
		t.Errorf("the rebuilt document is not the archive's (%d bytes vs %d)", len(got), len(raw[IndexFile]))
	}
	if _, err := os.Stat(filepath.Join(dir, themeCacheMarker)); err != nil {
		t.Errorf("the rebuilt cache has no completion marker: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale cache of another archive hash was not removed (stat error %v)", err)
	}
}

// 运行期缓存目录被删掉（运维手误、临时目录清理）时，必须退到内存副本，而不是把默认
// 主题的每个资源都变成 404 —— 缓存是加速层，它的消失不能变成故障。
func TestACacheDeletedAtRuntimeFallsBackToTheInMemoryArchive(t *testing.T) {
	router := newRouter(t)
	cacheRoot := enableThemeCacheForTest(t)
	forgetHashedAssetManifests()

	if recorder := get(t, router, "/themes/default/dist/index.html"); recorder.Code != http.StatusOK {
		t.Fatalf("materialise the cache: GET /themes/default/dist/index.html = %d", recorder.Code)
	}
	dir, err := defaultThemeCacheDir()
	if err != nil {
		t.Fatalf("the cache was not materialised: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(cacheRoot, filepath.Base(dir))); err != nil {
		t.Fatalf("delete the cache directory: %v", err)
	}
	forgetEmbeddedDistForTest(t)

	response := get(t, router, "/themes/default/dist/index.html")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200: a cache that vanished at runtime must not turn into a 404", response.Code)
	}
	source := mustEmbeddedDistFiles(t)
	if body := response.Body.Bytes(); !bytes.Equal(body, source[IndexFile]) {
		t.Errorf("the fallback served %d bytes that are not the archive's %d", len(body), len(source[IndexFile]))
	}
}

// 标记说完整、实际内容被截断（掉电后页缓存没落盘）时，第一次用到缓存必须发现并重建。
// 这条守着的是完整性复核本身：没有它，截断的 JS/CSS 会被原样发给浏览器，表现为随机
// 的语法错误，且只在某次异常重启之后出现。
func TestATruncatedCacheIsDetectedAndRebuilt(t *testing.T) {
	router := newRouter(t)
	enableThemeCacheForTest(t)
	if recorder := get(t, router, "/themes/default/dist/index.html"); recorder.Code != http.StatusOK {
		t.Fatalf("materialise the cache: GET /themes/default/dist/index.html = %d", recorder.Code)
	}
	dir, err := defaultThemeCacheDir()
	if err != nil {
		t.Fatalf("the cache was not materialised: %v", err)
	}

	// 截断一个文件，文件大小因此与标记记录的总字节数对不上。
	raw, err := decodeEmbeddedDistEntries(embeddedDistArchive)
	if err != nil {
		t.Fatalf("the embedded archive does not decode: %v", err)
	}
	target := filepath.Join(dir, IndexFile)
	if err := os.WriteFile(target, []byte(themeCacheHTML), 0o644); err != nil {
		t.Fatalf("truncate the cached document: %v", err)
	}

	// 模拟进程重启：状态重新判断一次。
	resetThemeCache()
	forgetHashedAssetManifests()

	response := get(t, router, "/themes/default/dist/index.html")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200", response.Code)
	}
	if response.Body.String() == themeCacheHTML {
		t.Fatal("the truncated cache was served instead of being rebuilt")
	}
	if body := response.Body.Bytes(); !bytes.Equal(body, raw[IndexFile]) {
		t.Errorf("the rebuilt document is not the archive's (%d bytes vs %d)", len(body), len(raw[IndexFile]))
	}
}

// waitForThemeCacheMarker 轮询等待默认主题的缓存出现 .complete，返回缓存目录。
//
// 预热是异步的（PrewarmDefaultThemeCache 起一个 goroutine），测试只能等它落地。判据用
// 标记文件而不是「目录存在」：目录在解压过程中就已经出现，而标记只在全部条目写完、
// 字节数核对通过、rename 之后才出现 —— 那才是「可以用了」的那一刻。
func waitForThemeCacheMarker(t *testing.T, timeout time.Duration) string {
	t.Helper()
	pattern := filepath.Join(DataDir, themeCacheDirName, "*", themeCacheMarker)
	deadline := time.Now().Add(timeout)
	for {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %q: %v", pattern, err)
		}
		// The marker is written inside the .tmp-<pid> staging directory before the rename, so a
		// glob can see it there first. Only a marker in a *published* directory means the cache
		// is ready; returning the staging path made the caller read a directory that had just
		// been renamed away (caught by Windows CI, whose rename is slower).
		published := make([]string, 0, len(matches))
		for _, match := range matches {
			if strings.Contains(filepath.Base(filepath.Dir(match)), ".tmp-") {
				continue
			}
			published = append(published, match)
		}
		if len(published) == 1 {
			return filepath.Dir(published[0])
		}
		if len(published) > 1 {
			t.Fatalf("%d cache directories carry a completion marker in %s: only one archive hash can be current",
				len(published), filepath.Join(DataDir, themeCacheDirName))
		}
		if time.Now().After(deadline) {
			t.Fatalf("the disk cache did not appear within %s (nothing matched %q)", timeout, pattern)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 启动预热必须真的把缓存建出来，而且整个过程不碰内存副本 —— 预热买的是「第一个访客
// 不用等 300-370ms」，不是「内存里多一份 33 MiB」。
func TestPrewarmMaterialisesTheDiskCache(t *testing.T) {
	t.Chdir(t.TempDir())
	cacheRoot := enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)

	PrewarmDefaultThemeCache()

	dir := waitForThemeCacheMarker(t, 5*time.Second)
	// 标记出现说明文件已经写完；<-themeCacheSettled() 再等后台那次调用整体返回，
	// 这样用例结束时的状态重置不会与它打架。
	<-themeCacheSettled()
	if want := filepath.Join(cacheRoot, embeddedDistArchiveKey()); dir != want {
		t.Fatalf("the prewarmed cache directory = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, IndexFile)); err != nil {
		t.Fatalf("the prewarmed cache has no %q: %v", IndexFile, err)
	}
	if defaultDistFiles != nil {
		t.Fatalf("prewarming loaded the in-memory archive (%d entries); the point is that no request ever needs it", len(defaultDistFiles))
	}
}

// 预热之后的第一个请求必须直接吃缓存（这正是预热的意义），而且仍然不加载内存副本。
func TestTheFirstRequestAfterPrewarmIsServedFromTheCache(t *testing.T) {
	router := newRouter(t)
	enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	forgetHashedAssetManifests()

	PrewarmDefaultThemeCache()
	dir := waitForThemeCacheMarker(t, 5*time.Second)
	<-themeCacheSettled()

	response := get(t, router, "/themes/default/dist/index.html")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200", response.Code)
	}
	archived, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		t.Fatalf("read the cached document: %v", err)
	}
	if !bytes.Equal(response.Body.Bytes(), archived) {
		t.Error("the first request did not serve the prewarmed document")
	}
	if defaultDistFiles != nil {
		t.Errorf("the first request after prewarming loaded the in-memory archive (%d entries)", len(defaultDistFiles))
	}
}

// 缓存被关闭时预热是 no-op：不建目录、不起后台工作、不加载内存副本。
func TestPrewarmIsANoOpWhenTheCacheIsDisabled(t *testing.T) {
	t.Chdir(t.TempDir())
	// 内嵌归档的内存副本可能已经被前面的用例解出来（它们走的就是回退路径），先丢掉，
	// 否则「预热没有加载它」这条断言会去衡量别人留下的状态。
	forgetEmbeddedDistForTest(t)
	// TestMain 关掉了缓存（既有用例走内存回退路径）；这里把那条前提写成断言。
	themeCacheEnabled = false
	resetThemeCache()
	t.Cleanup(resetThemeCache)

	PrewarmDefaultThemeCache()
	// 给一个「本来会写盘」的 goroutine 足够的时间出现。
	time.Sleep(100 * time.Millisecond)

	cacheRoot := filepath.Join(DataDir, themeCacheDirName)
	if _, err := os.Stat(cacheRoot); !os.IsNotExist(err) {
		t.Errorf("prewarming created %q although the disk cache is disabled (stat error %v)", cacheRoot, err)
	}
	if defaultDistFiles != nil {
		t.Errorf("prewarming loaded the in-memory archive (%d entries) although the disk cache is disabled", len(defaultDistFiles))
	}
}

// 重复预热 —— 以及预热之后到来的请求 —— 只能物化一次：一次多余的 33 MiB 写入是白花的
// 磁盘与 CPU，单飞（themeCacheState 的 done）的意义正是这个。
func TestPrewarmDoesNotMaterialiseTwice(t *testing.T) {
	router := newRouter(t)
	enableThemeCacheForTest(t)
	forgetEmbeddedDistForTest(t)
	forgetHashedAssetManifests()

	PrewarmDefaultThemeCache()
	dir := waitForThemeCacheMarker(t, 5*time.Second)
	<-themeCacheSettled()
	marker := filepath.Join(dir, themeCacheMarker)
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat the completion marker: %v", err)
	}

	// 再预热两次（等它们各自返回，别把后台 goroutine 留到用例之后），以及一个请求。
	<-prewarmDefaultThemeCacheAsync()
	<-prewarmDefaultThemeCacheAsync()
	if response := get(t, router, "/themes/default/dist/index.html"); response.Code != http.StatusOK {
		t.Fatalf("GET /themes/default/dist/index.html = %d, want 200", response.Code)
	}

	after, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat the completion marker after the extra prewarms: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the cache was materialised again (%v -> %v): prewarming a finished extraction must be a no-op",
			before.ModTime(), after.ModTime())
	}
	entries, err := filepath.Glob(filepath.Join(DataDir, themeCacheDirName, "*"))
	if err != nil {
		t.Fatalf("glob the cache root: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("%d entries in %s, want exactly one cache directory", len(entries), filepath.Join(DataDir, themeCacheDirName))
	}
}
