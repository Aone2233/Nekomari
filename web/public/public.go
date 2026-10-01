package public

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/gin-gonic/gin"
)

//go:embed defaultTheme/komari-theme.json
var PublicFS embed.FS

//go:embed defaultTheme/dist.tar.zst
var embeddedDistArchive []byte

// embeddedAssetModTime 是嵌入资源的修改时间。
//
// 嵌入文件没有真实文件时间，而 http.ServeContent 只有在 modTime 非零时才会发
// Last-Modified、才做条件请求协商。用进程启动时间作为它们的稳定校验值：进程存活
// 期间嵌入内容不变，所以校验值稳定、条件请求能命中 304；换二进制重启后时间变化，
// 缓存自然失效并回源 —— 这正是升级后需要的行为。
var embeddedAssetModTime = time.Now()

// assetSource 是一个已解析的静态资源。
//
// 为什么返回 io.ReadSeeker 而不是 []byte：http.ServeContent 需要 Seek 来支持
// Range 与长度计算，本地主题文件因此不必再整份读进内存（0.5MB 的背景图也一样）。
type assetSource struct {
	reader   io.ReadSeeker
	modTime  time.Time
	mimeType string
	size     int64
	// close 释放底层文件句柄；嵌入资源为 nil。
	close func()
}

// hashedAssetCache 按主题缓存构建清单的解析结果（themeID -> 产物路径集合）。
//
// 用 sync.Map 是因为请求是并发的，而清单在进程生命周期内不变（换主题或换版本会重新
// 加载主题），所以每个主题只解析一次。
var hashedAssetCache sync.Map

// hashedAssetsFromManifest 解析 Vite 的构建清单，返回它列出的产物路径集合
// （清单里的 "file" 字段，例如 "assets/chunk-x-Ab12Cd34.js"）。
//
// 为什么用清单而不是按文件名猜：assets/ 下并非全都带哈希 —— 实际产物里
// pwa-icon.webp 与 edit_117847723_p0.webp 就没有；而 Vite 的 base64url 哈希
// 本身可以含 '-'（index-Kbf1m-l1.js 的哈希是 Kbf1m-l1）。按模式匹配会把
// logo-v2Final1.png 这类普通文件名误判成哈希文件，代价是用户长期看到过期资源。
// 清单是构建自己写的，列出的就是它生成的每一个文件，没有猜测。
//
// 解析失败或没有清单（第三方主题可能没有）时返回 nil：调用方据此不发长期缓存头，
// 退回改动前的行为 —— 少缓存是安全的，缓存错不是。
func hashedAssetsFromManifest(data []byte) map[string]struct{} {
	var manifest map[string]struct {
		File string   `json:"file"`
		CSS  []string `json:"css"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	if len(manifest) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(manifest))
	for _, entry := range manifest {
		if entry.File != "" {
			out[entry.File] = struct{}{}
		}
		// A build's stylesheets are listed in a separate `css` array, not as their own entry, and they carry
		// a content hash just like the scripts. Missing them meant **every stylesheet from every build lost
		// its long-lived cache header** — silently, because the files still served. Found while adding the
		// admin, whose document references a stylesheet; the theme and the panel had the same gap.
		for _, css := range entry.CSS {
			if css != "" {
				out[css] = struct{}{}
			}
		}
	}
	return out
}

// isHashedAsset 判断 name（相对主题根，例如 "dist/assets/a.js"）是否是清单里
// 列出的构建产物。
//
// 先去前导 "/"：路由参数 /themes/:id/*path 拿到的路径是带前导斜杠的
// （openAsset 也做了同样的处理），不归一化就会永远匹配不上，长期缓存头静默失效。
func isHashedAsset(name string, hashedAssets map[string]struct{}) bool {
	if len(hashedAssets) == 0 {
		return false
	}
	normalized := strings.TrimPrefix(filepath.ToSlash(name), "/")
	normalized = strings.TrimPrefix(normalized, DistDir+"/")
	_, ok := hashedAssets[normalized]
	return ok
}

// serveAsset 用 http.ServeContent 输出一个静态资源。
//
// 为什么不再用 c.Data：那会整份读进内存，而且没有 Last-Modified/ETag/Range，
// 每次请求都要回源全量传输。ServeContent 用修改时间与 ETag 协商 304、原生支持
// Range，对 Cloudflare 的回源也友好。
//
// Content-Type 仍然沿用解析时算出的结果：扩展名没有已知类型时显式把头部置空，
// 阻止 ServeContent 改成嗅探内容 —— 保持原有的响应行为。
func serveAsset(c *gin.Context, source assetSource, name string, hashedAssets map[string]struct{}) {
	if source.close != nil {
		defer source.close()
	}
	header := c.Writer.Header()
	if source.mimeType == "" {
		header["Content-Type"] = nil
	} else {
		header.Set("Content-Type", source.mimeType)
	}
	// 与 web/filemanager 的下载路径同一个形状：长度-纳秒时间。
	header.Set("ETag", fmt.Sprintf("\"%x-%x\"", source.size, source.modTime.UnixNano()))
	// 构建产物（文件名带内容哈希）可以长期缓存：内容变了文件名就变了。
	// 没有这一行时源站不发任何 Cache-Control，Cloudflare 会套用自己的默认值
	// （实测 4 小时），于是每个 POP 每 4 小时都要回源重取一次这些永远不变的文件 ——
	// 白白制造回源流量，而回源正是最容易出问题的那一段。
	//
	// 其余资源显式声明为「每次协商」而不是留空。
	//
	// 留空曾经是这里的做法，注释里写的是「保持原样」—— 而「原样」就是源站不发 Cache-Control，
	// 于是 Cloudflare 套用自己的 4 小时（实测就是这个值）。这与「index.html 必须能立刻更新」
	// 的意图正好相反，并且造成过一次真实故障：主题的背景图是 public/ 下没有内容哈希的文件，
	// 我替换主题时它们短暂缺失，那个 404 就被边缘缓存下来，之后即便文件已经就位，页面背景
	// 在用户浏览器里仍然是空白，直到硬刷新。
	//
	// `no-cache` 的含义是「可以缓存，但每次必须回源校验」，配合上面那行 ETag 就能得到 304，
	// 不重复传内容。关键是它把决定权留在源站，而不是让中间层替我们决定缓存多久。
	if isHashedAsset(name, hashedAssets) {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// 服务外壳与 Service Worker 不能走协商缓存：`no-cache` 仍允许中间层保留一份副本，
		// 而这两者一旦被保留，下一次部署就拖住了整个应用。
		//
		// 比的是 path.Base(name) 而不是 name：调用方交上来的是**相对主题根**的路径
		// （SPA 分支给的是 path.Join(DistDir, reqPath)，即 "dist/sw.js"；主题路由给的是
		// URL 参数 "/dist/sw.js"）。按整串比较时 "sw.js" 这一支永远不会命中，于是面板
		// 自己的 Service Worker 拿的是 `no-cache` —— 而这段注释一直写着它拿的是
		// `no-store`。归档换成面板自己的前台构建之前，归档里根本没有 sw.js，
		// 这条路径从未被真正服务过，所以这个不一致一直没被看见。
		switch path.Base(name) {
		case "index.html", "sw.js":
			header.Set("Cache-Control", "no-store")
		default:
			header.Set("Cache-Control", "no-cache")
		}
	}
	http.ServeContent(c.Writer, c.Request, name, source.modTime, source.reader)
}

// panelPinnedMimeTypes 是面板**依赖**其 Content-Type 的少数几个扩展名。
//
// 为什么不能只用 mime.TypeByExtension：那个表随宿主变化。Windows 上它读注册表
// (HKEY_CLASSES_ROOT\<ext>\Content Type)，Linux 上读 /usr/share/mime/globs2 或
// /etc/mime.types，而一个精简容器里这些文件可能一个都没有。同一份二进制因此在开发机、
// CI 与生产上给出不同的响应头，而下面这两个扩展名的类型**决定了功能能不能用**：
//
//   - .js / .mjs：Service Worker 与它的 workbox 依赖（sw.js、registerSW.js、
//     workbox-<hash>.js）只要拿到的不是 JavaScript 类型，浏览器就拒绝注册
//     ("The script has an unsupported MIME type")，表现是「离线/PWA 功能静默失效」而
//     页面本身照常渲染。Go 内置表给 .js 的是 text/javascript; charset=utf-8，但注册表
//     会覆盖它 —— 本机实测 .js 得到 application/javascript（合法），而 .mjs 得到
//     text/plain; charset=utf-8（**不合法**：模块脚本会被直接拒绝执行）。钉死这两个，
//     响应就不再取决于运行它的机器。
//   - .webmanifest：Go 的内置表**根本没有**这个扩展名（本机实测
//     mime.TypeByExtension(".webmanifest") == ""），而 serveAsset 在 mimeType 为空时会
//     把 Content-Type 显式置空 —— 于是 PWA 清单会以「没有任何 Content-Type」的形式发出
//     去。清单是这几条路径里唯一必须靠类型才被认下的文件：浏览器按 JSON 类型解析它
//     (vite-plugin-pwa 的默认产物名就是 manifest.webmanifest)，而空 Content-Type 连
//     JSON 都不是。Chromium 正在把「必须是 JSON 类型」变成显式要求
//     (issues.chromium.org/issues/562035733)。
//
// 只钉这三个：其余扩展名（.json/.css/.ico/.png ...）即使宿主给出的类型不可意，浏览器
// 仍然按内容使用它 —— 为它们引入一张私有表只会制造「本机与生产不一致」的新来源，而
// 这正是这里要消除的东西。
var panelPinnedMimeTypes = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".webmanifest": "application/manifest+json",
}

// assetMimeType 返回某个扩展名对应的 Content-Type：面板依赖的先钉死，其余交给 mime。
func assetMimeType(extension string) string {
	if pinned, ok := panelPinnedMimeTypes[strings.ToLower(extension)]; ok {
		return pinned
	}
	return mime.TypeByExtension(extension)
}

// embeddedAsset 把嵌入内容包装成 assetSource。
func embeddedAsset(embedPath string, content []byte) assetSource {
	return assetSource{
		reader:   bytes.NewReader(content),
		modTime:  embeddedAssetModTime,
		mimeType: assetMimeType(filepath.Ext(embedPath)),
		size:     int64(len(content)),
	}
}

// 常量定义
const (
	DataDir            = "./data"
	ThemesDir          = "theme"
	FaviconFile        = "favicon.ico"
	DefaultTheme       = "default"
	LanguageCookieName = "language"

	// 主题内部结构定义
	DistDir   = "dist"       // 静态资源存放目录
	// AdminDistDir 是内置归档里面板自带后台界面的子树（roadmap H7）。归档里 dist/ 的内容在根上
	// （内置主题去掉前缀），所以后台单独放在 admin/ 下，避免与主题的 index.html 和 assets/ 相撞。
	AdminDistDir = "admin"
	IndexFile = "index.html" // 相对于 DistDir
)

func init() {
	_ = os.MkdirAll("./data/theme", 0755)

	var err error
	defaultDistFiles, err = loadEmbeddedDist()
	if err != nil {
		panic("load embedded default frontend: " + err.Error())
	}
}

// shellCacheControl 是 SPA 外壳的缓存策略：够短，能被边缘缓存，过期后走校验。
const shellCacheControl = "public, max-age=60, must-revalidate"

// writeShell 是 SPA 外壳唯一的出口：补 ETag 与一条短缓存。
//
// 为什么现在可以缓存：去掉原来按 language cookie 改写 `<html lang>` 那一步之后（见
// serveIndex 里的注释），这个响应只依赖站点级设置 —— 主题、站名、描述、自定义
// head/body、favicon 版本。同一条 URL 对每个人都是同一份内容，它才可以被缓存。
//
// 为什么是 60 秒而不是更久：源站渲染只要 1 毫秒，贵的是 Cloudflare 回源那一跳（实测
// 500 毫秒以上，因为它把不带缓存头的 HTML 当 DYNAMIC 处理）。60 秒足够让绝大多数打开
// 命中边缘，同时把「改了站点设置多久生效」压在 1 分钟以内。带 must-revalidate：过期
// 后靠 ETag 走 304，不会把这几 KB 重复传一遍。
//
// 这里不能像带内容哈希的构建产物那样用 immutable —— 外壳文件名不带哈希，它必须能更新。
func writeShell(c *gin.Context, body []byte) {
	sum := fnv.New64a()
	_, _ = sum.Write(body)
	etag := fmt.Sprintf("\"%x\"", sum.Sum64())
	c.Header("ETag", etag)
	c.Header("Cache-Control", shellCacheControl)
	if strings.Contains(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	// c.Data 会带上 Content-Length；靠 chunked 传输的响应 CDN 更不愿意缓存。
	c.Data(http.StatusOK, "text/html; charset=utf-8", body)
}

func stripServiceWorkerRegistration(html string) string {
	return strings.ReplaceAll(html, `<script id="vite-plugin-pwa:register-sw" src="/registerSW.js"></script>`, "")
}

// panelOwnedPrefixes are the paths whose document belongs to the panel itself rather than
// to whichever theme is installed.
//
// A theme replaces the public front end, and with it the router: everything a route in
// `frontend/src/routes.ts` under a themed prefix needs is gone. `/install` is exactly that — a route
// of the panel's own front end — and it used to fall through to `noRoute`/`serveIndex`, which answered
// it with the *installed theme's* document. Production returned the SAO theme page for `curl /install`
// while `/admin` correctly returned the panel.
//
// Being listed here IS the fix, and it is the whole fix: `serveIndex` pins a panel-owned path to the
// *built-in* front end (`currentTheme = DefaultTheme`, no theme-variable substitution), so the
// installed theme never gets to answer these paths.
//
// Registering them as explicit routes instead — which an earlier revision of this change did — is
// wrong twice over. `/database-recovery` already has a route in `internal/server/runtime.go`
// (the normal server redirects it to `/`, because the recovery UI belongs to its temporary restricted
// listener), so a second registration panics with `handlers are already registered for path
// '/database-recovery'`; and `serveAdminDocument` serves the *admin* bundle, whereas `/install` and
// `/database-recovery` are routes of the front-end app. The corrected mechanism is exercised by
// panel_routes_test.go.
//
// `/plugin/*` is deliberately absent: plugin pages and plugin assets share the prefix and
// need their own decision, so it is left to a separate change rather than half-fixed here.
var panelOwnedPrefixes = []string{"/admin", "/terminal", "/install", "/database-recovery"}

// isPanelOwned reports whether path is the panel's own document or a client-side route
// under one of its prefixes.
func isPanelOwned(requestPath string) bool {
	for _, prefix := range panelOwnedPrefixes {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}

// assetOnlyExtensions 是「光看扩展名就能断定客户端在要一个**文件**、而不是在要一条前端
// 路由」的扩展名集合。它决定 SPA 分支在什么都找不到时回 404 还是回 index.html。
//
// 为什么需要这条规则：SPA 分支对任何没命中的路径都回外壳。对一个过期的构建产物
// （/assets/chunk-<hash>.js）来说，那是把 text/html 当成 JavaScript 交出去 —— 浏览器报
// 的是 "Failed to load module script: Expected a JavaScript module script but the server
// responded with a MIME type of text/html"，而不是一个一眼能看懂的 404。真正的原因
// （「这份文件没有部署上去 / 浏览器还拿着上一版的 chunk 名字」）被 HTTP 200 掩盖：服务器
// 日志、CDN 日志、网络面板的状态码全都正常。改成这条规则之前实测到的就是这个形状：
// `GET /assets/bg-desktop-light.v2.jpeg`（主题的背景图，当时不在内嵌归档里）回的是
// 200 + 外壳 HTML，Cache-Control 是外壳的 `public, max-age=60, must-revalidate` ——
// 一个状态码完全正常、内容却是一个文档的响应，唯一表现出来的是浏览器控制台里的 MIME 错误。
//
// **边界**（这一段就是选择本身，改动前请先读它）：
//
//   - 在下面这张表里：404，绝不回外壳。表里的类型来自两处证据 —— 内嵌归档与面板前端
//     构建里**真实出现过**的扩展名（.js .css .json .webmanifest .txt .ico .svg .webp
//     .ttf），加上主题会提供的常见图片/字体/媒体类型。
//   - `.html` / `.htm`：**回落**，不 404。外壳本身就是 HTML 文档，客户端的期望类型因此
//     得到满足，不存在 MIME 错配；而面板恰好把文档放在 .html 这类 URL 上（独立页面
//     /sla.html 就是 roadmap H0/H1 定下的地址），服务端无法区分「文件不存在」与
//     「前端路由」。这里是策略选择而不是安全边界：判错的代价是给了一个文档。
//   - 没有扩展名：**回落**。这正是前端路由的形状（/、/dashboard、/instance/<uuid>、
//     /plugin/<short>/...）。
//   - 其余任何未知扩展名（.5、.com、.local …）：**回落**。一个我们没见过的后缀不是
//     「客户端在要文件」的证据，而真实的前端路由里就有带点的（/version/1.2.3、
//     /server/example.com）；为了多抓几个 404 把它们变成错误页是不划算的。代价是这些
//     后缀上的过期文件仍然会拿到外壳 —— 但它们不是构建产物，不会带内容哈希。
//
// 判据必须是这张固定的表，**不能**用 mime.TypeByExtension(ext) != ""：那张表取决于宿主
// （Windows 注册表、/etc/mime.types、/usr/share/mime/globs2），.webmanifest 在 Go 的
// 内置表里就不存在 —— 用「查得到 MIME 类型吗」当判据，会让这条规则在开发机、CI 与生产
// 之间行为不同，而 .webmanifest 恰恰是面板 PWA 清单的扩展名。
var assetOnlyExtensions = map[string]struct{}{
	".js": {}, ".mjs": {}, ".cjs": {}, ".css": {}, ".map": {}, ".json": {},
	".webmanifest": {}, ".txt": {}, ".xml": {}, ".wasm": {}, ".csv": {},
	".ico": {}, ".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {},
	".avif": {}, ".bmp": {}, ".svg": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".mp4": {}, ".webm": {}, ".mp3": {}, ".ogg": {}, ".wav": {},
	".pdf": {}, ".zip": {}, ".gz": {}, ".zst": {}, ".br": {},
}

// isAssetRequest 报告请求路径是否在要一个文件（而不是一条前端路由），即「查不到就必须
// 404」的那一类。边界的选取与理由见 assetOnlyExtensions 的注释。
//
// 用 path.Ext 而不是 filepath.Ext：这里分级的是 URL 路径，它永远用 "/" 分隔，path.Ext 就是
// 为这种输入准备的（servePanelPath 用的也是它）。两者在 URL 路径上结论一致 —— Windows 的
// os.IsPathSeparator 同时接受 '/' 与 '\\' —— 所以这不是在修一个差异，只是让函数与输入的
// 性质对上；真正会出错的是把 URL 路径交给 filepath 家族里那些**只认 os 分隔符**的函数
// （Clean/Join），那正是 openAsset 里每处都补了 filepath.ToSlash 的原因。
func isAssetRequest(requestPath string) bool {
	ext := path.Ext(requestPath)
	if ext == "" {
		return false
	}
	_, ok := assetOnlyExtensions[strings.ToLower(ext)]
	return ok
}

// isSafePath 验证路径是否在指定的基础目录内，防止路径穿透攻击
func isSafePath(basePath, targetPath string) bool {
	// 获取基础目录的绝对路径
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return false
	}

	// 清理目标路径，移除 ../ 等
	cleanTarget := filepath.Clean(targetPath)

	// 拼接完整路径
	fullPath := filepath.Join(absBase, cleanTarget)

	// 获取绝对路径
	absTarget, err := filepath.Abs(fullPath)
	if err != nil {
		return false
	}

	// 检查目标路径是否以基础路径开头
	// 使用 filepath.Rel 更可靠地检查路径关系
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}

	// 如果相对路径以 .. 开头，说明目标在基础目录之外
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

// Static 注册静态资源和 SPA 路由处理
func Static(r *gin.RouterGroup, noRoute func(handlers ...gin.HandlerFunc)) {
	static(r, noRoute, false)
}

// StaticRestricted serves only the embedded default frontend. Restricted
// startup listeners must not let an installed theme override same-named JS,
// CSS, manifest, or favicon assets used by login and recovery pages.
func StaticRestricted(r *gin.RouterGroup, noRoute func(handlers ...gin.HandlerFunc)) {
	static(r, noRoute, true)
}

func static(r *gin.RouterGroup, noRoute func(handlers ...gin.HandlerFunc), forceDefaultTheme bool) {
	// 初始化嵌入式文件系统，指向 defaultTheme 根目录。
	defaultThemeFS, err := fs.Sub(PublicFS, "defaultTheme")
	if err != nil {
		panic("embedded default theme metadata is unavailable: " + err.Error())
	}

	getConfig := func() map[string]any {
		cfg, _ := config.GetMany(map[string]any{
			config.DescriptionKey: "A simple server monitor tool.",
			config.CustomHeadKey:  "",
			config.CustomBodyKey:  "",
			config.SitenameKey:    "Nekomari Monitor",
			config.ThemeKey:       DefaultTheme,
		})
		return cfg
	}

	// 核心逻辑：解析文件来源
	// filePath: 相对于主题根目录的路径 (例如 "theme.json" 或 "dist/assets/a.js")
	// 返回: 可 Seek 的资源 + 修改时间 + Content-Type，exists
	//
	// 解析顺序与原来的 getFileContent 完全一致：先本地主题目录（仅非 default
	// 主题），再嵌入的默认前端；安全校验（路径穿透、主题 ID 形状）保持不变。
	openAsset := func(themeID string, relativePath string) (assetSource, bool) {
		cleanPath := strings.TrimPrefix(relativePath, "/")

		cleanPath = filepath.Clean(cleanPath)

		if themeID != DefaultTheme {
			if strings.Contains(themeID, "..") || strings.Contains(themeID, "/") || strings.Contains(themeID, "\\") {
				return assetSource{}, false
			}

			themeBasePath := filepath.Join(DataDir, ThemesDir, themeID)

			if !isSafePath(themeBasePath, cleanPath) {
				return assetSource{}, false
			}

			localPath := filepath.Join(themeBasePath, cleanPath)
			// 检查文件是否存在且不是目录
			if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
				file, err := os.Open(localPath)
				if err == nil {
					return assetSource{
						reader:   file,
						modTime:  info.ModTime(),
						mimeType: assetMimeType(filepath.Ext(localPath)),
						size:     info.Size(),
						close:    func() { _ = file.Close() },
					}, true
				}
			}
			// 本地文件不存在，或读取失败 -> 继续向下回退
		}

		// 2. 尝试从嵌入式 defaultTheme/{cleanPath} 读取
		// fs.ReadFile 处理 embed 路径时使用 "/"
		embedPath := filepath.ToSlash(cleanPath)

		if strings.Contains(embedPath, "..") {
			return assetSource{}, false
		}

		if strings.HasPrefix(embedPath, DistDir+"/") {
			if content, ok := defaultDistFiles[strings.TrimPrefix(embedPath, DistDir+"/")]; ok {
				return embeddedAsset(embedPath, content), true
			}
		} else if content, err := fs.ReadFile(defaultThemeFS, embedPath); err == nil {
			return embeddedAsset(embedPath, content), true
		}

		// 3. 归档根部的文件，即主题根而不在 dist/ 下的那些（roadmap H7）。
		//
		// `dist.tar.zst` 里 dist/ 的内容在归档根上，所以 `defaultDistFiles` 的键不带前缀；而主题根的文件
		// （preview.png、komari-theme.json）在归档里同样是根级名字，却落不到上面那个分支 —— 它要求
		// DistDir 前缀。结果是 `/themes/<short>/preview.png` 一律 404，而主题清单自己声明的 preview 正是
		// 这个形式，于是面板的主题页永远是一张破图。
		//
		// 放在最后而不是最前：先查 dist 分支保持既有解析顺序不变，只有它没命中时才考虑根级文件。
		if content, ok := defaultDistFiles[embedPath]; ok {
			return embeddedAsset(embedPath, content), true
		}

		return assetSource{}, false
	}

	// themeHashedAssets 返回某个主题的构建产物集合，供 serveAsset 决定是否发长期缓存头。
	// 读不到清单（第三方主题可能没有 .vite/manifest.json）时返回 nil，等于退回原行为。
	themeHashedAssets := func(themeID string) map[string]struct{} {
		if cached, ok := hashedAssetCache.Load(themeID); ok {
			assets, _ := cached.(map[string]struct{})
			return assets
		}
		var assets map[string]struct{}
		manifestPath := path.Join(DistDir, ".vite", "manifest.json")
		if source, ok := openAsset(themeID, manifestPath); ok {
			if source.close != nil {
				defer source.close()
			}
			if data, err := io.ReadAll(source.reader); err == nil {
				assets = hashedAssetsFromManifest(data)
			}
		}
		hashedAssetCache.Store(themeID, assets)
		return assets
	}

	// adminHashedAssets 与上面同理，但读的是面板自带后台界面的清单（roadmap H7）。
	//
	// 不能复用 themeHashedAssets(DefaultTheme)：那个读的是主题自己的 dist/.vite/manifest.json，而
	// 后台的清单在 admin/.vite/manifest.json。用错清单的后果不是报错，而是后台的每个资源都退成长缓存
	// 缺失 —— 与之前在主题上修过的是同一类静默退化。
	adminHashedAssets := func() map[string]struct{} {
		const cacheKey = "admin"
		if cached, ok := hashedAssetCache.Load(cacheKey); ok {
			assets, _ := cached.(map[string]struct{})
			return assets
		}
		var assets map[string]struct{}
		manifestPath := path.Join(DistDir, AdminDistDir, ".vite", "manifest.json")
		if source, ok := openAsset(DefaultTheme, manifestPath); ok {
			if source.close != nil {
				defer source.close()
			}
			if data, err := io.ReadAll(source.reader); err == nil {
				assets = hashedAssetsFromManifest(data)
			}
		}
		hashedAssetCache.Store(cacheKey, assets)
		return assets
	}

	// adminAssetKey 返回后台资源在清单意义上的键。
	//
	// 清单的键是 Vite 的 `file` 字段，相对于后台构建输出的根（`assets/foo-<hash>.js`），而路由交上来的
	// 路径带着 admin/ 前缀（`admin/assets/foo-<hash>.js`）。两者不相等时 isHashedAsset 一律返回 false，
	// 资源照常服务但静默失去长期缓存 —— 与主题上那次是同一个失效方式，只是这次是键不对齐。
	adminAssetKey := func(relative string) string {
		trimmed := strings.TrimPrefix(filepath.ToSlash(relative), "/")
		trimmed = strings.TrimPrefix(trimmed, DistDir+"/")
		return strings.TrimPrefix(trimmed, AdminDistDir+"/")
	}

	// serveAdminAsset 与 serveAsset 相同，只是用后台自己的清单判定长期缓存。
	serveAdminAsset := func(c *gin.Context, source assetSource, relative string) {
		if source.close != nil {
			defer source.close()
		}
		header := c.Writer.Header()
		if source.mimeType == "" {
			header["Content-Type"] = nil
		} else {
			header.Set("Content-Type", source.mimeType)
		}
		header.Set("ETag", fmt.Sprintf("\"%x-%x\"", source.size, source.modTime.UnixNano()))
		if isHashedAsset(adminAssetKey(relative), adminHashedAssets()) {
			header.Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeContent(c.Writer, c.Request, path.Base(relative), source.modTime, source.reader)
	}

	// 核心逻辑：获取文件内容（供 index.html 改写与 favicon 等需要完整内容的分支使用）
	// 返回: content, contentType, exists
	readAsset := func(themeID string, relativePath string) ([]byte, string, bool) {
		source, exists := openAsset(themeID, relativePath)
		if !exists {
			return nil, "", false
		}
		if source.close != nil {
			defer source.close()
		}
		content, err := io.ReadAll(source.reader)
		if err != nil {
			return nil, "", false
		}
		return content, source.mimeType, true
	}

	getFileContent := func(themeID string, relativePath string) ([]byte, string, bool) {
		content, mimeType, exists := readAsset(themeID, relativePath)
		if exists || themeID == DefaultTheme {
			return content, mimeType, exists
		}
		// 本地主题文件存在但读不出来时，继续向下回退到嵌入的默认前端 —— 这是
		// 拆分 openAsset 之前 getFileContent 的既有行为。
		return readAsset(DefaultTheme, relativePath)
	}

	// 核心逻辑：渲染 Index.html
	serveIndex := func(c *gin.Context) {
		reqPath := c.Request.URL.Path
		cfg := getConfig()

		currentTheme := cfg[config.ThemeKey].(string)
		shouldReplace := true

		// 特殊页面：强制使用 default 主题，且不进行内容替换
		if forceDefaultTheme || isPanelOwned(reqPath) {
			currentTheme = DefaultTheme
			shouldReplace = false
		}

		// 获取 dist/index.html (相对于主题根目录)
		targetFile := path.Join(DistDir, IndexFile)
		content, _, exists := getFileContent(currentTheme, targetFile)

		if !exists {
			c.String(http.StatusNotFound, "Index file missing (checked %s/dist/index.html and default).", currentTheme)
			return
		}

		htmlStr := string(content)
		if forceDefaultTheme {
			htmlStr = stripServiceWorkerRegistration(htmlStr)
		}
		// 这里原先按 language cookie 改写 `<html lang>`，现在移除了。那个改写是多余的
		// —— 前端 (frontend/src/utils/language.ts) 启动时会把同一个值写到
		// documentElement.lang 上 —— 而它让这个响应变成「每人一份」：只要带上 cookie，
		// 下面那条短缓存就永远不可能命中，每次打开面板都要回源一趟（实测回源这一跳
		// 要 500 毫秒以上，而源站自己渲染只要 1 毫秒）。去掉之后，这个外壳只剩站点级
		// 输入：主题、站名、描述、自定义 head/body、favicon 版本。

		// favicon 的 URL 带上版本号，必须在下面那条「不替换」的早退之前执行 ——
		// /admin 与 /terminal 走的就是那条路径，而站点设置页恰好在那里预览图标。
		// 浏览器标签图标和 Cloudflare 都会长期缓存 /favicon.ico（Cloudflare 甚至
		// 把源站的 no-store 换成自己的 max-age），只靠响应头不足以保证换了图标
		// 就能看到。版本取自文件修改时间：图标没变 URL 不变、缓存照旧生效；换了
		// 图标 URL 立刻变化、绕过所有缓存层。
		htmlStr = withVersionedFavicon(htmlStr)

		// 如果不替换，保留系统内置页面内容。
		if !shouldReplace {
			writeShell(c, []byte(htmlStr))
			return
		}

		// 执行 HTML 内容替换
		replacer := strings.NewReplacer(
			"<title>Nekomari Monitor</title>", "<title>"+cfg[config.SitenameKey].(string)+"</title>",
			"A simple server monitor tool.", cfg[config.DescriptionKey].(string),
			"</head>", cfg[config.CustomHeadKey].(string)+"</head>",
			"</body>", cfg[config.CustomBodyKey].(string)+"</body>",
		)

		writeShell(c, []byte(replacer.Replace(htmlStr)))
	}

	// ================= 路由定义 =================
	// 1. Favicon 优先策略
	r.GET("/favicon.ico", func(c *gin.Context) {
		// favicon 是可替换的，而且浏览器/CDN 对它的缓存极其激进 —— 没有缓存头时
		// 会长期沿用旧图标，表现就是「上传成功但图标不变」。这里禁止缓存并每次都
		// 回源校验，代价只是几十 KB 以内的一个小文件。
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")

		// 优先：./data/favicon.ico
		localFavicon := filepath.Join(DataDir, FaviconFile)
		if !forceDefaultTheme {
			if data, err := os.ReadFile(localFavicon); err == nil {
				// 上传接口收的是任意图片，却统一存成 favicon.ico，所以不能按扩展名
				// 判断类型：一个 PNG 被标成 image/vnd.microsoft.icon 后浏览器会直接
				// 忽略它，表现就是「上传成功但图标不变」。按内容的魔数判断。
				c.Data(http.StatusOK, sniffFaviconType(data), data)
				return
			}
		}

		// 其次：当前主题的 dist/favicon.ico 或 theme_root/favicon.ico ?
		// 通常构建后的资源在 dist 中，这里假设优先找 dist 内的，如果你的 favicon 在根目录，去掉 DistDir 拼接即可
		cfg := getConfig()
		themeFaviconPath := path.Join(DistDir, FaviconFile)
		currentTheme := cfg[config.ThemeKey].(string)
		if forceDefaultTheme {
			currentTheme = DefaultTheme
		}
		content, mimeType, exists := getFileContent(currentTheme, themeFaviconPath)
		if exists {
			c.Data(http.StatusOK, mimeType, content)
			return
		}

		c.Status(http.StatusNotFound)
	})

	// 2. 静态资源路由 /themes/:id/*path
	// 允许访问 /themes/MyTheme/theme.json 和 /themes/MyTheme/dist/assets/a.js
	r.GET("/themes/:id/*path", func(c *gin.Context) {
		themeID := c.Param("id")
		if forceDefaultTheme && themeID != DefaultTheme {
			c.Status(http.StatusNotFound)
			return
		}
		if forceDefaultTheme {
			themeID = DefaultTheme
		}
		// c.Param("path") 包含了开头的 /，openAsset 会处理
		filePath := c.Param("path")

		source, exists := openAsset(themeID, filePath)
		if exists {
			serveAsset(c, source, filePath, themeHashedAssets(themeID))
			return
		}
		c.Status(http.StatusNotFound)
	})

	// 2b. 面板自带的后台界面（roadmap H7）。
	//
	// 为什么需要这条路由：后台界面原先由「内置默认主题」提供 —— 下面的 SPA 分支对 /admin 与 /terminal
	// 强制使用内置主题 —— 所以内置主题换了之后后台就跟着换了。主题决定**前台**长什么样是合理的，
	// 但「这台面板能不能管理」不该取决于装了哪个主题。
	//
	// 现在它有自己的构建（frontend/vite.admin.config.ts，base 为 /admin/），并作为内置归档里
	// admin/ 子树分发。这里把 /admin 下的资源请求指到那棵子树，于是：
	//   - 浏览器的路径就是 /admin/...，与面板自己的路由和 API 绝对路径一致，不需要 basename 适配；
	//   - 请求 /admin 时取 admin/admin.html 而不是主题的 index.html，所以主题无法再接管这个前缀；
	//   - 面板的 /api/admin/* 是已注册路由，优先于 noRoute 的 SPA 分支，不受影响。
	// Gin's wildcard needs at least one character to match, so `/admin` itself is a separate route rather
	// than the empty case of `/admin/*path`. Without this the bare path fell through to the SPA branch,
	// which handed it the theme's document — the exact behaviour this route exists to replace.
	serveAdminDocument := func(c *gin.Context) {
		source, exists := openAsset(DefaultTheme, path.Join(DistDir, AdminDistDir, IndexFile))
		if !exists {
			c.Status(http.StatusNotFound)
			return
		}
		serveAdminAsset(c, source, path.Join(AdminDistDir, IndexFile))
	}
	r.GET("/admin", serveAdminDocument)
	// The workbench belongs to the panel, not the selected public theme.
	r.GET("/terminal", serveAdminDocument)
	r.GET("/terminal/", serveAdminDocument)

	// servePanelPath answers a path under a panel-owned prefix the way `/admin/*path` does: the
	// embedded admin subtree first, the panel document for extensionless client-side routes, 404
	// otherwise. `/install` and `/database-recovery` are routes of the panel's own app
	// (frontend/src/routes.ts) and were previously answered by noRoute with the *installed theme's*
	// document — which has no such route and renders its own 404. Production measured exactly that:
	// `curl /install` returned the SAO theme page while `/admin` returned the panel.
	servePanelPath := func(c *gin.Context) {
		filePath := c.Param("path")
		// /admin/ → admin/index.html, anything else by its own path.
		relative := path.Join(DistDir, AdminDistDir, strings.TrimPrefix(filePath, "/"))

		// Only the embedded archive is consulted, never an installed theme: the admin is the panel's own
		// asset, and a theme should not be able to replace it.
		source, exists := openAsset(DefaultTheme, relative)
		if exists {
			serveAdminAsset(c, source, relative)
			return
		}
		// A path with no extension is a client-side route (`/admin/servers` and the rest), so it gets the
		// document and the app resolves it.
		//
		// This has to come *after* the asset lookup: otherwise `/admin/assets/admin-<hash>.js` would be
		// answered with HTML, and the symptom would be a blank page and a MIME-type error in the console
		// rather than a 404 anyone can act on.
		if path.Ext(filePath) == "" {
			serveAdminDocument(c)
			return
		}
		c.Status(http.StatusNotFound)
	}
	r.GET("/admin/*path", servePanelPath)

	// 3. SPA 路由 (noRoute)
	noRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Status(http.StatusNotFound)
			return
		}
		//
		func() {
			tempKey := c.Query("temp_key")
			if tempKey == "" {
				return
			}

			tempKeyExpireTime, err := config.GetAs[int64]("tempory_share_token_expire_at", 0)
			if err != nil {
				return
			}
			allowTempKey, err := config.GetAs[string]("tempory_share_token", "")
			if err != nil {
				return
			}

			if allowTempKey == "" || tempKey != allowTempKey {
				return
			}
			now := time.Now().Unix()
			if tempKeyExpireTime < now {
				return
			}
			expireSeconds := int(tempKeyExpireTime - now)
			if expireSeconds > 0 {
				c.SetCookie(
					"temp_key",    // key
					tempKey,       // value
					expireSeconds, // maxAge（秒）
					"/",           // path
					"",            // domain
					false,         // secure
					false,         // httpOnly
				)
			}
		}()
		reqPath := c.Request.URL.Path
		cfg := getConfig()
		currentTheme := cfg[config.ThemeKey].(string)
		if forceDefaultTheme {
			currentTheme = DefaultTheme
		}

		// SPA 静态资源回退
		distPath := path.Join(DistDir, reqPath)

		source, exists := openAsset(currentTheme, distPath)
		if exists {
			serveAsset(c, source, distPath, themeHashedAssets(currentTheme))
			return
		}

		// 资源不存在：如果这个请求要的是一个**文件**（扩展名本身就能证明，见
		// assetOnlyExtensions 的注释），就以 404 结束，绝不让下面的 SPA 分支用
		// index.html 回答它 —— 那会把 text/html 当 JS/CSS/图片交出去，浏览器报的是
		// MIME 错误而不是「这个文件不存在」。
		//
		// 本项目自己的 PWA 文件也走这条判断：/sw.js、/registerSW.js、
		// /workbox-<hash>.js、/manifest.webmanifest、/manifest.json 都在表内，所以
		// 归档里少了任何一个都是 404，而不是「一个带着 200 的 HTML 外壳」——后者会让
		// Service Worker 的注册以「不支持的 MIME 类型」失败，而页面照常渲染。
		//
		// 前端路由（/dashboard、/instance/<uuid>、/plugin/<short>/...）、.html 文档与
		// 未知后缀仍然回外壳，理由同注释。
		if isAssetRequest(reqPath) {
			c.Status(http.StatusNotFound)
			return
		}

		// 路由 (如 /dashboard, /settings) -> 返回 index.html
		serveIndex(c)
	})
}

// sniffFaviconType 按内容判断 favicon 的真实类型。
//
// 上传接口接受任意图片（前端 input 的 accept 是 image/*），但一律写成
// favicon.ico。若按扩展名返回 image/vnd.microsoft.icon，浏览器会把 PNG 数据
// 当成损坏的图标丢弃，用户看到的就是「上传成功但图标没变」。这里按魔数判断，
// 让内容与 Content-Type 一致。
func sniffFaviconType(data []byte) string {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 6 && string(data[:6]) == "GIF87a":
		return "image/gif"
	case len(data) >= 6 && string(data[:6]) == "GIF89a":
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) >= 4 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x01 && data[3] == 0x00:
		return "image/vnd.microsoft.icon"
	default:
		// SVG 是文本，先跳过空白再找 "<svg" / "<?xml"
		head := strings.TrimSpace(string(data[:min(len(data), 256)]))
		if strings.HasPrefix(head, "<svg") || strings.HasPrefix(head, "<?xml") {
			return "image/svg+xml"
		}
		return "application/octet-stream"
	}
}

// faviconVersion 返回当前 favicon 的版本串。
//
// 用文件修改时间而不是内容哈希：图标文件很小，读一次做哈希也便宜，但修改时间
// 已经足够表达「这个图标换过了」，而且不需要每次请求都读文件内容。文件不存在
// （尚未自定义）时返回空串，此时保持原样、不做任何改写。
func faviconVersion() string {
	info, err := os.Stat(filepath.Join(DataDir, FaviconFile))
	if err != nil {
		return ""
	}
	return strconv.FormatInt(info.ModTime().Unix(), 36)
}

// withVersionedFavicon 给 index.html 里的 favicon 引用加上 ?v=<mtime>。
//
// 为什么必须改 URL 而不能只靠响应头：浏览器标签图标与 Cloudflare 都会长期缓存
// /favicon.ico，而 Cloudflare 会用自己配置的 max-age 覆盖源站的 Cache-Control
// （实测源站发 no-store，边缘仍回 max-age=14400）。URL 一变，所有缓存层都失效。
func withVersionedFavicon(html string) string {
	version := faviconVersion()
	if version == "" {
		return html
	}
	suffix := FaviconFile + "?v=" + version
	for _, original := range []string{
		`href="favicon.ico"`,
		`href="/favicon.ico"`,
	} {
		html = strings.ReplaceAll(html, original, `href="/`+suffix+`"`)
	}
	return html
}
