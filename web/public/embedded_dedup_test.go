package public

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// 归档里 admin/ 与 standalone/admin/ 是同一批文件的两份副本。
//
// script/embed-theme.mjs 把 frontend/dist/standalone/admin 复制到归档根的 admin/（/admin 的字面
// 路径），而 standalone/admin/ 下那一份原样留在归档里（/standalone/admin/... 的字面路径）。
// 实测 248 个文件、8.26 MiB —— 同一个二进制里存了两遍，而它们来自同一个源目录，每次请求都只会
// 用到其中一份。
//
// 去重本身是安全的，危险的是它的失效方式：如果将来两边真的分叉，而服务端仍按「只有一份」的
// 假设回答，被删掉的那份才是新的，客户端拿到的却是旧的 —— 没有任何日志。所以实现只在**逐字节
// 相同**时删，而下面第一条用例把「删掉的正好是相同的那些」和「不同的那些还在」同时钉住。

// TestTheEmbeddedArchiveStoresTheAdminOnlyOnce 检查真实归档：解压后的原始条目与去重后的结果
// 之间，差值必须正好是那些逐字节相同的副本。
func TestTheEmbeddedArchiveStoresTheAdminOnlyOnce(t *testing.T) {
	raw, err := decodeEmbeddedDistEntries(embeddedDistArchive)
	if err != nil {
		t.Fatalf("the embedded archive does not decode: %v", err)
	}
	files, err := decodeEmbeddedDist(embeddedDistArchive)
	if err != nil {
		t.Fatalf("the embedded archive does not load: %v", err)
	}

	// 先在**未去重**的条目里数重复：如果从去重后的结果里推，归档哪天不再包含两份副本时，
	// 这个用例会静默地什么都不检查。
	duplicates := 0
	duplicateBytes := 0
	for name, content := range raw {
		relative, ok := strings.CutPrefix(name, standaloneAdminPrefix)
		if !ok {
			continue
		}
		canonical, exists := raw[AdminDistDir+"/"+relative]
		if !exists || !bytes.Equal(content, canonical) {
			continue
		}
		duplicates++
		duplicateBytes += len(content)
	}
	t.Logf("the archive carries %d byte-identical admin copies (%d bytes); the loaded archive holds %d entries instead of %d",
		duplicates, duplicateBytes, len(files), len(raw))

	if got, want := len(raw)-len(files), duplicates; got != want {
		t.Errorf("dedupe removed %d entries, want exactly the %d byte-identical copies", got, want)
	}

	// 去重之后：任何与 admin/ 逐字节相同的 standalone/admin/ 条目都必须不在了。
	for name, content := range files {
		relative, ok := strings.CutPrefix(name, standaloneAdminPrefix)
		if !ok {
			continue
		}
		canonical, exists := files[AdminDistDir+"/"+relative]
		if exists && bytes.Equal(content, canonical) {
			t.Errorf("%q is still stored a second time: /standalone/admin/... and /admin/... hold the same bytes in memory",
				name)
		}
	}

	// 而 admin/ 那一份一个都不能少：它才是 /admin 与 /admin/* 的字面路径。
	for name := range raw {
		if !strings.HasPrefix(name, AdminDistDir+"/") {
			continue
		}
		if _, ok := files[name]; !ok {
			t.Errorf("%q was dropped from the archive, but /admin serves from that subtree", name)
		}
	}
}

// TestDedupeAdminCopiesOnlyDropsIdenticalCopies 钉住去重的判据本身，包括它必须**拒绝**删的两种情形。
func TestDedupeAdminCopiesOnlyDropsIdenticalCopies(t *testing.T) {
	files := map[string][]byte{
		"index.html": []byte("root document"),

		// 逐字节相同：删掉 standalone 那一份。
		"admin/index.html":                          []byte("admin document"),
		"standalone/admin/index.html":               []byte("admin document"),
		"admin/assets/entry-0badc0de.js":            []byte("console.log(1);"),
		"standalone/admin/assets/entry-0badc0de.js": []byte("console.log(1);"),

		// 内容不同（未来某次构建分叉）：两份都留，错误的那个宁可不服务也不能服务错内容。
		"admin/assets/diverged-0badc0de.js":            []byte("older build"),
		"standalone/admin/assets/diverged-0badc0de.js": []byte("newer build"),

		// 只有 standalone 一份（admin/ 下没有）：没有可回退的对象，必须留下。
		"standalone/admin/assets/only-standalone.js": []byte("only here"),

		// 别的目录不受影响。
		"standalone/sla/sla.js": []byte("sla"),
	}

	dedupeAdminCopies(files)

	for _, dropped := range []string{
		"standalone/admin/index.html",
		"standalone/admin/assets/entry-0badc0de.js",
	} {
		if _, ok := files[dropped]; ok {
			t.Errorf("%q was not deduped although its admin/ twin is byte-identical", dropped)
		}
	}
	for _, kept := range []string{
		"admin/index.html",
		"admin/assets/entry-0badc0de.js",
		"standalone/admin/assets/diverged-0badc0de.js",
		"admin/assets/diverged-0badc0de.js",
		"standalone/admin/assets/only-standalone.js",
		"standalone/sla/sla.js",
		"index.html",
	} {
		if _, ok := files[kept]; !ok {
			t.Errorf("%q was dropped, but it is not an identical copy of admin/%s", kept, strings.TrimPrefix(kept, standaloneAdminPrefix))
		}
	}
	// 分叉的那一对：内容各自还在，谁都没被另一半顶替。
	if got := string(files["standalone/admin/assets/diverged-0badc0de.js"]); got != "newer build" {
		t.Errorf("the diverged standalone copy reads %q, want its own content", got)
	}
	if got := string(files["admin/assets/diverged-0badc0de.js"]); got != "older build" {
		t.Errorf("the diverged admin copy reads %q, want its own content", got)
	}
}

// TestTheStandaloneAdminPathFallsBackToTheSharedCopy 检查去重的另一半：URL 上的
// /standalone/admin/... 仍然可达，且与 /admin/... 返回同一份字节。
//
// 这一条必须走真实路由：去重删掉的是内存里的键，而 openAsset 的回退是让那条 URL 还能工作的
// 唯一原因 —— 只测 map 会得到「条目确实少了」却漏掉「少掉的那条 URL 变成了 404」。
func TestTheStandaloneAdminPathFallsBackToTheSharedCopy(t *testing.T) {
	router := newRouter(t)

	files, err := loadEmbeddedDist()
	if err != nil {
		t.Fatalf("the embedded archive does not load: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("the embedded archive is empty")
	}
	// 未去重的视图：用来确认选中的这个文件**本来**有 standalone/admin/ 这一份
	// （也就是这条用例真的在验证回退，而不是验证一个恰好存在的键）。
	raw, err := decodeEmbeddedDistEntries(embeddedDistArchive)
	if err != nil {
		t.Fatalf("the embedded archive does not decode: %v", err)
	}

	names := make([]string, 0, 64)
	for name := range files {
		if strings.HasPrefix(name, AdminDistDir+"/assets/") && strings.HasSuffix(name, ".js") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var relative string
	for _, name := range names {
		candidate := strings.TrimPrefix(name, AdminDistDir+"/")
		if _, stillSeparate := files[standaloneAdminPrefix+candidate]; stillSeparate {
			continue // 没被去重（内容不同或没有副本），不是这条用例要的样本
		}
		if _, inRawArchive := raw[standaloneAdminPrefix+candidate]; !inRawArchive {
			continue // 归档本来就没有 standalone/admin/ 这一份
		}
		relative = candidate
		break
	}
	if relative == "" {
		t.Fatal("no admin asset was deduped, so the fallback lookup cannot be exercised: " +
			"either the archive changed shape or dedupeAdminCopies stopped running")
	}

	standalone := get(t, router, "/"+standaloneAdminPrefix+relative)
	canonical := get(t, router, "/"+AdminDistDir+"/"+relative)

	if standalone.Code != http.StatusOK {
		t.Fatalf("GET /%s = %d, want 200: deduping the copy must not turn the URL into a 404",
			standaloneAdminPrefix+relative, standalone.Code)
	}
	if canonical.Code != http.StatusOK {
		t.Fatalf("GET /%s = %d, want 200", AdminDistDir+"/"+relative, canonical.Code)
	}
	if !bytes.Equal(standalone.Body.Bytes(), canonical.Body.Bytes()) {
		t.Fatalf("/standalone/admin/ and /admin/ serve different bytes for %q (%d vs %d): the fallback is not reading the shared copy",
			relative, standalone.Body.Len(), canonical.Body.Len())
	}
	if want := files[AdminDistDir+"/"+relative]; !bytes.Equal(standalone.Body.Bytes(), want) {
		t.Errorf("GET /%s served %d bytes that are not the archive's %d",
			standaloneAdminPrefix+relative, standalone.Body.Len(), len(want))
	}
	if got := standalone.Header().Get("Content-Type"); got == "" || strings.Contains(strings.ToLower(got), "html") {
		t.Errorf("GET /%s got Content-Type %q: the fallback must still serve the asset as itself, not as a document",
			standaloneAdminPrefix+relative, got)
	}

	// 回退只对真正存在的文件生效：同一个前缀下不存在的路径仍然是 404，而不是退回一份文档。
	missing := get(t, router, "/"+standaloneAdminPrefix+"assets/does-not-exist-0badc0de.js")
	if missing.Code != http.StatusNotFound {
		t.Errorf("GET a missing /standalone/admin/ asset = %d, want 404", missing.Code)
	}
}
