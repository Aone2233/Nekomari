package server

import (
	"path/filepath"
	"testing"

	"github.com/Aone2233/nekomari/web/public"
)

// Bootstrap 的接线：只有当当前主题**就是**内嵌的默认主题时才预热它的磁盘缓存。
//
// 为什么不直接调用 Bootstrap：它一开始就做 ./data 的恢复与 dbcore.Initialize（打开面板
// 自己的数据库，必要时还会清空 ./data），那样这条用例就变成在测数据库了。这里测的正是
// 那个决定以及它发出的那一次调用 —— prewarmDefaultThemeCacheIfBuiltIn。
//
// 缓存目录用 glob 找（data/.theme-cache/*/.complete），不在这里重算归档哈希：哈希是
// web/public 的事，重算一遍的测试会在两边真的不一致时照样通过。
func TestOnlyTheBuiltInThemePrewarmsItsDiskCache(t *testing.T) {
	t.Run("the built-in theme is prewarmed", func(t *testing.T) {
		// public.PrewarmDefaultThemeCache 会写 ./data；把它留在临时目录里。
		t.Chdir(t.TempDir())

		done := prewarmDefaultThemeCacheIfBuiltIn(public.DefaultTheme)
		if done == nil {
			t.Fatal("built-in theme did not start cache warm-up")
		}
		// Join the writer before t.Chdir restores the working directory, even
		// on a slow runner. The job timeout still bounds a stuck writer.
		<-done
		assertThemeCacheMarker(t)
	})

	t.Run("an installed theme is left alone", func(t *testing.T) {
		t.Chdir(t.TempDir())

		if done := prewarmDefaultThemeCacheIfBuiltIn("sao"); done != nil {
			t.Fatal("installed theme unexpectedly started cache warm-up")
		}

		matches, err := filepath.Glob(filepath.Join("data", ".theme-cache", "*", ".complete"))
		if err != nil {
			t.Fatalf("glob the cache root: %v", err)
		}
		if len(matches) > 0 {
			t.Fatalf("an installed theme prewarmed the embedded theme's cache (%v), which that theme never reads", matches)
		}
	})
}

// The completion channel can also close after a failed warm-up; require the
// marker itself so this test still verifies materialization succeeded.
func assertThemeCacheMarker(t *testing.T) {
	t.Helper()
	pattern := filepath.Join("data", ".theme-cache", "*", ".complete")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %q: %v", pattern, err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one completed disk cache for %q, got %v", pattern, matches)
	}
}
