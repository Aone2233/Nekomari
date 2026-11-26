package monitoring

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	pkg_flags "github.com/Aone2233/nekomari/agent/cmd/flags"
)

// BootIDHash returns a hash of the Linux boot id, or an empty string when it is
// unavailable. The raw id is never sent: it is a stable fingerprint that has no
// monitoring value beyond change detection.
func BootIDHash() string {
	root := pkg_flags.GlobalConfig.HostProc
	if root == "" {
		root = "/proc"
	}
	data, err := os.ReadFile(filepath.Join(root, "sys", "kernel", "random", "boot_id"))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if id == "" || len(id) > 128 {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}
