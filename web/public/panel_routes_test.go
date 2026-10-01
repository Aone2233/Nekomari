package public

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/internal/config"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// F5/P2-1：面板自有路由被已安装主题接管。
//
// `/install` 与 `/database-recovery` 是面板自己 App 里的两条路由（frontend/src/routes.ts），
// 但服务端没有为它们注册路由，于是落到 noRoute → serveIndex，返回的是**已安装主题**的
// index.html —— 主题的路由表里没有这两条，页面渲染成它自己的 404。线上实测正是如此：
// `curl /install` 返回 SAO 主题页，而 `/admin` 返回面板页。
//
// 这条测试就是当年漏掉的那一步（同样的漏法在 v0.1.31 的 SLA 页上出现过）：装一个文档可以被
// 识别的主题，再断言这两个 URL 返回的是面板自己的构建 —— `/admin/assets/` 前缀与面板标题 ——
// 而不是主题的标题。
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
		if !strings.Contains(body, `src="/admin/assets/`) {
			t.Errorf("GET %s does not load the panel's own bundle from /admin/assets/:\n%s",
				requestPath, body[:min(300, len(body))])
		}
		if !strings.Contains(body, "<title>Nekomari</title>") {
			t.Errorf("GET %s is not the panel document (no Nekomari title):\n%s",
				requestPath, body[:min(300, len(body))])
		}
	}
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
