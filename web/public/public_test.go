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

func TestNormalizeHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"hyphen language": {
			input: "zh-CN",
			want:  "zh-CN",
		},
		"underscore language": {
			input: "zh_CN",
			want:  "zh-CN",
		},
		"reject script injection": {
			input: `zh-CN" autofocus`,
		},
		"reject too short": {
			input: "z",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeHTMLLanguage(tt.input); got != tt.want {
				t.Fatalf("normalizeHTMLLanguage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplaceHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		html     string
		language string
		want     string
	}{
		"replace existing lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: "zh-CN",
			want:     `<html lang="zh-CN"><head></head></html>`,
		},
		"insert missing lang": {
			html:     `<html><head></head></html>`,
			language: "ja_JP",
			want:     `<html lang="ja-JP"><head></head></html>`,
		},
		"ignore invalid lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: `zh-CN" autofocus`,
			want:     `<html lang="en"><head></head></html>`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := replaceHTMLLanguage(tt.html, tt.language); got != tt.want {
				t.Fatalf("replaceHTMLLanguage() = %q, want %q", got, tt.want)
			}
		})
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
