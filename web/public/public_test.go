package public

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/Aone2233/nekomari/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestShellIsCacheableAndCookieIndependent 固定这条缓存改动的两个要点。
//
// 一、外壳响应要带 ETag 与一条短缓存。源站渲染只要 1 毫秒，贵的是 Cloudflare 回源
// 那一跳（不带缓存头的 HTML 会被当成 DYNAMIC，实测每次打开 500 毫秒以上）。
//
// 二、带不带 language cookie 拿到的字节必须完全一样。这不是形式主义：只要响应随
// cookie 变化，CDN 就会把其中一种语言的 HTML 发给所有人 —— 而 Vary: Cookie 在
// Cloudflare 的默认缓存里并不参与缓存键。原来的实现按 cookie 改写 `<html lang>`，
// 正是这条断言的反面；语言现在由前端 (utils/language.ts) 在启动时写到
// documentElement.lang 上。
func TestShellIsCacheableAndCookieIndependent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)

	router := gin.New()
	StaticRestricted(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})

	fetch := func(language string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", "/", nil)
		if language != "" {
			request.AddCookie(&http.Cookie{Name: LanguageCookieName, Value: language})
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	plain := fetch("")
	if plain.Code != 200 {
		t.Fatalf("shell status = %d, want 200", plain.Code)
	}
	if got := plain.Header().Get("Cache-Control"); got != shellCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, shellCacheControl)
	}
	etag := plain.Header().Get("ETag")
	if etag == "" {
		t.Fatal("shell has no ETag, so a revalidation cannot be answered with 304")
	}
	if plain.Body.Len() == 0 {
		t.Fatal("shell body is empty")
	}

	for _, language := range []string{"zh-CN", "ja-JP", "en-US"} {
		withCookie := fetch(language)
		if withCookie.Body.String() != plain.Body.String() {
			t.Fatalf("shell differs when language=%s is set: it would be cached once and served to everyone", language)
		}
		if got := withCookie.Header().Get("ETag"); got != etag {
			t.Fatalf("language=%s changed the ETag (%q vs %q): the cache key is not just the URL", language, got, etag)
		}
	}

	conditional := httptest.NewRequest("GET", "/", nil)
	conditional.Header.Set("If-None-Match", etag)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, conditional)
	if recorder.Code != http.StatusNotModified {
		t.Fatalf("conditional request status = %d, want 304", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("304 carried %d bytes of body, want 0", recorder.Body.Len())
	}
}

func TestEmbeddedDistDoesNotEmbedRawFiles(t *testing.T) {
	if _, err := PublicFS.ReadFile("defaultTheme/dist/index.html"); err == nil {
		t.Fatal("PublicFS still embeds the raw frontend files")
	}
	if content, ok := defaultDistFiles[IndexFile]; !ok || len(content) == 0 {
		t.Fatalf("embedded dist does not contain a non-empty %q", IndexFile)
	}
}

func TestStaticRestrictedDoesNotServeCustomAssetOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())
	assetPath := filepath.Join("data", "theme", "custom", "dist", "assets")
	if err := os.MkdirAll(assetPath, 0o755); err != nil {
		t.Fatalf("create custom theme asset directory: %v", err)
	}
	const assetName = "about-D4JKo971.css"
	if err := os.WriteFile(filepath.Join(assetPath, assetName), []byte("custom override"), 0o644); err != nil {
		t.Fatalf("write custom theme asset: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ThemeKey, "custom"); err != nil {
		t.Fatalf("set custom theme: %v", err)
	}

	router := gin.New()
	StaticRestricted(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})
	for _, requestPath := range []string{"/assets/" + assetName} {
		request := httptest.NewRequest("GET", requestPath, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("restricted asset %s status = %d, want 200", requestPath, recorder.Code)
		}
		body, err := io.ReadAll(recorder.Result().Body)
		if err != nil {
			t.Fatalf("read restricted asset %s: %v", requestPath, err)
		}
		if string(body) == "custom override" {
			t.Fatalf("restricted listener served a custom theme asset override for %s", requestPath)
		}
	}

	indexRequest := httptest.NewRequest("GET", "/database-recovery", nil)
	indexRecorder := httptest.NewRecorder()
	router.ServeHTTP(indexRecorder, indexRequest)
	indexBody, err := io.ReadAll(indexRecorder.Result().Body)
	if err != nil {
		t.Fatalf("read restricted index: %v", err)
	}
	if strings.Contains(string(indexBody), `vite-plugin-pwa:register-sw`) {
		t.Fatal("restricted index still registers a service worker")
	}
}

// TestStaticAssetConditionalAndRangeRequests 钉住主题/SPA 资源的协商缓存与 Range。
//
// 背景：这些资源此前走 os.ReadFile + c.Data，既整份读进内存，也没有 ETag /
// Last-Modified / Accept-Ranges，每次请求都要回源全量传输（0.5MB 的背景图也是）。
// 改成 http.ServeContent 后必须真的发出校验值、真的对条件请求回 304、真的支持
// Range —— 否则浏览器与 Cloudflare 仍然只能每次都拉整份。
func TestStaticAssetConditionalAndRangeRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())

	const assetName = "background-D4JKo971.png"
	assetPath := filepath.Join("data", "theme", "custom", "dist", "assets")
	if err := os.MkdirAll(assetPath, 0o755); err != nil {
		t.Fatalf("create custom theme asset directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetPath, assetName), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("write custom theme asset: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ThemeKey, "custom"); err != nil {
		t.Fatalf("set custom theme: %v", err)
	}

	router := gin.New()
	Static(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})

	// SPA 回退路径：/assets/* 是构建产物实际使用的地址。
	requestPath := "/assets/" + assetName
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest("GET", requestPath, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("asset %s status = %d, want 200", requestPath, first.Code)
	}
	if body := first.Body.String(); body != "0123456789" {
		t.Fatalf("asset body = %q, want the file content", body)
	}
	validator := first.Header().Get("ETag")
	if validator == "" {
		t.Fatalf("asset %s has no ETag; conditional requests cannot be answered", requestPath)
	}
	if got := first.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("asset %s Accept-Ranges = %q, want bytes", requestPath, got)
	}

	conditional := httptest.NewRequest("GET", requestPath, nil)
	conditional.Header.Set("If-None-Match", validator)
	conditionalRecorder := httptest.NewRecorder()
	router.ServeHTTP(conditionalRecorder, conditional)
	if conditionalRecorder.Code != http.StatusNotModified {
		t.Fatalf("conditional asset request status = %d, want 304", conditionalRecorder.Code)
	}
	if body := conditionalRecorder.Body.String(); body != "" {
		t.Fatalf("304 response carried a body: %q", body)
	}

	ranged := httptest.NewRequest("GET", requestPath, nil)
	ranged.Header.Set("Range", "bytes=0-3")
	rangeRecorder := httptest.NewRecorder()
	router.ServeHTTP(rangeRecorder, ranged)
	if rangeRecorder.Code != http.StatusPartialContent {
		t.Fatalf("ranged asset request status = %d, want 206", rangeRecorder.Code)
	}
	if body := rangeRecorder.Body.String(); body != "0123" {
		t.Fatalf("ranged asset body = %q, want %q", body, "0123")
	}

	// 嵌入的默认前端走的是另一条分支（没有真实文件时间），同样要能协商缓存。
	t.Run("embedded default theme asset", func(t *testing.T) {
		embeddedPath := "/themes/default/dist/index.html"
		first := httptest.NewRecorder()
		router.ServeHTTP(first, httptest.NewRequest("GET", embeddedPath, nil))
		if first.Code != http.StatusOK {
			t.Fatalf("embedded asset status = %d, want 200", first.Code)
		}
		validator := first.Header().Get("ETag")
		if validator == "" {
			t.Fatal("embedded asset has no ETag; conditional requests cannot be answered")
		}
		conditional := httptest.NewRequest("GET", embeddedPath, nil)
		conditional.Header.Set("If-None-Match", validator)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, conditional)
		if recorder.Code != http.StatusNotModified {
			t.Fatalf("conditional embedded asset status = %d, want 304", recorder.Code)
		}
	})
}
