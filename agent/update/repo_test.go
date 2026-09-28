package update

import (
	"os"
	"path/filepath"
	"testing"
)

// readRepoFile reads a file from the repository root, found by walking up to go.mod.
//
// The workflow file is asserted against rather than the code alone, because the defect this guards against
// was a disagreement between the two — an asset name the code looked for and the release never contained.
func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	// Two go.mod files are above this package: the agent module's and the repository's. The workflow lives in
	// the repository, so the *outermost* one is what is wanted — the first version stopped at the agent's and
	// reported the workflow as missing, which the skip below then turned into a silent pass.
	var outermost string
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			outermost = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if outermost != "" {
		{
			data, err := os.ReadFile(filepath.Join(append([]string{outermost}, parts...)...))
			if err != nil {
				t.Fatalf("read %v: %v", parts, err)
			}
			return string(data)
		}
	}
	t.Fatalf("no go.mod found above the working directory")
	return ""
}