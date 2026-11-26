package plugin

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/utils"
)

type marketLock struct {
	CatalogUpdatedAt string `json:"catalog_updated_at"`
	Items            []struct {
		Short       string `json:"short"`
		Version     string `json:"version"`
		ArchiveSHA  string `json:"archive_sha256"`
		ManifestSHA string `json:"manifest_sha256"`
	} `json:"items"`
}

func readMarketLock(t *testing.T) marketLock {
	t.Helper()
	raw, err := os.ReadFile("testdata/komari-market-20261108.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock marketLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	return lock
}

// TestKomariMarketPluginManifests pins the verified 2026-11-08 marketplace
// baseline. It is offline: the fixtures are the exact root manifests extracted
// from catalog-hash-verified ZIPs, not downloaded extension code.
func TestKomariMarketPluginManifests(t *testing.T) {
	files, err := filepath.Glob("testdata/komari-market-20261108/*.json")
	if err != nil || len(files) != 26 {
		t.Fatalf("plugin fixtures: %d, %v", len(files), err)
	}
	lock := readMarketLock(t)
	indexed := make(map[string]struct{ version, hash string }, len(lock.Items))
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

// TestKomariMarketPluginVersionGate checks the pinned baseline against the
// version the v1.8 branch will ship. A suffixed version must still be refused
// rather than silently disabling the gate.
func TestKomariMarketPluginVersionGate(t *testing.T) {
	files, err := filepath.Glob("testdata/komari-market-20261108/*.json")
	if err != nil || len(files) == 0 {
		t.Fatal(err)
	}
	old := utils.CurrentVersion
	utils.CurrentVersion = "1.8.0"
	t.Cleanup(func() { utils.CurrentVersion = old })

	constrained := 0
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var info models.Plugin
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatal(err)
		}
		if info.Komari == "" {
			continue
		}
		constrained++
		if err := CheckKomariVersion(info.Komari); err != nil {
			t.Errorf("%s requires %s: %v", info.Short, info.Komari, err)
		}
	}
	if constrained == 0 {
		t.Fatal("no constrained plugin fixtures; the gate would be vacuous")
	}

	utils.CurrentVersion = "1.8.0-rc1"
	if err := CheckKomariVersion(">=1.6.0"); err == nil {
		t.Fatal("a suffixed version silently satisfied a plugin constraint")
	}
}
