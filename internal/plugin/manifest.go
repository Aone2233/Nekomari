package plugin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Aone2233/nekomari/database/models"
)

// urlSchemeRE matches a leading URL scheme such as "http:" or "javascript:",
// mirroring the theme redirect validation.
var urlSchemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+\-.]*:`)

// readManifest loads and validates the manifest of an installed plugin
// directory.
func readManifest(dir string) (models.Plugin, error) {
	var info models.Plugin
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return info, fmt.Errorf("read plugin manifest: %w", err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("parse plugin manifest: %w", err)
	}
	if err := validateManifest(&info); err != nil {
		return info, err
	}
	return info, nil
}

// validateManifest checks the fields the server relies on and fills in
// defaults.
// validPluginIcon accepts either a plugin-local path or an absolute http(s)
// icon URL. The URL form is already used by market plugins and previously
// slipped through on Unix; validating it explicitly keeps Windows and Linux
// behavior identical.
func validPluginIcon(icon string) bool {
	icon = strings.TrimSpace(icon)
	if icon == "" {
		return true
	}
	if parsed, err := url.Parse(icon); err == nil && parsed.IsAbs() {
		return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
	}
	return filepath.IsLocal(icon)
}

func validateManifest(info *models.Plugin) error {
	if info.Short == "" {
		return fmt.Errorf("plugin short is required")
	}
	if !validShort(info.Short) {
		return fmt.Errorf("plugin short %q is invalid: only letters, digits, '_' and '-' are allowed", info.Short)
	}
	if !models.IsLocalizedText(info.Name) {
		return fmt.Errorf("plugin name is required")
	}
	if info.Entry == "" {
		info.Entry = defaultEntry
	}
	if !filepath.IsLocal(info.Entry) {
		return fmt.Errorf("plugin entry %q must be a relative path inside the plugin directory", info.Entry)
	}
	if info.Icon != "" && !validPluginIcon(info.Icon) {
		return fmt.Errorf("plugin icon %q must be a relative path inside the plugin directory or an http(s) URL", info.Icon)
	}
	for i := range info.Pages {
		if err := validatePage(&info.Pages[i]); err != nil {
			return err
		}
	}
	return nil
}

// validatePage checks one declared plugin page. Type and visibility default
// to iframe/admin so existing manifests keep their behavior.
func validatePage(page *models.PluginPage) error {
	if page.Type == "" {
		page.Type = models.PageTypeIframe
	}
	if page.Visibility == "" {
		page.Visibility = models.PageVisibilityAdmin
	}
	switch page.Visibility {
	case models.PageVisibilityAdmin, models.PageVisibilityPublic:
	default:
		return fmt.Errorf("plugin page visibility %q is invalid: use \"admin\" or \"public\"", page.Visibility)
	}
	if page.Icon != "" && !filepath.IsLocal(page.Icon) {
		return fmt.Errorf("plugin page icon %q must be a relative path inside the plugin directory", page.Icon)
	}
	switch page.Type {
	case models.PageTypeIframe:
		if page.File == "" || !filepath.IsLocal(page.File) {
			return fmt.Errorf("plugin iframe page requires a relative file path inside the plugin directory")
		}
	case models.PageTypeRedirect:
		if page.URL == "" || !isSafeInternalPath(page.URL) {
			return fmt.Errorf("plugin redirect page requires an internal site path starting with /")
		}
	default:
		return fmt.Errorf("plugin page type %q is invalid: use \"iframe\" or \"redirect\"", page.Type)
	}
	if !models.IsLocalizedText(page.Title) {
		return fmt.Errorf("plugin page %q requires a title", page.Title)
	}
	return nil
}

// isSafeInternalPath mirrors the theme redirect rule: only same-origin
// relative paths are allowed, no scheme, no backslashes, no traversal.
func isSafeInternalPath(target string) bool {
	target = strings.TrimSpace(target)
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return false
	}
	if strings.Contains(target, "\\") || urlSchemeRE.MatchString(target) {
		return false
	}
	for _, segment := range strings.Split(target, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}
