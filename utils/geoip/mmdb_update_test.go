package geoip

// 这个文件是内部测试包（package geoip），因为它需要直接构造带 dbFilePath 的
// MaxMindGeoIPService，而不触发 NewMaxMindGeoIPService 的联网下载。
//
// 背景（F11/P7-3）：UpdateDatabase 原来在函数顶部取写锁、只在成功路径上解锁，五条早退
// 全部持锁返回。任何一次下载失败都会让 GetGeoInfo（RLock）永久阻塞。前两个用例都
// 指向 httptest 服务器，不访问真实网络，也不写仓库里的任何文件；第三个用例还需要一份
// 真实的 mmdb 作为"下载源"，本地没有就跳过（详见该用例）。

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newMmdbTestService 建一个自包含的服务实例：临时目录里的库文件 + 指定 URL。
// 返回原库文件内容，供调用方断言"失败后线上文件未被改动"。
func newMmdbTestService(t *testing.T, handler http.HandlerFunc) (*MaxMindGeoIPService, string, []byte) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	restoreURL := GeoIpUrl
	GeoIpUrl = server.URL
	t.Cleanup(func() { GeoIpUrl = restoreURL })

	path := filepath.Join(t.TempDir(), "GeoLite2-Country.mmdb")
	original := bytes.Repeat([]byte("original-mmdb-image"), 64) // 1216 字节，非空
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed the live mmdb file: %v", err)
	}

	return &MaxMindGeoIPService{dbFilePath: path}, path, original
}

// 下载返回 500 时：UpdateDatabase 必须返回 error，原库文件必须原样保留，而且
// GetGeoInfo 必须在 2 秒内返回 —— 修复前它会在写锁上永久阻塞（唯一 Unlock 在
// 成功路径上，早退把锁带走了）。
func TestUpdateDatabaseFailureDoesNotBlockGetGeoInfo(t *testing.T) {
	service, path, original := newMmdbTestService(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	if err := service.UpdateDatabase(); err == nil {
		t.Fatal("UpdateDatabase 应在 HTTP 500 时返回 error")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("失败后应仍能读回原库文件: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("下载失败后线上 mmdb 文件被改动了：大小 %d，期望 %d", len(got), len(original))
	}

	done := make(chan error, 1)
	go func() {
		// 返回什么不重要（此时 reader 仍是 nil，会返回"未初始化"）。要证明的是它
		// 没有被写锁挡住 —— 能返回就说明锁已释放。
		_, err := service.GetGeoInfo(net.ParseIP("8.8.8.8"))
		done <- err
	}()

	select {
	case err := <-done:
		t.Logf("GetGeoInfo 正常返回（err=%v），写锁未被泄漏", err)
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateDatabase 下载失败 2 秒后 GetGeoInfo 仍被阻塞：写锁从未释放")
	}
}

// 200 但内容不是 mmdb（例如错误页/截断响应）时：必须拒绝，线上文件保持原样，
// 临时文件不能留下。
func TestUpdateDatabaseRejectsNonMmdbBody(t *testing.T) {
	service, path, original := newMmdbTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>gateway error</body></html>"))
	})

	if err := service.UpdateDatabase(); err == nil {
		t.Fatal("200 但内容不是 mmdb 时应被校验拒绝")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("失败后应仍能读回原库文件: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("非法内容通过了替换：大小 %d，期望 %d", len(got), len(original))
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Errorf("临时文件 %s.new 未被清理（stat err=%v）", path, err)
	}
}

// 成功路径：下载到有效 mmdb → 校验通过 → 原子替换掉一个旧的（这里故意放成截断的）
// 线上文件 → initialize() 重新加载 → 查询可用。
//
// 这个用例专门盯住 F11 的自死锁陷阱：initialize() 自己也取写锁，如果实现改成在同一个
// 函数里 defer Unlock 再调用它，UpdateDatabase 会永久卡住 —— 用 20 秒看门狗把它报成
// 明确失败，而不是让整个测试进程挂到超时。
//
// 需要一份真实的 mmdb 作为下载源。本地 ./data/GeoLite2-Country.mmdb 是运行过 TestMmdb
// 后留下的（已 gitignore），CI 上没有；没有就跳过，而不是去联网下载。
func TestUpdateDatabaseReplacesTheLiveFileAndReloads(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(".", "data", "GeoLite2-Country.mmdb"))
	if err != nil {
		t.Skipf("缺少本地 mmdb 下载源（%v）；该用例只在已存在 ./data/GeoLite2-Country.mmdb 时运行", err)
	}

	service, path, original := newMmdbTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	if bytes.Equal(original, payload) {
		t.Fatal("用例前提有误：种子内容不应等于下载内容")
	}

	done := make(chan error, 1)
	go func() { done <- service.UpdateDatabase() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("成功路径不应返回 error: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("UpdateDatabase 20 秒未返回：initialize() 在自己持锁的情况下被调用，自死锁")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("替换后应能读回线上文件: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("线上文件未被替换：大小 %d，期望 %d", len(got), len(payload))
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Errorf("临时文件 %s.new 未被清理（stat err=%v）", path, err)
	}

	info, err := service.GetGeoInfo(net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("替换并重载后查询应可用: %v", err)
	}
	if info.ISOCode != "US" {
		t.Fatalf("8.8.8.8 的国家码 = %q，期望 US（说明加载的是新库而不是残留状态）", info.ISOCode)
	}
	if err := service.Close(); err != nil {
		t.Errorf("Close 失败: %v", err)
	}
}
