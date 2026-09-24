package public

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
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
		File string `json:"file"`
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
	// 其余资源（index.html、sw.js、以及 public/ 下那些没有哈希的图标）保持原样：
	// index.html 必须能立刻更新，sw.js 缓存久了会拖住整个应用的下一次部署。
	if isHashedAsset(name, hashedAssets) {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(c.Writer, c.Request, name, source.modTime, source.reader)
}

// embeddedAsset 把嵌入内容包装成 assetSource。
func embeddedAsset(embedPath string, content []byte) assetSource {
	return assetSource{
		reader:   bytes.NewReader(content),
		modTime:  embeddedAssetModTime,
		mimeType: mime.TypeByExtension(filepath.Ext(embedPath)),
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

func normalizeHTMLLanguage(language string) string {
	language = strings.TrimSpace(strings.ReplaceAll(language, "_", "-"))
	if len(language) < 2 || len(language) > 32 {
		return ""
	}

	for _, r := range language {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return ""
	}

	return language
}

func replaceHTMLLanguage(htmlStr, language string) string {
	language = normalizeHTMLLanguage(language)
	if language == "" {
		return htmlStr
	}

	replacements := []struct {
		old string
		new string
	}{
		{`<html lang="en">`, `<html lang="` + language + `">`},
		{`<html lang='en'>`, `<html lang='` + language + `'>`},
		{`<html>`, `<html lang="` + language + `">`},
	}

	for _, replacement := range replacements {
		if strings.Contains(htmlStr, replacement.old) {
			return strings.Replace(htmlStr, replacement.old, replacement.new, 1)
		}
	}

	return htmlStr
}

func stripServiceWorkerRegistration(html string) string {
	return strings.ReplaceAll(html, `<script id="vite-plugin-pwa:register-sw" src="/registerSW.js"></script>`, "")
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
						mimeType: mime.TypeByExtension(filepath.Ext(localPath)),
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
		if forceDefaultTheme || strings.HasPrefix(reqPath, "/admin") || strings.HasPrefix(reqPath, "/terminal") {
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
		if language, err := c.Cookie(LanguageCookieName); err == nil {
			htmlStr = replaceHTMLLanguage(htmlStr, language)
		}

		// favicon 的 URL 带上版本号，必须在下面那条「不替换」的早退之前执行 ——
		// /admin 与 /terminal 走的就是那条路径，而站点设置页恰好在那里预览图标。
		// 浏览器标签图标和 Cloudflare 都会长期缓存 /favicon.ico（Cloudflare 甚至
		// 把源站的 no-store 换成自己的 max-age），只靠响应头不足以保证换了图标
		// 就能看到。版本取自文件修改时间：图标没变 URL 不变、缓存照旧生效；换了
		// 图标 URL 立刻变化、绕过所有缓存层。
		htmlStr = withVersionedFavicon(htmlStr)

		// 如果不替换，保留系统内置页面内容，仅同步 html lang。
		if !shouldReplace {
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(htmlStr))
			return
		}

		// 执行 HTML 内容替换
		replacer := strings.NewReplacer(
			"<title>Nekomari Monitor</title>", "<title>"+cfg[config.SitenameKey].(string)+"</title>",
			"A simple server monitor tool.", cfg[config.DescriptionKey].(string),
			"</head>", cfg[config.CustomHeadKey].(string)+"</head>",
			"</body>", cfg[config.CustomBodyKey].(string)+"</body>",
		)

		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(replacer.Replace(htmlStr)))
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

		// 如果资源不存在，且路径包含扩展名 (如 .js, .css, .png)，则返回 404
		// 避免将 index.html 作为 js 文件返回导致 "Failed to fetch dynamically imported module"
		//ext := filepath.Ext(reqPath)
		//if ext != "" && ext != ".html" {
		//	c.Status(http.StatusNotFound)
		//	return
		//}

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
