package dbcore

import (
	"strings"
	"testing"
)

func TestAssessDataMount(t *testing.T) {
	hex := strings.Repeat("a1", 32)
	line := func(root, target, fs string) string {
		return "42 30 8:1 " + root + " " + target + " rw,relatime - " + fs + " /dev/sda1 rw\n"
	}
	tests := []struct {
		name, mountinfo, target string
		kind                    dataMountKind
		root                    string
	}{
		{"default docker anonymous", line("/var/lib/docker/volumes/"+hex+"/_data", "/app/data", "ext4"), "/app/data", dataMountAnonymousLike, "/var/lib/docker/volumes/" + hex + "/_data"},
		{"custom docker root", line("/srv/docker/volumes/"+hex+"/_data", "/app/data", "ext4"), "/app/data", dataMountAnonymousLike, "/srv/docker/volumes/" + hex + "/_data"},
		{"rootless volume", line("/home/user/.local/share/docker/volumes/"+hex+"/_data", "/app/data", "ext4"), "/app/data", dataMountAnonymousLike, "/home/user/.local/share/docker/volumes/" + hex + "/_data"},
		{"volume subdirectory", line("/srv/docker/volumes/"+hex+"/_data/db", "/app/data", "ext4"), "/app/data", dataMountAnonymousLike, "/srv/docker/volumes/" + hex + "/_data/db"},
		{"named volume", line("/srv/docker/volumes/nekomari-data/_data", "/app/data", "ext4"), "/app/data", dataMountOther, "/srv/docker/volumes/nekomari-data/_data"},
		{"bind mount", line("/opt/nekomari/data", "/app/data", "ext4"), "/app/data", dataMountOther, "/opt/nekomari/data"},
		{"path escaping", line(`/host/my\040data`, `/app/my\040data`, "ext4"), "/app/my data", dataMountOther, "/host/my data"},
		{"backslash escaping", line(`/host/my\134040data`, "/app/data", "ext4"), "/app/data", dataMountOther, `/host/my\040data`},
		{"separate filesystem root", line("/", "/app/data", "tmpfs"), "/app/data", dataMountUncertain, ""},
		{"two overlaid mounts", line("/opt/first", "/app/data", "ext4") + line("/opt/second", "/app/data", "ext4"), "/app/data", dataMountUncertain, ""},
		{"parent mount only", line("/opt/nekomari", "/app", "ext4"), "/app/data", dataMountNotDirect, ""},
		{"unrelated mount", line("/opt/data", "/app/database", "ext4"), "/app/data", dataMountNotDirect, ""},
		{"malformed mount", "42 30 8:1 /foo /app/data rw\n", "/app/data", dataMountNotDirect, ""},
		{"uppercase is not generated docker id", line("/srv/docker/volumes/"+strings.ToUpper(hex)+"/_data", "/app/data", "ext4"), "/app/data", dataMountOther, "/srv/docker/volumes/" + strings.ToUpper(hex) + "/_data"},
		{"ordinary bind containing prefix", line("/srv/docker/volumes/"+hex+"/_database", "/app/data", "ext4"), "/app/data", dataMountOther, "/srv/docker/volumes/" + hex + "/_database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, root := assessDataMount([]byte(tt.mountinfo), tt.target)
			if kind != tt.kind || root != tt.root {
				t.Fatalf("assessDataMount() = (%v, %q), want (%v, %q)", kind, root, tt.kind, tt.root)
			}
		})
	}
}
