package admin

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/database/models"
)

func TestKomariMarketThemeManifests(t *testing.T) {
	files, err := filepath.Glob("testdata/komari-market-20261108/*.json")
	if err != nil || len(files) != 64 {
		t.Fatalf("theme fixtures: %d, %v", len(files), err)
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
			var info models.Theme
			if err := json.Unmarshal(raw, &info); err != nil {
				t.Fatal(err)
			}
			entry, ok := indexed[info.Short]
			digest := fmt.Sprintf("%x", sha256.Sum256(raw))
			if !ok || info.Short+".json" != filepath.Base(file) || info.Version != entry.version || digest != entry.hash {
				t.Fatalf("fixture identity does not match catalog lock: %s", file)
			}
			if err := validateThemeManifest(info); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Supply only archives already checked against the catalog SHA-256.
func TestKomariMarketThemeArchives(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_THEME_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil || len(files) != 64 {
		t.Fatalf("theme archives: %d, %v", len(files), err)
	}
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
			if _, err := peekThemeFromZip(file); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// This optional gate installs every hash-verified theme archive in its own
// temporary panel directory, twice (install + reinstall).
func TestKomariMarketThemeInstallAll(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_THEME_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.zip"))
	if err != nil || len(files) != 64 {
		t.Fatalf("theme archives: %d, %v", len(files), err)
	}
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
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			short := strings.TrimSuffix(filepath.Base(file), ".zip")
			if expected, ok := archives[short]; !ok || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
				t.Fatalf("archive %s does not match catalog SHA-256", short)
			}
			tmp := t.TempDir()
			if err := os.Chdir(tmp); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(old) })
			if _, err := extractAndValidateTheme(file); err != nil {
				t.Fatalf("install: %v", err)
			}
			if _, err := extractAndValidateTheme(file); err != nil {
				t.Fatalf("reinstall: %v", err)
			}
		})
	}
}

// This optional test installs each hash-verified archive in a temporary panel
// directory, including archives without Unix mode metadata.
func TestKomariMarketThemeRealInstall(t *testing.T) {
	dir := os.Getenv("NEKOMARI_COMPAT_THEME_ZIPS")
	if dir == "" {
		t.Skip("optional market archives not supplied")
	}
	name := os.Getenv("NEKOMARI_COMPAT_THEME_NAME")
	if name == "" {
		t.Skip("market theme name not supplied")
	}
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
	if err = json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	expected := ""
	for _, item := range lock.Items {
		if item.Short == name {
			expected = item.ArchiveSHA
		}
	}
	path := filepath.Join(dir, name+".zip")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if expected == "" || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
		t.Fatal("archive hash mismatch")
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if _, err = extractAndValidateTheme(path); err != nil {
		t.Fatal(err)
	}
	if _, err = extractAndValidateTheme(path); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
}
