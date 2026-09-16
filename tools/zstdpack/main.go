// zstdpack —— 把前端 dist 目录打成 Komari 内嵌主题所需的 defaultTheme/dist.tar.zst
//
// 为什么需要它：Komari 服务端用 //go:embed defaultTheme/dist.tar.zst 把默认主题
// 编进二进制（见 komari/web/public/public.go）。官方 CI 的做法是：
//     tar -cf dist.tar -C komari-web/dist .
//     zstd -19 -T0 -f dist.tar -o web/public/defaultTheme/dist.tar.zst
// 但 Windows 上不一定有 zstd CLI，所以这里直接用 Komari 自身依赖的
// github.com/klauspost/compress/zstd 来实现，保证与运行时解码器完全一致
//（解码逻辑见 komari/web/public/embedded.go 的 decodeEmbeddedDist）。
//
// 用法：
//     zstdpack -src <dist目录> -out <dist.tar.zst>
package main

import (
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/klauspost/compress/zstd"
)

func main() {
	src := flag.String("src", "", "源目录（通常是 komari-web/dist）")
	out := flag.String("out", "", "输出文件（.../defaultTheme/dist.tar.zst）")
	level := flag.Int("level", 19, "zstd 压缩级别 1-22（官方 CI 用 19）")
	flag.Parse()

	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "用法: zstdpack -src <dist目录> -out <dist.tar.zst>")
		os.Exit(2)
	}
	if err := run(*src, *out, *level); err != nil {
		fmt.Fprintf(os.Stderr, "失败: %v\n", err)
		os.Exit(1)
	}
}

func run(src, out string, level int) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("源目录不可读: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("源路径不是目录: %s", src)
	}

	// 收集文件（排序保证可复现的产物）
	var files []string
	err = filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil // 跳过符号链接/设备等，解码器只接受普通文件
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	enc, err := zstd.NewWriter(f,
		zstd.WithEncoderLevel(levelToZstd(level)),
	)
	if err != nil {
		return fmt.Errorf("创建 zstd writer: %w", err)
	}
	tw := tar.NewWriter(enc)

	var total int64
	hasIndex := false
	for _, p := range files {
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		// tar 内部统一用正斜杠；解码器会做 path.Clean
		name := filepath.ToSlash(rel)
		if name == "index.html" {
			hasIndex = true
		}
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:    name,
			Mode:    0o644,
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("写 tar 头 %s: %w", name, err)
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		n, err := io.Copy(tw, src)
		src.Close()
		if err != nil {
			return fmt.Errorf("写 tar 内容 %s: %w", name, err)
		}
		total += n
	}

	// 解码器强制要求 index.html 存在（embedded.go: files[IndexFile]）
	if !hasIndex {
		return fmt.Errorf("源目录里没有 index.html —— 解码器会拒绝这个包")
	}

	if err := tw.Close(); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	st, _ := os.Stat(out)
	var packed int64
	if st != nil {
		packed = st.Size()
	}
	ratio := 0.0
	if total > 0 {
		ratio = float64(packed) / float64(total) * 100
	}
	fmt.Printf("已打包 %d 个文件\n", len(files))
	fmt.Printf("  原始 %s -> 压缩后 %s (%.1f%%)\n", human(total), human(packed), ratio)
	fmt.Printf("  输出 %s\n", out)
	return nil
}

func levelToZstd(level int) zstd.EncoderLevel {
	switch {
	case level <= 2:
		return zstd.SpeedFastest
	case level <= 6:
		return zstd.SpeedDefault
	case level <= 12:
		return zstd.SpeedBetterCompression
	default:
		return zstd.SpeedBestCompression
	}
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
