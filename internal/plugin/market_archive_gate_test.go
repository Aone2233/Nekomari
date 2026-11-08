package plugin

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/utils"
	"github.com/gin-gonic/gin"
)

// Optional archive gate: point NEKOMARI_COMPAT_PLUGIN_ZIPS at the directory of
// hash-verified market ZIPs. The default CI run stays offline and does not
// execute third-party code.
func TestKomariMarketPluginArchives(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil || len(files) != 26 {
		t.Fatalf("plugin archives: %d, %v", len(files), err)
	}
	lock := readMarketLock(t)
	archives := make(map[string]string, len(lock.Items))
	for _, item := range lock.Items {
		archives[item.Short] = item.ArchiveSHA
	}
	withTempDataDir(t)
	oldVersion := utils.CurrentVersion
	utils.CurrentVersion = "1.8.0"
	t.Cleanup(func() { utils.CurrentVersion = oldVersion })
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

// Load/unload executes third-party code, so it is opt-in even when ZIPs are
// supplied. Set NEKOMARI_COMPAT_ALLOW_PLUGIN_EXEC=1 only in a disposable,
// network-isolated sandbox.
func TestKomariMarketPluginLoadUnload(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_PLUGIN_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	if os.Getenv("NEKOMARI_COMPAT_ALLOW_PLUGIN_EXEC") != "1" {
		t.Skip("set NEKOMARI_COMPAT_ALLOW_PLUGIN_EXEC=1 in a disposable sandbox")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil || len(files) != 26 {
		t.Fatalf("plugin archives: %d, %v", len(files), err)
	}
	lock := readMarketLock(t)
	archives := make(map[string]string, len(lock.Items))
	for _, item := range lock.Items {
		archives[item.Short] = item.ArchiveSHA
	}
	for _, file := range files {
		short := strings.TrimSuffix(filepath.Base(file), ".zip")
		t.Run(short, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if expected, ok := archives[short]; !ok || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
				t.Fatalf("archive %s does not match catalog SHA-256", short)
			}
			if short == "komari-gfw" {
				t.Skip("known fixture limitation: the plugin rejects the synthetic domain at load")
			}
			withTempDataDir(t)
			Init(gin.New())
			oldVersion := utils.CurrentVersion
			utils.CurrentVersion = "1.8.0"
			t.Cleanup(func() { utils.CurrentVersion = oldVersion })
			if _, err := InstallZip(file); err != nil {
				t.Fatal(err)
			}
			if err := SetEnabled(short, true, true); err != nil {
				t.Fatalf("load: %v", err)
			}
			if err := SetEnabled(short, false, false); err != nil {
				t.Fatalf("unload: %v", err)
			}
		})
	}
}
