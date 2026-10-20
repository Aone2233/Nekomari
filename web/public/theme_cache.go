package public

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	logger "github.com/Aone2233/nekomari/utils/log"
)

// 默认主题的磁盘缓存。
//
// 背景：内嵌归档 defaultTheme/dist.tar.zst 解压后是 32.96 MiB、约 1080 个文件，改动前
// init() 无条件把它们全解进 map[string][]byte —— 33 MiB 常驻，启动瞬时峰值约
// 70 MiB（解压缓冲 + map）。批次 A 只去掉了 admin/ 与 standalone/admin/ 的重复副本
// （8.26 MiB）。
//
// 这里做的事：第一次真正需要默认主题的资源时，把归档**流式**解到磁盘（不建 map），
// 此后默认主题的每个资源都由 os.File 直接供给，内存里不再留整份副本。物化失败（只读
// 文件系统、权限、磁盘满）就退回原来的内存 map —— 行为与改动前完全一致，只是慢一点。
//
// 为什么不在 init() 里物化：那会让「启动」变成一次 33 MiB 的磁盘写入，而且测试、以及
// 任何只是启动一下的进程都会写盘。物化因此挂在第一次资源查找上（sync.Once）。
//
// 缓存不在内存里留任何索引：目录内容就是归档内容，查找是一次 os.Stat。
const (
	// themeCacheDirName 是缓存根目录，位于 DataDir 下。
	themeCacheDirName = ".theme-cache"
	// themeCacheMarker 是「这次物化完整地写到了最后」的标记文件。
	themeCacheMarker = ".complete"
	// themeCacheEnvVar 是运维开关：设为 0/false/off/no 时不启用磁盘缓存。
	themeCacheEnvVar = "KOMARI_THEME_CACHE"
)

// themeCacheManifest 是标记文件的内容：条目数与总字节数。
//
// 它不只是装饰。「目录里有 .complete」只说明上一次物化写完了最后一个文件；进程若在
// 写文件期间被强杀或掉电，页缓存里的数据可能没有落盘。启动后第一次用到缓存时按目录里
// 的实际大小复核一遍（约 1080 次 stat，几毫秒），对不上就当作不完整重新物化 —— 宁可
// 多解一次压，也不能把截断的 JS/CSS 当成完好的资源发给浏览器（那表现为随机的语法错误，
// 且只在升级后的某次启动里出现）。
type themeCacheManifest struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// themeCacheState 按进程缓存一次物化结果。err 非 nil 时不再重试：只读文件系统不会
// 因为第二个请求就变成可写。
//
// 为什么不是 sync.Once：物化现在有两条入口 —— 启动预热（后台 goroutine）与第一个请求。
// 用例之间需要清状态（它们会 t.Chdir 到各自的临时目录），而重置一个**正在 Do 里**的
// sync.Once 会让进程直接 fatal（"sync: unlock of unlocked mutex"），那不是一条用例失败，
// 是整个测试二进制崩掉。done 通道同时表达两件事：只做一次，以及「这次做完了没有」。
type themeCacheState struct {
	mu   sync.Mutex
	dir  string
	err  error
	done chan struct{} // 非 nil 表示物化已开始；关闭表示 dir/err 已经写定
}

var defaultThemeCache themeCacheState

// themeCacheEnabled 是包级总开关。生产恒为 true；测试在 TestMain 里关掉它，好让既有
// 用例继续走「缓存不可用 → 内存 map」这条路径（也就是线上只读文件系统时走的路径）。
// 运维版本是 KOMARI_THEME_CACHE=0，见 themeCacheDisabledByEnvironment。
var themeCacheEnabled = true

var (
	themeCacheEnvOnce     sync.Once
	themeCacheEnvDisabled bool
)

// themeCacheAllowed 汇总「这一次要不要用磁盘缓存」。
func themeCacheAllowed() bool {
	if !themeCacheEnabled {
		return false
	}
	themeCacheEnvOnce.Do(func() {
		themeCacheEnvDisabled = themeCacheDisabledByEnvironment()
	})
	return !themeCacheEnvDisabled
}

// themeCacheDisabledByEnvironment 解析 KOMARI_THEME_CACHE。
//
// 只认明确写出的关闭值：环境变量写错时保持启用，因为缓存本身有完整的失败回退，误判为
// 启用不会造成故障，反而还能拿到内存收益。
func themeCacheDisabledByEnvironment() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(themeCacheEnvVar))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

var (
	themeCacheKeyOnce sync.Once
	themeCacheKey     string
)

// embeddedDistArchiveKey 是内嵌归档的 sha256 前 16 个 hex 字符。
//
// 用内容哈希而不是版本号：归档一换（升级二进制、CI 重新装配），目录名跟着换，旧目录
// 再也不会被读到，可以安全删掉；同一个二进制里它恒定，缓存因此不会被另一份归档复用。
func embeddedDistArchiveKey() string {
	themeCacheKeyOnce.Do(func() {
		sum := sha256.Sum256(embeddedDistArchive)
		themeCacheKey = hex.EncodeToString(sum[:])[:16]
	})
	return themeCacheKey
}

// defaultThemeCacheDir 返回默认主题磁盘缓存的目录，必要时先把它物化出来。
//
// 返回的目录一定带匹配的标记文件；物化失败时返回 error，调用方回退到内存 map。
// 一次物化只有一次：并发的调用方（启动预热与第一个请求）都等同一个 done —— 重复写
// 33 MiB 是白花的磁盘与 CPU。失败也只记录一次，不会每次请求都刷一行日志。
func defaultThemeCacheDir() (string, error) {
	defaultThemeCache.mu.Lock()
	if done := defaultThemeCache.done; done != nil {
		defaultThemeCache.mu.Unlock()
		<-done // 已经有人在物化：等它的结果，而不是再写一遍
		defaultThemeCache.mu.Lock()
		dir, err := defaultThemeCache.dir, defaultThemeCache.err
		defaultThemeCache.mu.Unlock()
		return dir, err
	}
	done := make(chan struct{})
	defaultThemeCache.done = done
	defaultThemeCache.mu.Unlock()

	dir, err := materializeDefaultThemeCache()
	if err != nil {
		logger.Warnf("theme-cache",
			"default theme disk cache unavailable, falling back to the in-memory archive: %v", err)
	} else {
		logger.Infof("theme-cache", "default theme materialised at %s; the in-memory copy is no longer needed", dir)
	}

	defaultThemeCache.mu.Lock()
	defaultThemeCache.dir, defaultThemeCache.err = dir, err
	defaultThemeCache.mu.Unlock()
	close(done)
	return dir, err
}

// themeCacheSettled 返回一个在「已经开始的那次物化」结束后关闭的 channel；没有正在跑的
// 物化时返回一个已关闭的 channel。
//
// 只给测试用：预热是异步的，而用例在重置状态（它们会 t.Chdir 到各自的临时目录）之前
// 必须确认后台那次物化已经落地，否则 t.TempDir 的清理会与它打架。
func themeCacheSettled() <-chan struct{} {
	defaultThemeCache.mu.Lock()
	defer defaultThemeCache.mu.Unlock()
	if done := defaultThemeCache.done; done != nil {
		return done
	}
	settled := make(chan struct{})
	close(settled)
	return settled
}

// materializeDefaultThemeCache 把内嵌归档解到 data/.theme-cache/<archive-hash>/ 并返回该目录。
//
// 发布方式：先在同级建 .tmp-<pid>/，写完所有条目与标记文件之后再 rename 成正式目录。
// rename 之前目录里没有标记，所以任何中途留下的残骸都不会被当成缓存用。
func materializeDefaultThemeCache() (string, error) {
	root := filepath.Join(DataDir, themeCacheDirName)
	key := embeddedDistArchiveKey()
	target := filepath.Join(root, key)

	// 上一次物化可能留下一个同名的半份目录，或者被别的东西占位成了一个普通文件。
	// 两种都不是完整缓存，先清掉 —— 尤其是普通文件：它会让下面的标记读取报
	// ENOTDIR 而不是「文件不存在」，于是缓存永远不会重建。
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		if err := os.Remove(target); err != nil {
			return "", fmt.Errorf("clear theme cache placeholder: %w", err)
		}
	}

	if complete, err := themeCacheIsComplete(target); err != nil {
		return "", err
	} else if complete {
		removeStaleThemeCaches(root, key)
		return target, nil
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create theme cache root: %w", err)
	}
	staging := fmt.Sprintf("%s.tmp-%d", target, os.Getpid())
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clear staging directory: %w", err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}

	written, err := extractEmbeddedDist(staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	// 复核磁盘上的实际结果：写出时统计到的条目数与总字节数必须与目录里的一致。
	// 这条同时兜住「归档里出现同名条目、后一个覆盖了前一个」这种会让标记永远对不上的
	// 情形 —— 那时宁可报错回退，也不要留下一个每次启动都要重新物化的目录。
	usage, err := themeCacheDirectoryUsage(staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("measure staged theme cache: %w", err)
	}
	if usage != written {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("staged theme cache is inconsistent: wrote %d files/%d bytes, found %d files/%d bytes",
			written.Files, written.Bytes, usage.Files, usage.Bytes)
	}
	if err := writeThemeCacheMarker(staging, usage); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}

	// Windows 上 os.Rename 覆盖不了一个已存在的目录，而 target 可能正是一份不完整的
	// 缓存（比如上次在解压中途被杀）。先删再改名；删不掉就让错误如实上报。
	if err := removeThemeCachePath(target); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("replace incomplete theme cache: %w", err)
	}
	if err := renameThemeCache(staging, target); err != nil {
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("publish theme cache: %w", err)
	}

	removeStaleThemeCaches(root, key)
	return target, nil
}

// renameThemeCache publishes the staging directory, retrying briefly on Windows.
//
// Windows returns a sharing violation when another process (indexer, antivirus, an
// open handle) has any file in the tree open, and that is transient. Production hit
// this during a prewarm on CI: the rename failed, the cache fell back to the 33 MiB
// in-memory map, and the next request paid the extraction again. A few hundred
// milliseconds of retry keeps the disk path, which is the whole point of the cache.
// Other platforms get the atomic rename with no retry.
func renameThemeCache(staging, target string) error {
	const attempts = 20
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = os.Rename(staging, target)
		if err == nil || runtime.GOOS != "windows" {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return err
}

// removeThemeCachePath clears a previous incomplete cache, with the same Windows
// sharing-violation retry as renameThemeCache.
func removeThemeCachePath(target string) error {
	const attempts = 20
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = os.RemoveAll(target)
		if err == nil || runtime.GOOS != "windows" {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return err
}

// themeCacheIsComplete 报告 dir 是不是一份完整、且大小与标记一致的缓存。
func themeCacheIsComplete(dir string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, themeCacheMarker))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// 目录不存在、没有标记、或只有半份内容：都当作「需要重新物化」。
			return false, nil
		}
		return false, fmt.Errorf("read theme cache marker: %w", err)
	}
	var want themeCacheManifest
	if err := json.Unmarshal(raw, &want); err != nil {
		// 标记本身不认识（旧版本写的、或写坏了）：同样只是重新物化一次。
		return false, nil
	}
	got, err := themeCacheDirectoryUsage(dir)
	if err != nil {
		return false, fmt.Errorf("measure theme cache: %w", err)
	}
	return got == want, nil
}

// themeCacheDirectoryUsage 统计目录里的普通文件数与总字节数（跳过标记文件本身）。
func themeCacheDirectoryUsage(dir string) (themeCacheManifest, error) {
	var usage themeCacheManifest
	err := filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if filepath.ToSlash(relative) == themeCacheMarker {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		usage.Files++
		usage.Bytes += info.Size()
		return nil
	})
	return usage, err
}

// writeThemeCacheMarker 在 dir 里写下标记文件。
func writeThemeCacheMarker(dir string, manifest themeCacheManifest) error {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode theme cache marker: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, themeCacheMarker), raw, 0o644); err != nil {
		return fmt.Errorf("write theme cache marker: %w", err)
	}
	return nil
}

// removeStaleThemeCaches 尽力删掉根目录下不属于 key 的旧缓存（升级后归档换了，旧目录
// 再也不会被读到）。失败一律忽略：删不掉一个旧目录不该让默认主题加载失败。
//
// 边界：两个进程共用一个 data/ 时，这里可能删掉另一个进程正在写的 .tmp-<pid>。
// 那一边的物化会失败并回退到内存副本 —— 面板本来就是单进程持有 data/ 的（SQLite），
// 所以这里选择简单而不是加锁。
func removeStaleThemeCaches(root, key string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() == key {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, entry.Name()))
	}
}

// extractEmbeddedDist 把内嵌归档流式解到 dir，返回写出的条目数与字节数。
//
// 刻意不走 loadEmbeddedDist：那条路会在内存里建出 33 MiB 的 map，而「把归档放到磁盘」
// 的全部意义就是不再需要它。这里从 zstd 解码器直接读 tar，任何时刻只持有一个文件的
// 内容，峰值内存与最大的那个文件同阶，与归档总量无关。
//
// 解出来的是**归档的原样**，包括 admin/ 与 standalone/admin/ 那一对逐字节相同的副本：
// 去重（dedupeAdminCopies）是内存里的优化，在磁盘上再做一遍只会多一份可能与它分叉的
// 实现，而磁盘上多出来的那 8 MiB 占的是空间，不是常驻内存。
func extractEmbeddedDist(dir string) (themeCacheManifest, error) {
	var written themeCacheManifest

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return written, fmt.Errorf("open zstd archive: %w", err)
	}
	defer decoder.Close()
	if err := decoder.Reset(bytes.NewReader(embeddedDistArchive)); err != nil {
		return written, fmt.Errorf("reset zstd decoder: %w", err)
	}

	reader := tar.NewReader(decoder)
	sawIndex := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return written, fmt.Errorf("read tar entry: %w", err)
		}

		// 与 decodeEmbeddedDistEntries 同一套规范化与校验：解压到磁盘的路径同样是
		// 不可信输入（归档由 script/embed-theme.mjs 生成，而它的输入是构建产物）。
		name := path.Clean(strings.ReplaceAll(header.Name, "\\", "/"))
		if header.Typeflag == tar.TypeDir && name == "." {
			continue
		}
		if name == "." || name == ".." ||
			strings.HasPrefix(name, "../") ||
			strings.HasPrefix(name, "/") ||
			strings.ContainsRune(name, 0) {
			return written, fmt.Errorf("invalid embedded tar path %q", name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return written, fmt.Errorf("unsupported tar entry %q type %d", name, header.Typeflag)
		}

		relative := filepath.FromSlash(name)
		if !isSafePath(dir, relative) {
			return written, fmt.Errorf("embedded tar path %q escapes the cache directory", name)
		}
		target := filepath.Join(dir, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return written, fmt.Errorf("create directory for %q: %w", name, err)
		}
		size, err := writeArchiveEntry(target, reader)
		if err != nil {
			return written, fmt.Errorf("write %q: %w", name, err)
		}
		if size != header.Size {
			return written, fmt.Errorf("tar entry %q is short: wrote %d of %d bytes", name, size, header.Size)
		}
		if name == IndexFile {
			sawIndex = true
		}
		written.Files++
		written.Bytes += size
	}

	if !sawIndex {
		return written, fmt.Errorf("tar archive does not contain %q", IndexFile)
	}
	return written, nil
}

// writeArchiveEntry 把一个 tar 条目的正文写进 target，返回实际写出的字节数。
func writeArchiveEntry(target string, reader io.Reader) (int64, error) {
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}

// PrewarmDefaultThemeCache 在后台开始物化默认主题的磁盘缓存。
//
// 为什么需要预热：物化挂在第一次资源查找上（见上面的注释），而它要写 1080 个文件、
// 32.96 MiB —— 实测 300-370ms，全部落在第一个访客的请求上。启动时预热把这段时间挪到
// 进程还没有负载的时候。
//
// 为什么是异步：启动不该为一次磁盘写入停下来。预热失败（只读文件系统、权限、磁盘满）
// 与「第一个请求触发物化」失败时的行为完全相同：defaultThemeCacheDir 把失败缓存在
// state 里、只记一次 Warn，随后所有查找回退到内存副本。
//
// 不会重复物化：预热、第一个请求、以及任何并发调用都等同一个 done（见
// themeCacheState）。已经物化过、正在物化、或物化失败过时，这里要么立刻返回，要么与
// 那次物化共享同一个结果。
//
// 缓存被关闭（KOMARI_THEME_CACHE=0 或包级开关）时什么都不做，调用方不必自己判断 ——
// 但调用方仍要判断**主题**：这份缓存就是内嵌归档，只有默认主题用它（见
// internal/server/bootstrap.go 的调用点）。
func PrewarmDefaultThemeCache() {
	_ = prewarmDefaultThemeCacheAsync()
}

// prewarmDefaultThemeCacheAsync 与 PrewarmDefaultThemeCache 相同，但交还一个在后台那次
// 物化结束后关闭的 channel。
//
// 只给测试用：预热是异步的，而用例在重置状态或清理 t.TempDir 之前必须等它落地 ——
// 否则会出现「用例已经结束、后台还在往刚被删掉的临时目录里写」这种竞态。
func prewarmDefaultThemeCacheAsync() <-chan struct{} {
	done := make(chan struct{})
	if !themeCacheAllowed() {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		// 失败已由 defaultThemeCacheDir 记过一次 Warn，这里只需要把这次物化推进完；
		// 回退到内存副本的决定由请求路径做。
		_, _ = defaultThemeCacheDir()
	}()
	return done
}

// lookupDefaultThemeCachedAsset 在默认主题的磁盘缓存里查找 name（相对归档根，例如
// "index.html"、"assets/entry-*.js"、"admin/index.html"）。
//
// 返回三个值：命中的资源、是否命中、以及**缓存能不能代表归档**。
//
// 第三个值是这里最关键的一条：物化成功之后，缓存目录就是归档的完整副本（物化时逐条
// 校验过条目数与总字节数），因此「缓存里没有」等价于「归档里没有」—— 调用方此时绝不该
// 再去解那份 33 MiB 的内存副本。少了这条，一个过期资源的 404（真实存在：升级后浏览器
// 还拿着上一版的 chunk 名）就会把整份归档解进内存，把所有收益一次抵消掉。
// 反过来，未启用/物化失败/文件读不出来时第三个值是 false，调用方回退内存副本 ——
// 那是「行为与改动前一致」的那条路。
//
// 返回的 assetSource 里 reader 是 *os.File，close 负责关闭它 —— serveAsset 与
// readAsset 都已经按这个形状释放资源，因此这两条路径都不需要改动。
//
// modTime 用**文件自己的修改时间**，而不是 embeddedAssetModTime（进程启动时间）：
//   - ETag 由「长度-纳秒时间」组成，文件时间在同一个二进制里恒定（目录名带归档内容
//     哈希，内容换了目录就换了），所以协商缓存的行为与内嵌资源一致；
//   - Last-Modified 是真实的，If-Modified-Since 也能正确命中，而且重启进程不会让
//     所有缓存的校验值失效（用进程启动时间时每次重启都会强制回源一次）。
//
// 名字来自 URL，所以这里再校验一次：clean 之后仍含 ".." 的拒绝；前导 "/" 先剥掉，
// 等价于从归档根解析（路由交上来的路径本来就带前导斜杠），再由 isSafePath 复核目录内
// 定位。两道都不是装饰 —— 这个路径最终会交给 os.Open。
func lookupDefaultThemeCachedAsset(name string) (assetSource, bool, bool) {
	if !themeCacheAllowed() {
		return assetSource{}, false, false
	}
	dir, err := defaultThemeCacheDir()
	if err != nil || dir == "" {
		return assetSource{}, false, false
	}
	// 到这里缓存已经就绪：它是归档的完整副本，未命中就是「归档里没有」。
	relative, ok := themeCacheRelativeName(name)
	if !ok {
		return assetSource{}, false, true
	}
	if !isSafePath(dir, filepath.FromSlash(relative)) {
		return assetSource{}, false, true
	}
	filePath := filepath.Join(dir, filepath.FromSlash(relative))
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		// 「归档里没有这个文件」与「整个缓存目录已经不在了」必须分开：前者是可靠的
		// 404，后者说明缓存不再可信（运维手误、临时目录清理、磁盘被换掉）—— 那时
		// 必须交回内存副本，否则一次运行期的删除会让默认主题的每个资源都变成 404。
		if _, dirErr := os.Stat(dir); dirErr != nil {
			return assetSource{}, false, false
		}
		return assetSource{}, false, true
	}
	file, err := os.Open(filePath)
	if err != nil {
		// 目录被外部清掉、权限变了、文件刚被删：这一次交回内存副本，宁可慢也不要 404。
		return assetSource{}, false, false
	}
	return assetSource{
		reader:   file,
		modTime:  info.ModTime(),
		mimeType: assetMimeType(filepath.Ext(filePath)),
		size:     info.Size(),
		close:    func() { _ = file.Close() },
	}, true, true
}

// themeCacheRelativeName 把 URL 形态的名字归一化成缓存目录里的相对路径。
func themeCacheRelativeName(name string) (string, bool) {
	cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// resetThemeCache 清空缓存状态：已物化的目录、失败结果与环境判断。
//
// 只给测试用：状态按进程缓存，而用例之间会 t.Chdir 到各自的临时目录，DataDir 因此
// 指向不同的地方。调用前必须确认没有在跑的物化（themeCacheSettled），否则后台那次
// 物化会在重置之后把结果写回来 —— 不会崩，但会让后面的用例看到一个指向已删目录的状态。
func resetThemeCache() {
	defaultThemeCache = themeCacheState{}
	themeCacheEnvOnce = sync.Once{}
	themeCacheEnvDisabled = false
}
