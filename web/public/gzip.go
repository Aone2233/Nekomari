package public

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// 静态资源的 gzip 响应压缩。
//
// 为什么只做在这一层：serveAsset 是**静态资源**的出口 —— 正文是归档或磁盘上的一个文件，
// 长度已知、类型已知、内容不变，压缩是纯收益。同一条服务上还有 WebSocket（实时监控数据）、
// SSE 与 JSON-RPC 流，它们的响应是逐帧产生的、没有已知长度；那里套一层 gzip 只会给每一帧
// 增加延迟和一层缓冲，换不来任何传输量 —— 所以压缩只发生在 serveAsset 里。
//
// 为什么不预压缩后存进归档：同一份内容压完的大小随内容变化，预压缩要在归档里给每份资源
// 再存一份（此后每次构建都要比对两份是否一致）。这里用流式压缩，代价是一次固定的 CPU，
// 换来归档不变大。

// gzipMinAssetSize 是启用压缩的最小体积。
//
// 低于这个值的响应压完可能反而更大：gzip 的固定开销是十几字节，还要多一层分块传输的
// 状态。面板里这个量级的文件是 favicon、PWA 清单、主题清单，都是几百字节 —— 压它们
// 没有收益，反而是每次请求多一份 CPU 开销。
const gzipMinAssetSize = 1024

// compressibleMimeTypes 是「压了确实更小」的静态资源类型，与 text/* 一起构成白名单。
//
// 为什么用白名单而不是「不是已知压缩格式就压」：图片（png/jpeg/webp/avif）与字体
// （woff/woff2）的正文本身就是压缩过的格式，再压一遍既压不动又白烧 CPU。反过来
// application/wasm 是二进制，但它的压缩率很高、浏览器也允许以 gzip 内容编码接收，
// 而它恰好是面板里最大的资源之一（Monaco 等按需加载的块），漏掉它是实打实的传输量。
var compressibleMimeTypes = map[string]struct{}{
	"application/javascript":    {},
	"application/json":          {},
	"application/manifest+json": {},
	"image/svg+xml":             {},
	"application/wasm":          {},
}

// isCompressibleAssetType 报告一个 Content-Type 是否属于可压缩的那一类。
//
// 用 mime.ParseMediaType 而不是按前缀比较字符串：真实值带参数（面板钉死的
// "text/javascript; charset=utf-8" 就是这样），而宿主 MIME 表给的大小写也不保证统一
// （Windows 注册表里可能是 "Text/JavaScript"）—— ParseMediaType 同时处理这两件事，
// 它返回的 media type 已经小写、去掉参数。
func isCompressibleAssetType(mimeType string) bool {
	if mimeType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return false
	}
	if _, ok := compressibleMimeTypes[mediaType]; ok {
		return true
	}
	return strings.HasPrefix(mediaType, "text/")
}

// gzipAcceptable 报告 Accept-Encoding 是否允许 gzip。
//
// 只认显式写出的 gzip，也不看通配符 *：这是保守的一侧。漏压一个响应只是少了一次优化；
// 而给一个明确只要 identity 的客户端发 gzip，是客户端解不开的响应体 —— 代价不对等。
//
// q=0 是**明确拒绝**，不是「没写优先级」：HTTP 用 gzip;q=0 表达「别给我 gzip」。
// 把它当成接受，得到的是一个被客户端丢掉的响应，而服务端日志里一切正常。
func gzipAcceptable(acceptEncoding string) bool {
	for _, entry := range strings.Split(acceptEncoding, ",") {
		fields := strings.Split(entry, ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		refused := false
		for _, parameter := range fields[1:] {
			name, value, ok := strings.Cut(parameter, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			if q, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && q <= 0 {
				refused = true
			}
		}
		if !refused {
			return true
		}
	}
	return false
}

// shouldGzipAsset 汇总「这一次响应要不要 gzip」的全部条件。
//
// 只在 GET 上压：HEAD 没有正文，声明 Content-Encoding 却按原文长度写头，只会让头部与
// 实际语义对不上。
//
// Range / If-Range 一律走 identity：ServeContent 会把这种请求改写成 206 与文件里的一段
// 字节区间，而那段区间在原文件里的偏移与压缩后字节流的偏移毫无关系（Range 的单位是
// 所选表示的字节，不是原文的字节）。客户端要的是原始字节，压了就是错的。
//
// 已经有 Content-Encoding 的响应绝不二次压缩：客户端会按两层编码去解，而实际只有一层。
func shouldGzipAsset(r *http.Request, header http.Header, mimeType string, size int64) bool {
	if r == nil || r.Method != http.MethodGet {
		return false
	}
	if r.Header.Get("Range") != "" || r.Header.Get("If-Range") != "" {
		return false
	}
	if header.Get("Content-Encoding") != "" {
		return false
	}
	if size < gzipMinAssetSize {
		return false
	}
	if !isCompressibleAssetType(mimeType) {
		return false
	}
	return gzipAcceptable(r.Header.Get("Accept-Encoding"))
}

// addVary 把 value 并进 Vary，已经存在时不重复添加。
//
// 用 Add 而不是 Set：Vary 可能已经被别处（中间件、gin）写过，直接覆盖会把它们的缓存键
// 维度抹掉 —— 抹掉的后果是「按一种变体缓存、回给所有人」，而 Vary 存在的意义正是防止这件事。
func addVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

// gzipWriterPool 复用 gzip.Writer。
//
// 每次 NewWriter 都要分配约 1MB 的压缩窗口与哈希表，而静态资源的请求量正好是「每个访客、
// 每个资源一次」—— 不复用的话这部分分配会成为每次打开面板时最显眼的一处 GC 来源。
// Reset/Close 之间复用是 gzip.Writer 的既有用法，Close 之后 Reset 会清掉 closed 状态。
//
// 压缩级别用 gzip 的默认值（6）：这些正文是内容不变的构建产物，浏览器与边缘都会长期缓存，
// 所以「压得更小」值回那一点 CPU，而「压得更快」省下的时间只发生在这份内容第一次被取走时。
var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

// gzipResponseWriter 把 ServeContent 的「写头」与「写正文」分别接到两个落点：头改在真正的
// ResponseWriter 上，正文交给 gzip.Writer。
//
// 为什么必须在 WriteHeader 里改头：ServeContent 知道文件的完整长度，会先把 Content-Length
// 写进头部再写正文；而压缩后的长度只有写完之后才知道，gzip 也不保证与原文同长。头部一旦
// 交给 net/http 就改不动了 —— WriteHeader 是最后一个还能动它的点。删掉长度之后由分块
// 传输决定边界，这也是 gzip 动态压缩的常规形状。
//
// 为什么内嵌 gin.ResponseWriter 而不是包一层裸的 http.ResponseWriter：它的 Status()
// 与其它的响应语义仍然走 gin 自己的实现，访问日志里的状态码不会退成默认值。
//
// 但 Size() 并没有被保住：正文是写进 gzip.Writer 的，gin 只看到「一个字节都没写」，所以
// 压缩响应的 Size 是 0 而不是压缩后的长度。当前访问日志只读 Status，因此没有功能影响；
// 将来若要记录传输字节数，必须在这里自己累计（下面的 plain 记的是原文长度，压缩后长度
// 只有 gzip.Writer 知道）。
type gzipResponseWriter struct {
	gin.ResponseWriter
	body io.Writer
	// plain 记录实际交给压缩器的原文字节数。304（条件请求命中）没有正文，gzip.Writer
	// 就没地方可写，也不能 Close（那会往一个不允许带正文的响应里塞头部与尾部）。
	plain int64
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	n, err := w.body.Write(p)
	w.plain += int64(n)
	return n, err
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	header := w.ResponseWriter.Header()
	// 只有带正文的 200 才声明编码。304 由 ServeContent 按规范处理（它已经清掉了
	// Content-Type / Content-Length / Content-Encoding），这里再写一个 Content-Encoding
	// 会得到一个「声明了 gzip 却没有正文」的响应。
	if code == http.StatusOK {
		header.Del("Content-Length")
		header.Set("Content-Encoding", "gzip")
	}
	w.ResponseWriter.WriteHeader(code)
}

// serveAssetContent 是静态资源的最终出口：补 Vary、按需压缩，其余情况原样交给
// http.ServeContent。
//
// 为什么抽成一个函数：serveAsset（主题与独立页面的资源）和 serveAdminAsset（/admin 子树）
// 的差别只在缓存头怎么算，正文该怎么发是一样的。此前 serveAdminAsset 直接调
// http.ServeContent，于是面板自己最大的那批资源 —— 后台入口包与按需加载的 Monaco 块，
// 都在 /admin/* 下 —— 成了唯一没有压缩的静态路径。
//
// name 只用于 http.ServeContent 的类型兜底与错误信息；调用方按各自的既有形状传
// （serveAdminAsset 传 path.Base(relative)，与它原来那次调用一致）。
func serveAssetContent(c *gin.Context, source assetSource, name string) {
	header := c.Writer.Header()
	// 可压缩的类型一律声明 Vary，无论这一次是否真的压缩：同一个 URL 的响应会随
	// Accept-Encoding 变化（gzip / identity），缓存必须把这个维度算进键里。少了它，
	// 一份压缩过的响应会被发给不支持 gzip 的客户端，反过来也一样。
	if isCompressibleAssetType(source.mimeType) {
		addVary(header, "Accept-Encoding")
	}
	// 压缩只发生在这一层：WebSocket、SSE 与 JSON-RPC 走的是别的路径，那里逐帧生成、
	// 没有已知长度，不该被这里影响。
	if shouldGzipAsset(c.Request, header, source.mimeType, source.size) {
		serveGzipAsset(c, source, name)
		return
	}
	http.ServeContent(c.Writer, c.Request, name, source.modTime, source.reader)
}

// serveGzipAsset 用 gzip 输出一个静态资源。
//
// ETag、Last-Modified 与 Cache-Control 都由 serveAsset 在调用前设好，这里只接管正文：
// 校验值仍然按原文计算 —— 同一个 URL 的两种编码共用一个 ETag，靠 Vary 让缓存分开存。
// 这也是 HTTP 允许的（ETag 标识的是「同一个资源」，不是某一种字节流）；面板的 ETag 由
// 长度与修改时间组成，对两种编码都稳定。
func serveGzipAsset(c *gin.Context, source assetSource, name string) {
	gzipWriter := gzipWriterPool.Get().(*gzip.Writer)
	defer func() {
		// 放回池里之前先断开与本次响应的引用：Reset(io.Discard) 之后这个 writer 不再
		// 持有 ResponseWriter，池里的对象也就不会把一次请求的内存拖到下一次。
		gzipWriter.Reset(io.Discard)
		gzipWriterPool.Put(gzipWriter)
	}()
	// 压缩后的字节直接写给真正的 ResponseWriter；ServeContent 写正文时经过下面这层包装，
	// 它把原文交给 gzipWriter。
	gzipWriter.Reset(c.Writer)

	writer := &gzipResponseWriter{ResponseWriter: c.Writer, body: gzipWriter}
	http.ServeContent(writer, c.Request, name, source.modTime, source.reader)

	if writer.plain > 0 {
		// Close 写出 gzip 的尾部（CRC 与长度）并冲刷缓冲。不 Close 的话响应体是不完整的
		// gzip 流：浏览器会把它当成截断的内容丢弃，而状态码仍然是 200。
		_ = gzipWriter.Close()
	}
}
