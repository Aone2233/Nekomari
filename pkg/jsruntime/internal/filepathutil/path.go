package filepathutil

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// RelativeToBase resolves name against baseDir and returns the path relative to
// baseDir, rejecting anything that escapes it.
func RelativeToBase(baseDir, name string) (string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if lexicalWithin(baseDir, absolute) {
		return relativeWithin(baseDir, absolute)
	}
	// Windows can name one directory two ways: an 8.3 short name (RUNNER~1) and
	// its long form. BaseDir is resolved with EvalSymlinks when the runtime is
	// built, so a caller that passes the short form would otherwise look like it
	// escapes. Re-check with both resolved; this runs only on the mismatch path.
	if resolvedBase, resolvedPath, ok := resolveShortNames(baseDir, absolute); ok && lexicalWithin(resolvedBase, resolvedPath) {
		return relativeWithin(resolvedBase, resolvedPath)
	}
	return "", fmt.Errorf("JavaScript path escapes BaseDir: %s", name)
}

// WithinBase reports whether path stays inside baseDir.
func WithinBase(baseDir, path string) bool {
	if lexicalWithin(baseDir, path) {
		return true
	}
	if resolvedBase, resolvedPath, ok := resolveShortNames(baseDir, path); ok {
		return lexicalWithin(resolvedBase, resolvedPath)
	}
	return false
}

func relativeWithin(baseDir, path string) (string, error) {
	relative, err := filepath.Rel(baseDir, path)
	if err != nil {
		return "", fmt.Errorf("make JavaScript path relative to BaseDir: %w", err)
	}
	if relative == "." {
		return ".", nil
	}
	return relative, nil
}

func lexicalWithin(baseDir, path string) bool {
	relative, err := filepath.Rel(baseDir, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// resolveShortNames resolves both paths with EvalSymlinks, which on Windows also
// expands 8.3 short names. It is a no-op elsewhere, where the lexical comparison
// is already exact.
func resolveShortNames(baseDir, path string) (string, string, bool) {
	if runtime.GOOS != "windows" {
		return "", "", false
	}
	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return "", "", false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", false
	}
	return resolvedBase, resolvedPath, true
}
