package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/pkg/rpc"
	"github.com/Aone2233/nekomari/utils"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/gin-gonic/gin"
)

// Check before executing any third-party code, even when a test is invoked on
// its own rather than after the archive preflight test.
func verifiedMarketPluginArchive(t *testing.T, dir, name string) string {
	t.Helper()
	lockRaw, err := os.ReadFile("testdata/komari-market-20261108.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Items []struct {
			Short string `json:"short"`
			SHA   string `json:"archive_sha256"`
		} `json:"items"`
	}
	if err := json.Unmarshal(lockRaw, &lock); err != nil {
		t.Fatal(err)
	}
	expected := ""
	for _, item := range lock.Items {
		if item.Short == name {
			expected = item.SHA
		}
	}
	path := filepath.Join(dir, name+".zip")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if expected == "" || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
		t.Fatal("plugin archive hash mismatch")
	}
	return path
}

func TestKomariMarketPluginManifests(t *testing.T) {
	files, err := filepath.Glob("testdata/komari-market-20261108/*.json")
	if err != nil || len(files) != 26 {
		t.Fatalf("plugin fixtures: %d, %v", len(files), err)
	}
	lockData, err := os.ReadFile("testdata/komari-market-20261108.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Items []struct {
			Short       string `json:"short"`
			Version     string `json:"version"`
			ManifestSHA string `json:"manifest_sha256"`
		} `json:"items"`
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	indexed := make(map[string]struct{ version, hash string })
	for _, item := range lock.Items {
		indexed[item.Short] = struct{ version, hash string }{item.Version, item.ManifestSHA}
	}
	if len(indexed) != len(files) {
		t.Fatalf("market lock entries = %d, fixtures = %d", len(indexed), len(files))
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var info models.Plugin
			if err := json.Unmarshal(raw, &info); err != nil {
				t.Fatal(err)
			}
			entry, ok := indexed[info.Short]
			digest := fmt.Sprintf("%x", sha256.Sum256(raw))
			if !ok || info.Short+".json" != filepath.Base(file) || info.Version != entry.version || digest != entry.hash {
				t.Fatalf("fixture identity does not match catalog lock: %s", file)
			}
			if err := validateManifest(&info); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Set NEKOMARI_COMPAT_PLUGIN_ZIPS to the directory of hash-verified market
// archives to exercise the real installer without network access in CI.
func TestKomariMarketPluginArchives(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil || len(files) != 26 {
		t.Fatalf("plugin archives: %d, %v", len(files), err)
	}
	withTempDataDir(t)
	oldVersion := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = oldVersion })
	lockData, err := os.ReadFile("testdata/komari-market-20261108.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Items []struct {
			Short      string `json:"short"`
			ArchiveSHA string `json:"archive_sha256"`
		} `json:"items"`
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	archives := make(map[string]string, len(lock.Items))
	for _, item := range lock.Items {
		archives[item.Short] = item.ArchiveSHA
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			short := strings.TrimSuffix(filepath.Base(file), ".zip")
			if expected, ok := archives[short]; !ok || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
				t.Fatalf("archive %s does not match catalog SHA-256", short)
			}
			if _, err := InstallZip(file); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKomariMarketNotificationPluginsLoad(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	for _, name := range []string{"bark-plus", "email-notification-plugin", "gotify-notification-plugin", "serverchan3-notification-plugin", "serverchanturbo-notification-plugin", "telegram-notification-plugin"} {
		t.Run(name, func(t *testing.T) {
			withTempDataDir(t)
			Init(gin.New())
			oldVersion := utils.CurrentVersion
			utils.CurrentVersion = "1.7.30"
			t.Cleanup(func() { utils.CurrentVersion = oldVersion })
			path := filepath.Join(dir, name+".zip")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lockRaw, err := os.ReadFile("testdata/komari-market-20261108.lock.json")
			if err != nil {
				t.Fatal(err)
			}
			var lock struct {
				Items []struct {
					Short      string `json:"short"`
					ArchiveSHA string `json:"archive_sha256"`
				} `json:"items"`
			}
			if err := json.Unmarshal(lockRaw, &lock); err != nil {
				t.Fatal(err)
			}
			expected := ""
			for _, item := range lock.Items {
				if item.Short == name {
					expected = item.ArchiveSHA
				}
			}
			if expected == "" || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
				t.Fatal("archive hash mismatch")
			}
			if _, err := InstallZip(path); err != nil {
				t.Fatal(err)
			}
			if err := SetEnabled(name, true, true); err != nil {
				t.Fatal(err)
			}
			choices := messageSender.PluginChannelChoices()
			if len(choices) != 1 {
				t.Fatalf("registered channels: %#v", choices)
			}
			if err := SetEnabled(name, false, false); err != nil {
				t.Fatal(err)
			}
			if len(messageSender.PluginChannelChoices()) != 0 {
				t.Fatal("channel survived unload")
			}
		})
	}
}

// Execute this test in a disposable, network-isolated and resource-limited
// sandbox: loading a third-party plugin may run arbitrary code.
func TestKomariMarketSandboxedLoad(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	name := os.Getenv("NEKOMARI_COMPAT_PLUGIN_NAME")
	if dir == "" || name == "" {
		t.Skip("market archive and plugin name required")
	}
	lockRaw, err := os.ReadFile("testdata/komari-market-20261108.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Items []struct {
			Short      string `json:"short"`
			ArchiveSHA string `json:"archive_sha256"`
		} `json:"items"`
	}
	if err = json.Unmarshal(lockRaw, &lock); err != nil {
		t.Fatal(err)
	}
	expected := ""
	for _, item := range lock.Items {
		if item.Short == name {
			expected = item.ArchiveSHA
		}
	}
	if expected == "" {
		t.Fatal("plugin absent from lock")
	}
	path := filepath.Join(dir, name+".zip")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
		t.Fatal("archive hash mismatch")
	}
	withTempDataDir(t)
	Init(gin.New())
	// Production registers this system RPC in the web package; provide an
	// empty representative task list in this plugin-only sandbox.
	if name == "komari-gfw" {
		if err := rpc.Register("admin:getAllPingTasks", func(context.Context, *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) { return []any{}, nil }); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rpc.Unregister("admin:getAllPingTasks") })
	}
	oldVersion := utils.CurrentVersion
	utils.CurrentVersion = "1.7.30"
	t.Cleanup(func() { utils.CurrentVersion = oldVersion })
	if _, err = InstallZip(path); err != nil {
		t.Fatal(err)
	}
	if err = SetEnabled(name, true, true); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err = Reload(name); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err = SetEnabled(name, false, false); err != nil {
		t.Fatalf("unload: %v", err)
	}
}
