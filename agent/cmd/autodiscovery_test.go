package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withTempIdentityFile 把身份文件的路径指向临时目录。
//
// 这个文件里有节点 token，而它此前没有任何测试——权限一旦回退成 0644，本机任何
// 用户都能读到并冒充该节点。所以下面几条用例盯的就是"写出去的权限"。
func withTempIdentityFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auto-discovery.json")
	saved := autoDiscoveryFilePath
	autoDiscoveryFilePath = func() string { return path }
	t.Cleanup(func() { autoDiscoveryFilePath = saved })
	return path
}

func TestAutoDiscoveryConfigRoundTrips(t *testing.T) {
	withTempIdentityFile(t)

	want := &AutoDiscoveryConfig{UUID: "11111111-2222-3333-4444-555555555555", Token: "abcdefghijklmnopqrstuv"}
	if err := saveAutoDiscoveryConfig(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadAutoDiscoveryConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.UUID != want.UUID || got.Token != want.Token {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestMissingAutoDiscoveryConfigIsNotAnError(t *testing.T) {
	withTempIdentityFile(t)

	got, err := loadAutoDiscoveryConfig()
	if err != nil {
		t.Fatalf("a missing identity file must not be an error: %v", err)
	}
	if got != nil {
		t.Fatalf("a missing identity file must load as nil, got %+v", got)
	}
}

// 写出去的身份文件必须只有属主可读。
//
// 对照：同一个 agent 在读取 --token-file 时会拒绝组/其他可读的文件
// （token.go 的 tokenFileReadableByOthers）。写出去的至少要和读进来的一样严格。
func TestSaveAutoDiscoveryConfigWritesOwnerOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 chmod 只影响只读位，无法表达 0600")
	}
	path := withTempIdentityFile(t)

	if err := saveAutoDiscoveryConfig(&AutoDiscoveryConfig{UUID: "u", Token: "t"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("身份文件权限应为 0600（它含节点 token），实际为 %#o", perm)
	}
}

// 历史版本用 0644 写过这个文件，所以已存在的安装仍可能是全局可读的。
// 读取时应当就地收紧，而不是等下一次重新注册。
func TestLoadAutoDiscoveryConfigTightensLegacyWidePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 chmod 只影响只读位，无法表达 0600")
	}
	path := withTempIdentityFile(t)

	body := []byte("{\n  \"uuid\": \"legacy-uuid\",\n  \"token\": \"legacytoken00000000000\"\n}")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 确认种子确实是宽的（否则这条用例会因错误的原因通过）。
	if info, _ := os.Stat(path); info.Mode().Perm()&0o077 == 0 {
		t.Fatalf("测试前提不成立：种子文件已是 %#o", info.Mode().Perm())
	}

	got, err := loadAutoDiscoveryConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.Token != "legacytoken00000000000" {
		t.Fatalf("宽权限文件仍应能被正确解析，got %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("读取时未收紧历史权限：实际为 %#o，应为 0600", perm)
	}
}

// 已经是 0600 的文件不应被无谓改动（幂等）。
func TestTightenIdentityFilePermissionsIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 chmod 只影响只读位，无法表达 0600")
	}
	path := withTempIdentityFile(t)

	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := tightenIdentityFilePermissions(path); err != nil {
		t.Fatalf("tighten on an already-tight file: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("权限被意外改动为 %#o", info.Mode().Perm())
	}
}
