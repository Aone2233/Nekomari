package public

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 内嵌归档换成「面板自己的前台构建」之后，第一次真正被服务的这批文件：
// sw.js、registerSW.js、workbox-<hash>.js、manifest.webmanifest、manifest.json、favicon.ico。
//
// 它们此前不在归档里（归档是主题的构建产物），所以两条能坏掉它们、而且都坏得很安静的路径
// 从未被跑过：
//
//  1. Content-Type。serveContent 之前用 mime.TypeByExtension 算类型，而那张表随宿主变化：
//     Go 的内置表里**没有** .webmanifest（本机实测返回 ""），于是 PWA 清单会以「没有
//     Content-Type」发出去；Windows 注册表还把 .mjs 覆盖成 text/plain，而那不是浏览器肯
//     执行的 JavaScript 类型。见 panelPinnedMimeTypes。
//  2. SPA 兜底。noRoute 分支对任何没命中的路径都回 index.html，所以归档里少一个 sw.js
//     得到的是一个 HTTP 200 的 HTML 文档 —— Service Worker 注册以「不支持的 MIME 类型」
//     失败，而页面照常渲染。见 isAssetRequest / assetOnlyExtensions。
//
// 这两条都是「错得很安静」的类型，所以这里一次钉住三件事：类型对不对、字节是不是来自归档
// （而不是外壳）、以及查不到时是不是 404。
//
// 归档本身有没有带上这批文件是**另一半**，由已经在跑的用例覆盖：面板自己的 index.html
// 引用了 /registerSW.js 与 /manifest.json，`TestEveryAssetTheEmbeddedIndexReferencesExists`
// 会为此失败。这里不再重复断言归档内容 —— 归档正在被另一处改动替换，把「它的在途状态」
// 写死进这个文件只会制造与被测代码无关的红灯。

// pwaShellMarker 只出现在本文件合成的外壳文档里。
//
// 一条断言就够判断「这个响应是不是被 SPA 分支顶替了」：外壳里有它，从归档取出来的资源里
// 没有。比断言 Content-Type 更直接 —— 一个 200 + text/html 的响应可能在任何一层看起来都
// 正常，只有内容能证明它是外壳。
const pwaShellMarker = "PWA-SYNTHETIC-SHELL-MARKER-8f2c"

// pwaSWMarker 是合成 sw.js 的内容，用来断言响应体确实来自归档。
const pwaSWMarker = "// synthetic service worker 8f2c"

// syntheticPWAArchive 是一份最小的「面板前台构建」归档。
//
// 文件名取自 `frontend/dist` 的真实产物（sw.js、registerSW.js、workbox-8359a6b2.js、
// manifest.webmanifest、manifest.json、favicon.ico、assets/entry-index-<hash>.js），
// 内容换成标记串。用合成归档而不是真归档，是为了让这个文件断言的是**服务方式**：归档正在
// 被替换，而这些行为不该随它的构成变化。
func syntheticPWAArchive() map[string][]byte {
	return map[string][]byte{
		// 外壳：任何「本该是资源却拿到外壳」的响应都会带上 pwaShellMarker。
		IndexFile: []byte(`<!doctype html><html lang="en"><head><title>Nekomari Monitor</title>` +
			`</head><body>` + pwaShellMarker + `<div id="root"></div></body></html>`),
		// 构建清单：让 assets/ 下的产物被认成「带内容哈希」的那一类。
		".vite/manifest.json": []byte(`{"src/main.tsx":{"file":"assets/entry-index-0badc0de.js","isEntry":true}}`),

		"assets/entry-index-0badc0de.js": []byte("// entry bundle 8f2c\n"),

		// 面板自己的 PWA 文件，全部在归档根上（这正是 /sw.js 这类 URL 解析到的地方）。
		"sw.js":                []byte(pwaSWMarker + "\n"),
		"registerSW.js":        []byte("// register 8f2c\n"),
		"workbox-8359a6b2.js":  []byte("// workbox 8f2c\n"),
		"manifest.webmanifest": []byte(`{"name":"Nekomari synthetic 8f2c"}`),
		"manifest.json":        []byte(`{"name":"Nekomari synthetic 8f2c"}`),
		FaviconFile:            []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x08, 0x08},
	}
}

// withSyntheticArchive 把包级 defaultDistFiles 换成一份合成归档，并在用例结束时还原。
//
// 同时清掉 hashedAssetCache：构建清单的解析结果按主题 ID 缓存，换了归档而不清缓存，
// openAsset 与 themeHashedAssets 就会一个读新、一个读旧。
func withSyntheticArchive(t *testing.T, files map[string][]byte) {
	t.Helper()
	previous := defaultDistFiles
	defaultDistFiles = files
	forgetHashedAssetManifests()
	t.Cleanup(func() {
		defaultDistFiles = previous
		forgetHashedAssetManifests()
	})
}

func forgetHashedAssetManifests() {
	hashedAssetCache.Delete(DefaultTheme)
	hashedAssetCache.Delete("admin")
}

// pwaRouter 构造一个用内嵌归档提供静态资源的 public 路由，配置库是空的内存库（主题键因此
// 取默认值 DefaultTheme），工作目录是临时目录（./data 里没有主题、也没有自定义 favicon）。
//
// 与 admin_theme_test.go 的 newRouter 做同一件事，但刻意另起一个名字：那个文件由另一处
// 改动负责，共用它的辅助函数会让这个文件因为一次无关的重命名而编译不过。
func pwaRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)

	router := gin.New()
	Static(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})
	return router
}

func pwaGet(t *testing.T, router *gin.Engine, requestPath string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, requestPath, nil))
	return recorder
}

// 面板自己的 PWA 文件必须从内嵌归档取出来，带能用得上的 Content-Type，而且**不是外壳**。
func TestPWAArchivesAreServedFromTheEmbeddedArchive(t *testing.T) {
	router := pwaRouter(t)
	withSyntheticArchive(t, syntheticPWAArchive())

	cases := []struct {
		path string
		// exactType 非空时要求 Content-Type 完全等于它。只对面板钉死的那几种类型这么断言：
		// .json/.ico 的类型由宿主的 MIME 表决定（Windows 注册表 vs Linux 的 mime.types），
		// 钉死它们等于让这条断言在写它的机器上通过、在别处失败 —— 那种测试比没有更糟。
		exactType string
		// typePrefix 非空时要求 Content-Type 以它开头。
		typePrefix string
		// bodyMarker 必须出现在响应体里，证明内容来自归档而不是外壳。
		bodyMarker string
		// wantCacheContains 非空时要求 Cache-Control 含这一段。
		wantCacheContains string
	}{
		{path: "/sw.js", exactType: "text/javascript; charset=utf-8", bodyMarker: pwaSWMarker, wantCacheContains: "no-store"},
		{path: "/registerSW.js", exactType: "text/javascript; charset=utf-8", bodyMarker: "// register 8f2c"},
		{path: "/workbox-8359a6b2.js", exactType: "text/javascript; charset=utf-8", bodyMarker: "// workbox 8f2c"},
		{path: "/manifest.webmanifest", exactType: "application/manifest+json", bodyMarker: "Nekomari synthetic 8f2c"},
		{path: "/manifest.json", bodyMarker: "Nekomari synthetic 8f2c"},
		{path: "/favicon.ico", typePrefix: "image/"},
		{path: "/assets/entry-index-0badc0de.js", exactType: "text/javascript; charset=utf-8",
			bodyMarker: "// entry bundle 8f2c", wantCacheContains: "max-age=31536000"},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			recorder := pwaGet(t, router, tc.path)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200: this is a file the panel build ships", tc.path, recorder.Code)
			}
			body := recorder.Body.String()
			if strings.Contains(body, pwaShellMarker) {
				t.Errorf("GET %s was answered with the SPA shell instead of the file from the archive", tc.path)
			}
			if tc.bodyMarker != "" && !strings.Contains(body, tc.bodyMarker) {
				t.Errorf("GET %s did not return the archive's bytes:\n%s", tc.path, body[:min(300, len(body))])
			}

			contentType := recorder.Header().Get("Content-Type")
			if strings.Contains(strings.ToLower(contentType), "html") {
				t.Errorf("GET %s got Content-Type %q: a browser refuses that type here and the error points at MIME, not at the file",
					tc.path, contentType)
			}
			if contentType == "" {
				t.Errorf("GET %s sent no Content-Type at all: the next layer sniffs it, and a service worker or a PWA manifest is exactly where that fails",
					tc.path)
			}
			if tc.exactType != "" && contentType != tc.exactType {
				t.Errorf("GET %s got Content-Type %q, want %q", tc.path, contentType, tc.exactType)
			}
			if tc.typePrefix != "" && !strings.HasPrefix(contentType, tc.typePrefix) {
				t.Errorf("GET %s got Content-Type %q, want it to start with %q", tc.path, contentType, tc.typePrefix)
			}
			if tc.wantCacheContains != "" {
				if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, tc.wantCacheContains) {
					t.Errorf("GET %s got Cache-Control %q, want it to contain %q", tc.path, got, tc.wantCacheContains)
				}
			}
		})
	}
}

// 真实归档的现场验证：内嵌归档现在已经换成了面板自己的前台构建，这批文件第一次真的在
// 归档根上 —— 所以这里直接把它们取出来，逐个请求，并把响应体与归档里的字节逐字节比对。
//
// 与上一个用例的分工：那个用合成归档钉「服务方式」（不随归档构成变化），这个钉「真实产物
// 确实能用」。两者都需要：前者防行为漂移，后者防归档丢文件 —— 而归档丢文件正是这批文件
// 此前从未被服务过的原因（那时的归档是主题的构建产物，里面没有 sw.js）。
//
// 「必须在归档里」是硬断言，是有意的：面板自己的 index.html 用 <script src="/registerSW.js">
// 注册 Service Worker，而 registerSW.js 在运行时会去取 /sw.js —— 少了任何一个，PWA/离线
// 这半边就是静默失效。workbox 的文件名带内容哈希，所以按前缀发现，不写死。
//
// 唯一的前置条件：归档得**是**面板自己的前台构建。归档换成它正是这批文件第一次被服务的
// 原因（换之前归档是主题的构建产物，里面没有 sw.js），而这次替换由另一处改动负责、可能
// 正在途中。在它落地之前，此处"缺文件"是构型在途而不是服务路径坏了 —— 那正是
// embedded_theme_test.go / panel_routes_test.go 现在一并报告的那批失败 —— 所以这种情况
// 下跳过而不是误报。落地之后（判据与那两个文件用的是同一个：面板前端的标题）这里是硬断言。
func TestThePanelsPWAFilesInTheRealArchiveAreServed(t *testing.T) {
	if len(defaultDistFiles) == 0 {
		t.Fatal("the embedded archive did not load, so nothing here can be judged")
	}
	if !embeddedArchiveIsThePanelsFrontEnd() {
		t.Skip("the embedded archive is not the panel's own front end yet, so its PWA files cannot be there: " +
			"the archive swap is still in flight. Same condition as embedded_theme_test.go's \"belongs to the " +
			"panel's own build\" assertions, which fail for that reason right now.")
	}
	router := pwaRouter(t)

	names := []string{"sw.js", "registerSW.js", "manifest.webmanifest", "manifest.json", FaviconFile}
	for name := range defaultDistFiles {
		if strings.HasPrefix(name, "workbox-") && strings.HasSuffix(name, ".js") {
			names = append(names, name)
		}
	}

	for _, name := range names {
		content, embedded := defaultDistFiles[name]
		if !embedded {
			t.Errorf("%q is not in the embedded archive: the panel's own build emits it, and its absence is only visible as a service worker that never registers", name)
			continue
		}
		recorder := pwaGet(t, router, "/"+name)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET /%s = %d, want 200", name, recorder.Code)
			continue
		}
		if body := recorder.Body.Bytes(); !bytes.Equal(body, content) {
			t.Errorf("GET /%s returned %d bytes that are not the archive's %d: it did not come from the embedded archive",
				name, len(body), len(content))
		}
		contentType := recorder.Header().Get("Content-Type")
		if contentType == "" {
			t.Errorf("GET /%s sent no Content-Type", name)
			continue
		}
		if strings.Contains(strings.ToLower(contentType), "html") {
			t.Errorf("GET /%s got Content-Type %q, so it is not being served as the file it is", name, contentType)
		}
		// 面板自己的这几条路径一旦查不到就必须 404（见 isAssetRequest）：它们都在那张表里，
		// 所以「归档里没有」在线上是 404，而不是一个带着 200 的 HTML 外壳。
		if !isAssetRequest("/" + name) {
			t.Errorf("isAssetRequest(%q) = false: a missing %s would be answered with the SPA shell instead of a 404", "/"+name, name)
		}
	}
}

// embeddedArchiveIsThePanelsFrontEnd 判断内嵌归档里的 index.html 是不是**面板自己**的前台
// 文档，而不是某个主题的。
//
// 判据两条，满足其一即可：面板前端的标题（`<title>Nekomari Monitor</title>`，也正是
// embedded_theme_test.go 与 panel_routes_test.go 用来判定同一件事的那个字符串），或者它
// 引用了 `/registerSW.js` —— vite-plugin-pwa 注入的注册脚本，主题的文档不会有。两条都用，
// 是因为其中一条换掉时另一条还在；两条都换掉意味着面板前端的身份变了，那时这三个文件要
// 一起改。
func embeddedArchiveIsThePanelsFrontEnd() bool {
	document := defaultDistFiles[IndexFile]
	if len(document) == 0 {
		return false
	}
	return bytes.Contains(document, []byte("<title>Nekomari Monitor</title>")) ||
		bytes.Contains(document, []byte("/registerSW.js"))
}

// 上一个用例的前置条件本身。判宽了会跳过本该失败的真回归，判严了会在归档还没换到位时
// 制造与被测代码无关的红灯 —— 两种都值得钉住。
func TestEmbeddedArchiveIsThePanelsFrontEnd(t *testing.T) {
	previous := defaultDistFiles
	t.Cleanup(func() { defaultDistFiles = previous })

	cases := []struct {
		name     string
		document string
		want     bool
	}{
		{
			name:     "the panel's own front end, identified by its title",
			document: `<!doctype html><html><head><title>Nekomari Monitor</title></head><body><div id="root"></div></body></html>`,
			want:     true,
		},
		{
			// 标题被改掉时的第二条判据：vite-plugin-pwa 注入的注册脚本。
			name:     "the panel's own front end, identified by its service-worker registration",
			document: `<!doctype html><html><head><title>Whatever</title><script src="/registerSW.js"></script></head><body></body></html>`,
			want:     true,
		},
		{
			name:     "an installed theme's document",
			document: `<!doctype html><html><head><title>Komari-Theme-LuminaPlus</title></head><body><div id="root"></div></body></html>`,
			want:     false,
		},
		{name: "an empty archive", document: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defaultDistFiles = map[string][]byte{IndexFile: []byte(tc.document)}
			if got := embeddedArchiveIsThePanelsFrontEnd(); got != tc.want {
				t.Errorf("embeddedArchiveIsThePanelsFrontEnd() = %v, want %v (%s)", got, tc.want, tc.name)
			}
		})
	}
}

// 查不到的构建产物必须 404，而且响应**不是 HTML**。
//
// 这条断言针对的是一个被 HTTP 200 掩盖的故障：SPA 分支用 index.html 回答一个
// /assets/<过期哈希>.js，浏览器把 text/html 当 JavaScript 解析，报的是 MIME 错误而不是
// 「这个文件不存在」；服务器日志、CDN 日志、网络面板的状态码全都是 200。
func TestAMissingAssetIs404AndNotTheShell(t *testing.T) {
	router := pwaRouter(t)
	withSyntheticArchive(t, syntheticPWAArchive())

	for _, requestPath := range []string{
		"/assets/does-not-exist-D4JKo971.js",
		"/assets/does-not-exist-D4JKo971.css",
		"/assets/does-not-exist-D4JKo971.png",
		// 生产上真实发生过的那一次：主题背景图不在归档里，页面拿到的是 200 的 HTML，
		// 于是「图片缺失」在 cache_headers_test.go 里表现为一条缓存策略不符。
		"/assets/bg-desktop-light.v2.jpeg",
		// 面板自己的 PWA 文件少了一个，同样必须是 404。
		"/workbox-00000000.js",
		"/registerSW-absent.js",
		"/manifest-absent.webmanifest",
	} {
		recorder := pwaGet(t, router, requestPath)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: an extension this specific names a file, and a 200 here is what hides the real fault",
				requestPath, recorder.Code)
		}
		body := recorder.Body.String()
		if strings.Contains(body, pwaShellMarker) {
			t.Errorf("GET %s was answered with the SPA shell:\n%s", requestPath, body[:min(300, len(body))])
		}
		if strings.Contains(strings.ToLower(body), "<html") || strings.Contains(strings.ToLower(body), "<!doctype") {
			t.Errorf("GET %s returned an HTML document, so a browser would report a MIME error instead of a missing file", requestPath)
		}
		contentType := strings.ToLower(recorder.Header().Get("Content-Type"))
		if strings.Contains(contentType, "html") {
			t.Errorf("GET %s got Content-Type %q, want no HTML type on a 404", requestPath, contentType)
		}
	}
}

// 边界的另一半：前端路由、.html 文档与带点的未知后缀仍然回外壳。
//
// 这条与上一条是一对。只钉 404 会让后来的人以为「扩展名就该 404」，然后把 /sla.html 和
// /version/1.2.3 一起变成错误页 —— 而独立页面的地址正是 .html（roadmap H0/H1 的决定），
// 带点的路径正是前端路由的形状。
func TestRoutesAndDotCarryingPathsStillGetTheShell(t *testing.T) {
	router := pwaRouter(t)
	withSyntheticArchive(t, syntheticPWAArchive())

	for _, requestPath := range []string{
		"/",
		"/install",
		"/database-recovery",
		"/dashboard",
		"/instance/0f8fad5b-d9cb-469f-a165-70867728950e",
		"/plugin/demo/page",
		"/sla.html",            // 独立页面按计划就放在 .html 上
		"/does-not-exist.html", // .html 缺失时仍给文档：外壳本身就是 HTML，类型不会错配
		"/index.html",
		"/version/1.2.3", // 未知后缀：前端路由
		"/server/example.com",
		"/a.b/c", // 带点的目录名不是扩展名
	} {
		recorder := pwaGet(t, router, requestPath)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200: this path is a route or a document, not a file", requestPath, recorder.Code)
			continue
		}
		if !strings.Contains(recorder.Body.String(), pwaShellMarker) {
			t.Errorf("GET %s did not get the shell, so a client-side route stopped resolving:\n%s",
				requestPath, recorder.Body.String()[:min(300, recorder.Body.Len())])
		}
		if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
			t.Errorf("GET %s got Content-Type %q, want the shell's HTML type", requestPath, got)
		}
	}
}

// 判据表本身，含三条刻意留空的边界。
func TestIsAssetRequestBoundary(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/assets/chunk-Ab12Cd34.js", true},
		{"/assets/does-not-exist-D4JKo971.js", true},
		{"/assets/bg-desktop-light.v2.jpeg", true},
		{"/fonts/inter.woff2", true},
		{"/sw.js", true},
		{"/registerSW.js", true},
		{"/workbox-8359a6b2.js", true},
		{"/manifest.webmanifest", true},
		{"/manifest.json", true},
		{"/favicon.ico", true},
		// .js 的大写形式也要认（URL 不区分大小写，宿主文件系统可能是）
		{"/ASSETS/CHUNK-AB12CD34.JS", true},

		// 没有扩展名：前端路由
		{"/", false},
		{"/dashboard", false},
		{"/install", false},
		{"/database-recovery", false},
		{"/plugin/demo", false},
		// .html：策略上回落到外壳
		{"/sla.html", false},
		{"/index.html", false},
		{"/does-not-exist.html", false},
		// 未知后缀：真实的前端路由里就有带点的
		{"/version/1.2.3", false},
		{"/server/example.com", false},
		// 带点的目录名不是扩展名
		{"/a.b/c", false},
		{"/foo.png/bar", false},
	}
	for _, tc := range cases {
		if got := isAssetRequest(tc.path); got != tc.want {
			t.Errorf("isAssetRequest(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// 类型钉死的那几个必须与宿主无关。这条用例的价值不在当下（本机 mime 表恰好给出可用的
// 值），而在于它把「.webmanifest 的类型不能是空串」变成一条在 Linux CI 上也成立的断言：
// Go 的内置表里没有 .webmanifest，那里得到的就是空串。
func TestPinnedMimeTypesDoNotDependOnTheHost(t *testing.T) {
	for extension, want := range map[string]string{
		".js":          "text/javascript; charset=utf-8",
		".mjs":         "text/javascript; charset=utf-8",
		".webmanifest": "application/manifest+json",
	} {
		if got := assetMimeType(extension); got != want {
			t.Errorf("assetMimeType(%q) = %q, want %q", extension, got, want)
		}
		// 大写扩展名（Windows 上的 .JS 文件）也必须命中同一张表。
		if got := assetMimeType(strings.ToUpper(extension)); got != want {
			t.Errorf("assetMimeType(%q) = %q, want %q", strings.ToUpper(extension), got, want)
		}
	}
	// 没钉死的扩展名仍然走 mime —— 这张表只覆盖面板依赖其类型的那几个。
	if got := assetMimeType(".css"); !strings.Contains(got, "text/css") {
		t.Errorf("assetMimeType(\".css\") = %q, want the mime table's CSS type", got)
	}
}
