package public

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// F5/P2-1：面板自有的前台路由被已安装主题接管。
//
// `/install` 是面板自己前台 App 的一条路由（frontend/src/routes.ts:41），但服务端没有为它注册路由，
// 于是落到 noRoute → serveIndex，返回的是**已安装主题**的 index.html —— 主题的路由表里没有这条，
// 页面渲染成它自己的 404。线上实测正是如此：`curl /install` 返回 SAO 主题页，而 `/admin` 返回面板页。
//
// 修法是 `panelOwnedPrefixes` + `serveIndex` 里那条既有的 `currentTheme = DefaultTheme`：
// panel-owned 路径固定用**内置**前端，不替换主题变量，已安装主题因此碰不到这些路径。
//
// 注意这条测试测的是 public 路由**自身**的行为，不覆盖 `internal/server/runtime.go` 那一层：
// 在真实应用里 `/database-recovery` 是已注册路由（正常模式 307 跳回 `/`，因为恢复界面属于它的
// 临时受限监听器），根本到不了 serveIndex。这里仍然带上它，是因为万一它落进来，也绝不能是主题的
// 文档。
//
// 断言是**正面**的（理由见 embedded_theme_test.go 的 panelDocumentProblems）：panel-owned 路径
// 拿到的那份文档必须就是内嵌归档的根文档本身 —— 面板自己的标题、面板自己的 entry 命名
// （`/assets/entry-*`，见 frontend/vite.config.ts 的 entryFileNames）、以及只有面板前台才有的路由。
//
// 这正是当年漏掉的那条牙：旧版本用排除法（"不是已安装主题的文档"），而**没装主题**时内置主题的
// 文档与面板自己的文档在排除法下无法区分 —— 2026-10-01 之前内置主题**就是**那份文档，所以断言在
// 缺陷上线时照样通过。归档现在是面板自己的构建（script/embed-theme.mjs 从 frontend/dist 装配，
// CI 在 go build 前重新装配），所以正面判据既成立、也是唯一能区分两者的判据。
func TestPanelOwnedRoutesAreNotTakenOverByAnInstalledTheme(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())

	// A theme whose document is unmistakable, installed the way a real one is:
	// data/theme/<short>/dist/index.html.
	const themeMarker = "SAO-THEME-DOCUMENT-MARKER"
	themeDist := filepath.Join("data", "theme", "sao", "dist")
	if err := os.MkdirAll(themeDist, 0o755); err != nil {
		t.Fatalf("create theme directory: %v", err)
	}
	themeDocument := `<!doctype html><html><head><title>SAO</title></head>` +
		`<body>` + themeMarker + `<div id="root"></div></body></html>`
	if err := os.WriteFile(filepath.Join(themeDist, IndexFile), []byte(themeDocument), 0o644); err != nil {
		t.Fatalf("write theme document: %v", err)
	}

	// 再装一份**名为 `default` 的磁盘主题**没有意义：`DefaultTheme` 只从内嵌归档
	// `defaultTheme/dist.tar.zst` 取，磁盘上的同名目录不会被采用（实测确认）。
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ThemeKey, "sao"); err != nil {
		t.Fatalf("set installed theme: %v", err)
	}

	router := gin.New()
	Static(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})

	// The theme has to actually be the one serving `/`, or this test would pass for the
	// wrong reason — e.g. because the theme directory was never consulted at all.
	themed := get(t, router, "/")
	if !strings.Contains(themed.Body.String(), themeMarker) {
		t.Fatalf("the installed theme is not served at /, so this test would prove nothing:\n%s",
			themed.Body.String())
	}

	// 内嵌归档的根文档：这就是 panel-owned 路径必须拿到的那一份。先确认归档自己是对的，
	// 否则下面的比较会把"归档被换成了主题构建"误报成"路由接错了"。
	files, err := loadEmbeddedDist()
	if err != nil {
		t.Fatalf("the embedded archive does not load: %v", err)
	}
	rootDocument := string(files[IndexFile])
	if problems := panelDocumentProblems(files, rootDocument); len(problems) > 0 {
		t.Fatalf("the embedded archive's own %s is not the panel's front end, so this test cannot mean anything: %v",
			IndexFile, problems)
	}

	for _, requestPath := range []string{"/install", "/install/", "/database-recovery", "/database-recovery/"} {
		recorder := get(t, router, requestPath)
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", requestPath, recorder.Code)
			continue
		}
		body := recorder.Body.String()
		if strings.Contains(body, themeMarker) {
			t.Errorf("GET %s was answered with the installed theme's document:\n%s",
				requestPath, body[:min(300, len(body))])
			continue
		}
		// 正面判据：这份文档必须是面板自己的前台构建。
		assertDocumentIsThePanelsOwnFrontEnd(t, files, "GET "+requestPath, body)
		// 而且就是内嵌归档的根文档本身，不是另一份"看起来像面板"的文档。
		// withoutFaviconVersion 只是去掉 serveIndex 在有自定义图标时给 /favicon.ico 加的
		// `?v=<mtime>`；测试工作目录里没有 data/favicon.ico，这一步实际是恒等的。
		if got, want := withoutFaviconVersion(body), withoutFaviconVersion(rootDocument); got != want {
			t.Errorf("GET %s served a different document than the embedded archive's %s", requestPath, IndexFile)
		}
		// 早期那版修复把它们接到 admin 包上，那是错的：/install 属于前台 App，不是后台。
		if strings.Contains(body, `"/admin/assets/`) {
			t.Errorf("GET %s served the admin bundle; /install and /database-recovery are front-end routes:\n%s",
				requestPath, body[:min(300, len(body))])
		}
		if !strings.Contains(body, "<html") || !strings.Contains(body, `id="root"`) {
			t.Errorf("GET %s is not an SPA document:\n%s", requestPath, body[:min(300, len(body))])
		}
	}
}

// faviconVersionSuffix matches the `?v=<mtime>` serveIndex appends to /favicon.ico when a custom icon
// exists in the data directory (see withVersionedFavicon in public.go).
var faviconVersionSuffix = regexp.MustCompile(`(/favicon\.ico)\?v=[^"]*`)

// withoutFaviconVersion drops that query string so two copies of the same document compare equal.
func withoutFaviconVersion(html string) string {
	return faviconVersionSuffix.ReplaceAllString(html, "$1")
}

// The prefix rule itself, including the two boundaries that are easy to get wrong:
// `/administrator` is not under `/admin`, and `/plugin/...` is deliberately *not* panel-owned
// yet — plugin pages and plugin assets share that prefix and need their own decision, so the
// fix for F5 leaves it alone rather than half-converting it.
func TestIsPanelOwnedPrefixBoundaries(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/admin", true},
		{"/admin/", true},
		{"/admin/servers", true},
		{"/terminal", true},
		{"/terminal/", true},
		{"/install", true},
		{"/install/", true},
		{"/database-recovery", true},
		{"/database-recovery/", true},
		{"/administrator", false},
		{"/installed", false},
		{"/database-recovery-old", false},
		{"/plugin/demo", false},
		{"/", false},
		{"/dashboard", false},
	}
	for _, tc := range cases {
		if got := isPanelOwned(tc.path); got != tc.want {
			t.Errorf("isPanelOwned(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
