package public

import "testing"

// manifestFixture 是一段真实的 Vite 清单片段（键是模块 id，路径在 "file" 里）。
// 注意 index-Kbf1m-l1.js 的哈希含 '-' —— 这正是「按文件名猜」会判错的地方。
const manifestFixture = `{
  "index.html": { "file": "assets/entry-index-E1qL84zG.js", "isEntry": true },
  "src/main.tsx": { "file": "assets/index-Kbf1m-l1.js" },
  "src/x.css": { "file": "assets/index-DYCkzwy-.css" },
  "_chunk-layout.js": { "file": "assets/chunk-_layout-Um_EzOCS.js" },
  "src/rolldown.ts": { "file": "assets/rolldown-runtime-Cyuzqnbw.js" }
}`

// TestHashedAssetsFromManifest 钉住解析：取的是 "file" 字段（产物路径），
// 不是清单的键（模块 id）。
func TestHashedAssetsFromManifest(t *testing.T) {
	assets := hashedAssetsFromManifest([]byte(manifestFixture))
	if len(assets) != 5 {
		t.Fatalf("expected 5 assets, got %d: %#v", len(assets), assets)
	}
	for _, want := range []string{
		"assets/entry-index-E1qL84zG.js",
		"assets/index-Kbf1m-l1.js",
		"assets/index-DYCkzwy-.css",
		"assets/chunk-_layout-Um_EzOCS.js",
		"assets/rolldown-runtime-Cyuzqnbw.js",
	} {
		if _, ok := assets[want]; !ok {
			t.Errorf("manifest asset %q missing from the set", want)
		}
	}
	// 键（模块 id）不能进集合，否则会把 "index.html" 之类当成可长期缓存。
	if _, ok := assets["index.html"]; ok {
		t.Error("the manifest key leaked into the set")
	}
}

func TestHashedAssetsFromManifestRejectsBadInput(t *testing.T) {
	if got := hashedAssetsFromManifest([]byte("not json")); got != nil {
		t.Errorf("malformed manifest should yield nil, got %#v", got)
	}
	if got := hashedAssetsFromManifest([]byte("{}")); got != nil {
		t.Errorf("empty manifest should yield nil, got %#v", got)
	}
	if got := hashedAssetsFromManifest(nil); got != nil {
		t.Errorf("nil input should yield nil, got %#v", got)
	}
}

// TestIsHashedAsset 钉住查找：调用方传的是相对主题根的路径（"dist/assets/a.js"），
// 而清单里的路径是相对 dist 的。
func TestIsHashedAsset(t *testing.T) {
	assets := hashedAssetsFromManifest([]byte(manifestFixture))

	cases := []struct {
		name string
		want bool
	}{
		{"dist/assets/index-Kbf1m-l1.js", true},
		{"dist/assets/index-DYCkzwy-.css", true},
		{"/dist/assets/entry-index-E1qL84zG.js", true},

		// 真实产物里没有哈希的两个 —— 不在清单里，必须 false
		{"dist/assets/pwa-icon.webp", false},
		{"dist/assets/edit_117847723_p0.webp", false},

		// 必须能立刻更新的东西
		{"dist/index.html", false},
		{"dist/sw.js", false},
		{"dist/registerSW.js", false},
		{"dist/manifest.json", false},
		{"dist/US.svg", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isHashedAsset(tc.name, assets); got != tc.want {
			t.Errorf("isHashedAsset(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestIsHashedAssetWithoutManifest 钉住回退：第三方主题可能没有清单，
// 此时必须【什么都不缓存】，而不是猜。少缓存是安全的，缓存错不是。
func TestIsHashedAssetWithoutManifest(t *testing.T) {
	for _, assets := range []map[string]struct{}{nil, {}} {
		if isHashedAsset("dist/assets/index-Kbf1m-l1.js", assets) {
			t.Error("without a manifest nothing may be treated as a hashed asset")
		}
	}
}
