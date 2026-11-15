package admin

import (
	"archive/zip"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/gin-gonic/gin"
)

func TestListThemesOnlyListsInstalledDirectories(t *testing.T) {
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workDir) })

	for dir, short := range map[string]string{
		"emerald-insights": "emerald-insights",
		".emerald-stage":   "emerald-insights",
		"renamed-copy":     "emerald-insights",
		"SAO-Nekomari":     "SAO-Nekomari",
	} {
		path := filepath.Join("data", "theme", dir)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		manifest, _ := json.Marshal(map[string]string{"name": dir, "short": short})
		if err := os.WriteFile(filepath.Join(path, "komari-theme.json"), manifest, 0644); err != nil {
			t.Fatal(err)
		}
	}

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ListThemes(ctx)
	var result struct {
		Data []models.Theme `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, theme := range result.Data {
		seen[theme.Short]++
	}
	if response.Code != 200 || seen["emerald-insights"] != 1 || seen["SAO-Nekomari"] != 1 || seen["renamed-copy"] != 0 {
		t.Fatalf("unexpected theme list: status=%d shorts=%v", response.Code, seen)
	}
}

// TestIsValidThemeShort_PathTraversal 防止 DeleteTheme/UpdateTheme/SetTheme
// 的路径穿越漏洞：req.Short 直接进入 filepath.Join("./data/theme", short)，
// 若 short 含 "../" 会规范化到 ./data/theme 之外，配合 os.RemoveAll 可删除
// 工作目录外任意目录。isValidMarketShort 必须拒绝所有此类 payload。
func TestIsValidThemeShort_PathTraversal(t *testing.T) {
	// 必须被拒绝：路径穿越 / 绝对路径 / 目录分隔符 / 空值 / default
	deny := []string{
		"",
		"default",
		"..",
		"../",
		"./",
		"../../etc",
		"../..",
		"foo/../bar",
		"/etc/passwd",
		"foo/bar",
		"foo\\bar",
		"a b",
		"a;b",
		"a$(id)",
	}
	for _, in := range deny {
		if isValidMarketShort(in) {
			t.Errorf("isValidMarketShort(%q) = true, want false (路径穿越/非法字符未被拦截)", in)
		}
	}

	// 必须被接受：仅字母数字下划线连字符
	accept := []string{
		"mytheme",
		"my-theme",
		"my_theme",
		"theme123",
		"ABC",
		"a",
	}
	for _, in := range accept {
		if !isValidMarketShort(in) {
			t.Errorf("isValidMarketShort(%q) = false, want true (合法名称被误拒)", in)
		}
	}
}

func TestValidateThemeManifestAcceptsLocalizedMetadata(t *testing.T) {
	theme := models.Theme{
		Name: map[string]any{
			"zh-CN": "配置字段演示主题",
			"en":    "Managed Configuration Demo Theme",
		},
		Short: "managed-config-demo",
		Description: map[string]any{
			"zh-CN": "用于验证全部托管配置字段的测试主题。",
			"en":    "A test theme covering every managed configuration field type.",
		},
		Author: map[string]any{
			"zh-CN": "Komari 团队",
			"en":    "Komari",
		},
	}

	if err := validateThemeManifest(theme); err != nil {
		t.Fatalf("localized theme metadata was rejected: %v", err)
	}
}

func TestPeekThemeFromZipAcceptsLocalizedMetadata(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "localized-theme.zip")
	archive, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	writer := zip.NewWriter(archive)
	manifest, err := writer.Create("komari-theme.json")
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	_, err = manifest.Write([]byte(`{
  "name": {"zh-CN": "本地化主题", "en": "Localized Theme"},
  "short": "localized-theme",
  "description": {"zh-CN": "描述", "en": "Description"},
  "author": {"zh-CN": "作者", "en": "Author"},
  "version": "1.0.0"
}`))
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}

	theme, err := peekThemeFromZip(zipPath)
	if err != nil {
		t.Fatalf("localized theme package was rejected: %v", err)
	}
	if theme.Short != "localized-theme" {
		t.Fatalf("short = %q, want localized-theme", theme.Short)
	}

	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(workDir)
	})

	installed, err := extractAndValidateTheme(zipPath)
	if err != nil {
		t.Fatalf("localized theme upload package was rejected: %v", err)
	}
	if installed.Short != "localized-theme" {
		t.Fatalf("installed short = %q, want localized-theme", installed.Short)
	}
}

// Some marketplace archives have no Unix permission metadata. Their files
// must still be readable and writable by the panel on subsequent installs.
func TestThemeInstallWithoutUnixModes(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "theme.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, value := range map[string]string{"komari-theme.json": `{"name":"Mode test","short":"mode-test","version":"1.0.0"}`, "dist/index.html": "hello"} {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.CreatorVersion = 0
		entry, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if _, err = extractAndValidateTheme(zipPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat("data/theme/mode-test/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0600 != 0600 {
		t.Fatalf("installed file mode %v", info.Mode())
	}
	if _, err = extractAndValidateTheme(zipPath); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
}

func TestThemeInstallFailurePreservesExistingTheme(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.MkdirAll("data/theme/example", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/theme/example/index.html", []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "bad.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, item := range [][2]string{{"komari-theme.json", `{"name":"Example","short":"example"}`}, {"index.html", "replacement"}, {"../escape", "bad"}} {
		entry, err := w.Create(item[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(item[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extractAndValidateTheme(zipPath); err == nil {
		t.Fatal("expected invalid archive to fail")
	}
	data, err := os.ReadFile("data/theme/example/index.html")
	if err != nil || string(data) != "original" {
		t.Fatalf("existing theme changed after failure: %q, %v", data, err)
	}
	entries, err := os.ReadDir("data/theme")
	if err != nil || len(entries) != 1 || entries[0].Name() != "example" {
		t.Fatalf("temporary install files remain: %v, %v", entries, err)
	}
}
