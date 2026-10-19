package server

import (
	"path/filepath"
	"testing"
	"time"

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

		prewarmDefaultThemeCacheIfBuiltIn(public.DefaultTheme)
		waitForThemeCacheMarker(t, 5*time.Second)
	})

	t.Run("an installed theme is left alone", func(t *testing.T) {
		t.Chdir(t.TempDir())

		prewarmDefaultThemeCacheIfBuiltIn("sao")
		// 给一个「本来会预热」的 goroutine 足够的时间出现。
		time.Sleep(100 * time.Millisecond)

		matches, err := filepath.Glob(filepath.Join("data", ".theme-cache", "*", ".complete"))
		if err != nil {
			t.Fatalf("glob the cache root: %v", err)
		}
		if len(matches) > 0 {
			t.Fatalf("an installed theme prewarmed the embedded theme's cache (%v), which that theme never reads", matches)
		}
	})
}

// waitForThemeCacheMarker 轮询等待缓存出现 .complete。
//
// 判据是标记文件而不是目录本身：目录在解压中途就已经存在，标记只在全部条目写完、字节数
// 核对通过、rename 之后才出现 —— 那才是「可以用了」的那一刻。预热是异步的，所以这里只能
// 轮询等待，而 5 秒的上限远大于实测的 300-370ms。
func waitForThemeCacheMarker(t *testing.T, timeout time.Duration) {
	t.Helper()
	pattern := filepath.Join("data", ".theme-cache", "*", ".complete")
	deadline := time.Now().Add(timeout)
	for {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %q: %v", pattern, err)
		}
		if len(matches) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the disk cache did not appear within %s (nothing matched %q)", timeout, pattern)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
