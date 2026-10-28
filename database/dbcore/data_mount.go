package dbcore

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	logger "github.com/Aone2233/nekomari/utils/log"
)

type dataMountKind int

const (
	dataMountNotDirect dataMountKind = iota
	dataMountUncertain
	dataMountAnonymousLike
	dataMountOther
)

var anonymousVolumeRoot = regexp.MustCompile(`(?:^|/)volumes/[0-9a-f]{64}/_data(?:/|$)`)

// mountinfo escapes whitespace and backslashes in path fields.
var unescapeMountPath = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

// assessDataMount cannot prove persistence or whether a volume name was generated.
func assessDataMount(mountinfo []byte, dataDir string) (dataMountKind, string) {
	var root string
	matches := 0
	for _, line := range strings.Split(string(mountinfo), "\n") {
		fields := strings.Fields(line)
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(fields) < separator+4 || unescapeMountPath.Replace(fields[4]) != dataDir {
			continue
		}
		root = unescapeMountPath.Replace(fields[3])
		matches++
	}
	if matches == 0 {
		return dataMountNotDirect, ""
	}
	if matches != 1 || root == "" || root == "/" {
		return dataMountUncertain, ""
	}
	if anonymousVolumeRoot.MatchString(root) {
		return dataMountAnonymousLike, root
	}
	return dataMountOther, root
}

// The container's mountinfo is only a hint: runtime configuration determines
// whether a mount is reused when a container is recreated.
func warnIfDataNotPersistent() {
	if !runningInContainer() {
		return
	}
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return
	}
	dataDir, err := filepath.Abs("./data")
	if err != nil {
		return
	}

	kind, root := assessDataMount(raw, dataDir)
	switch kind {
	case dataMountNotDirect:
		logger.Warnf("dbcore", "data directory %q is not a separate mount point; it may be in the container's writable layer. "+
			"An ancestor mount may also cover it. Check the container's mount configuration and use a reusable named volume or bind mount before recreating it.", dataDir)
	case dataMountUncertain:
		logger.Warnf("dbcore", "data directory %q has an ambiguous mount; container mount information cannot establish whether it is reused. "+
			"Check the container's mount configuration before recreating it.", dataDir)
	case dataMountAnonymousLike:
		logger.Warnf("dbcore", "data directory %q has a mount root resembling an automatically named volume (%q). "+
			"An anonymous volume can be orphaned on container recreation; a deliberately named volume or bind mount can look similar. "+
			"Check the container's mount configuration and ensure its data is retained before upgrading.", dataDir, root)
	case dataMountOther:
		logger.Infof("dbcore", "data directory %q has a mount root %q; check the container's mount configuration to ensure it is reused when recreated.", dataDir, root)
	}
}
