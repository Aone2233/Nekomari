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
// 文档；而**不是**在断言它会给面板文档。
//
// 这条测试就是当年漏掉的那一步（同样的漏法在 v0.1.31 的 SLA 页上出现过）：装一个文档可以被
// 识别的主题，再断言这两个 URL 返回的是**内置默认前端**——而不是主题的标题、也不是 admin 包。
// 早期版本的修复曾把它们注册成显式路由并返回 admin 文档，那是错的：`/install` 属于前台 App，
// 不是 admin 包；而且 `/database-recovery` 的重复注册会让服务器直接 panic。
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
	// `defaultTheme/dist.tar.zst` 取，磁盘上的同名目录不会被采用（实测确认）。而内嵌归档是个
	// 构建产物 —— 仓库里提交的那份与 `frontend/dist` 并不一致（CI 在 `go build` 前会用
	// build.sh 重新打包，本地直接 `go test` 用的却是提交的那份），所以**不能**钉它的标题或内容：
	// 那会让这条测试取决于"提交的是哪次构建"，而这类耦合正是这个缺陷当年能活下来的原因之一。
	//
	// 因此断言用排除法：面板自有路径的文档只可能来自三处 —— 已安装主题、内置默认主题、admin 包。
	// 排除前两者中的"已安装主题"与"admin 包"，剩下的只能是内置默认主题。这两个排除项各自都有牙：
	// 前者是原始缺陷（主题接管），后者是早期那版修复的错误目标（接到 admin 文档）。
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
		// 排除法（理由见上方注释）：不是已安装主题，也不是 admin 包。
		if strings.Contains(body, `"/admin/assets/`) {
			t.Errorf("GET %s served the admin bundle; /install and /database-recovery are front-end routes:\n%s",
				requestPath, body[:min(300, len(body))])
		}
		// 正面的最低要求：它得是个像样的 SPA 文档，而不是空白或错误页。
		if !strings.Contains(body, "<html") || !strings.Contains(body, `id="root"`) {
			t.Errorf("GET %s is not an SPA document:\n%s", requestPath, body[:min(300, len(body))])
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
