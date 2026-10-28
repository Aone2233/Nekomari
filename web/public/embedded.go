package public

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// defaultDistFiles 是内嵌归档在**内存**里的那份副本（去重之后）。
//
// 它不再是 init() 的产物：磁盘缓存可用时（生产默认）根本不需要这 33 MiB，所以它由
// embeddedDistFiles() 在第一次真的用到时才解开。保留这个包级变量是为了让测试仍能把
// 归档换成合成数据（pwa_assets_test.go 的 withSyntheticArchive）。
var defaultDistFiles map[string][]byte

// embeddedDistOnce 保证 loadEmbeddedDist 只跑一次；失败结果缓存下来，不反复重试。
var (
	embeddedDistOnce sync.Once
	embeddedDistErr  error
)

// embeddedDistFiles 返回内嵌归档在内存里的那份副本，第一次用到时才解开它。
//
// 惰性是有意的：init() 里无条件解开就是 33 MiB 常驻 + 启动瞬时约 70 MiB —— 那正是
// 这一批次要消掉的东西。生产路径（磁盘缓存可用）不会走到这里；缓存不可用（只读文件
// 系统、权限、磁盘满）时 openAsset 回退到这里，于是行为与改动前完全一致。
//
// 已经有人把 defaultDistFiles 换成别的（合成归档）时直接返回它，不再去解真归档。
func embeddedDistFiles() map[string][]byte {
	embeddedDistOnce.Do(func() {
		if defaultDistFiles != nil {
			return
		}
		files, err := loadEmbeddedDist()
		if err != nil {
			embeddedDistErr = err
			return
		}
		defaultDistFiles = files
		defaultThemeMemoryLoaded.Store(true)
	})
	return defaultDistFiles
}

// standaloneAdminPrefix 是归档里面板后台界面的**第二份**副本。
//
// 装配脚本 script/embed-theme.mjs 把 frontend/dist/standalone/admin 复制到归档根的 admin/
// （那是 /admin 的字面路径），而 standalone/admin/ 下那一份原样留在归档里（那是
// /standalone/admin/... 的字面路径）。两棵子树来自同一个源目录，因此逐字节相同 ——
// 实测 248 个文件、8.26 MiB，在内存里存了两遍。
const standaloneAdminPrefix = "standalone/" + AdminDistDir + "/"

func loadEmbeddedDist() (map[string][]byte, error) {
	return decodeEmbeddedDist(embeddedDistArchive)
}

// decodeEmbeddedDist 解开归档并去掉 admin/ 与 standalone/admin/ 之间的重复副本。
//
// 分两步（decodeEmbeddedDistEntries + dedupeAdminCopies）而不是写成一趟：去重是唯一一处
// 会**删掉归档条目**的逻辑，把它单独拿出来才能有一条测试同时看到「删之前有几份」与
// 「删之后少了哪些」。
func decodeEmbeddedDist(archive []byte) (map[string][]byte, error) {
	files, err := decodeEmbeddedDistEntries(archive)
	if err != nil {
		return nil, err
	}
	dedupeAdminCopies(files)
	return files, nil
}

// dedupeAdminCopies 删掉与 admin/ 逐字节相同的 standalone/admin/ 条目（保留 admin/ 那一份）。
//
// 为什么可以删：两棵子树是同一个源目录的两次复制，而 /standalone/admin/... 的请求会由
// embeddedDistContent 回退到 admin/ 那一份（去重的另一半在 openAsset 的查找里）。
//
// 为什么只在**逐字节相同**时删：这个优化唯一的危险是「将来两边真的分叉，而服务端仍按
// 去重后的样子回答」—— 那种情况下被删掉的那份才是新的，客户端拿到的却是旧的，而且没有
// 任何日志。比较一次内容的代价是一次 O(n) 的字节比较，换来的是「分叉时行为自动退回到
// 两份都保留」：这条优化在最坏情况下的失效方式是**没有优化**，而不是服务错内容。
func dedupeAdminCopies(files map[string][]byte) {
	for name, content := range files {
		relative, ok := strings.CutPrefix(name, standaloneAdminPrefix)
		if !ok {
			continue
		}
		canonical, exists := files[AdminDistDir+"/"+relative]
		if !exists || !bytes.Equal(content, canonical) {
			continue
		}
		delete(files, name)
	}
}

// embeddedDistContent 按归档内路径取内容，未命中且路径在 standalone/admin/ 下时回退到
// admin/ 的同名文件。
//
// 回退是去重的另一半：dedupeAdminCopies 删掉的只是内存里的一份副本，而
// /standalone/admin/... 仍然是一条真实可达的 URL（独立页面按路径服务），所以那里查不到
// 时必须回到 admin/ 去取。反过来，若某次构建让两边真的不同，去重不会删，这里也就命中
// 它自己那一份 —— 两处配合的结果是「去重只在安全时生效」。
func embeddedDistContent(embedPath string) ([]byte, bool) {
	files := embeddedDistFiles()
	if content, ok := files[embedPath]; ok {
		return content, true
	}
	relative, ok := strings.CutPrefix(embedPath, standaloneAdminPrefix)
	if !ok {
		return nil, false
	}
	content, ok := files[AdminDistDir+"/"+relative]
	return content, ok
}

// decodeEmbeddedDistEntries 是纯解包：只做格式校验与路径规范化，不删任何条目。
//
// 独立于 decodeEmbeddedDist 存在是为了让去重可以被观察 —— 见上面那条注释。
func decodeEmbeddedDistEntries(archive []byte) (map[string][]byte, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("open zstd archive: %w", err)
	}
	defer decoder.Close()

	tarBytes, err := decoder.DecodeAll(archive, nil)
	if err != nil {
		return nil, fmt.Errorf("decode zstd archive: %w", err)
	}

	files := make(map[string][]byte)
	reader := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		name := path.Clean(strings.ReplaceAll(header.Name, "\\", "/"))
		if header.Typeflag == tar.TypeDir && name == "." {
			continue
		}
		if name == "." || name == ".." ||
			strings.HasPrefix(name, "../") ||
			strings.HasPrefix(name, "/") ||
			strings.ContainsRune(name, '\x00') {
			return nil, fmt.Errorf("invalid embedded tar path %q", name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
			content, err := io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("read tar entry %q: %w", name, err)
			}
			if _, exists := files[name]; exists {
				return nil, fmt.Errorf("duplicate tar entry %q", name)
			}
			files[name] = content
		default:
			return nil, fmt.Errorf("unsupported tar entry %q type %d", name, header.Typeflag)
		}
	}

	if _, ok := files[IndexFile]; !ok {
		return nil, fmt.Errorf("tar archive does not contain %q", IndexFile)
	}
	return files, nil
}
