package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// The token is the node's whole identity: whoever holds it can report as this
// node and open its terminal. Passing it as `-t <token>` puts it in the process
// command line, which on Linux means /proc/<pid>/cmdline and
// `systemctl show -p ExecStart` — readable by every local user on the host, not
// just root. `--token-file` is the way to keep it out of both.
//
// The file holds the bare token and nothing else, so it can also be consumed by
// systemd as an EnvironmentFile (`AGENT_TOKEN=...`) without a second format.
// See docs/SECRETS.md.

// tokenFileReadableByOthers reports whether the file's mode lets group or other
// read it.
//
// Only meaningful on POSIX. Windows has no mode bits — os.Chmod there toggles
// the read-only attribute and Stat reports 0666 for an ordinary writable file —
// so the check would reject every token file on Windows instead of protecting
// anything. The fleet this matters for is Linux, and there the check is the
// difference between a fix and the appearance of one.
func tokenFileReadableByOthers(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return mode.Perm()&0o077 != 0
}

// readTokenFile returns the token stored in path.
//
// It refuses a file that group or other can read. That is a deliberate
// fail-closed choice: the whole point of moving the token out of the command
// line is that other local users cannot read it, and a 0644 file is a silent
// way to lose that property while looking like it was fixed.
func readTokenFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("token file %s is a directory", path)
	}
	if tokenFileReadableByOthers(info.Mode()) {
		return "", fmt.Errorf(
			"token file %s is readable by other users (mode %04o); run `chmod 600 %s`",
			path, info.Mode().Perm(), path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	}
	return parseTokenFileContent(string(raw))
}

// parseTokenFileContent accepts either the bare token or a systemd-style
// `AGENT_TOKEN=<token>` line, so one file can serve both the agent and an
// EnvironmentFile drop-in.
func parseTokenFileContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errors.New("token file is empty")
	}

	// Only the first non-empty line counts; a trailing newline is expected and
	// anything after it is ignored rather than silently concatenated.
	if idx := strings.IndexAny(content, "\r\n"); idx >= 0 {
		content = strings.TrimSpace(content[:idx])
	}
	for _, key := range []string{"AGENT_TOKEN=", "TOKEN="} {
		if strings.HasPrefix(content, key) {
			content = strings.TrimSpace(strings.TrimPrefix(content, key))
			break
		}
	}
	content = strings.Trim(content, `"'`)
	if content == "" {
		return "", errors.New("token file has no token")
	}
	return content, nil
}

// tokenFileFromEnvFile reads the token out of a systemd EnvironmentFile. It
// exists so the unit can carry `EnvironmentFile=` instead of `--token-file`:
// either route keeps the token off the command line, and which one a host uses
// depends on whether it is systemd, OpenRC, a container or launchd.
func tokenFileFromEnvFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read env file: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, key := range []string{"AGENT_TOKEN=", "TOKEN="} {
			if strings.HasPrefix(line, key) {
				value := strings.TrimSpace(strings.TrimPrefix(line, key))
				value = strings.Trim(value, `"'`)
				if value == "" {
					continue
				}
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("no AGENT_TOKEN in env file %s", path)
}

// resolveToken fills flags.Token from whichever source the operator chose, in
// this order: an explicit --token-file, the AGENT_TOKEN_FILE environment
// variable, then AGENT_TOKEN in a systemd EnvironmentFile.
//
// It never overwrites a token that is already set. `-t` and `AGENT_TOKEN` still
// work and still win, because an operator who set one of those means it, and
// because the panel's generated install command uses `-t` — breaking that would
// trade one security problem for a fleet that cannot be installed.
func resolveToken() error {
	if flags.Token != "" {
		return nil
	}

	// An EnvironmentFile is read before the file flags so that a unit using
	// `EnvironmentFile=` works with no extra arguments at all.
	if envFile := os.Getenv("AGENT_ENV_FILE"); envFile != "" {
		token, err := tokenFileFromEnvFile(envFile)
		if err != nil {
			return err
		}
		flags.Token = token
		return nil
	}

	path := flags.TokenFile
	if path == "" {
		path = os.Getenv("AGENT_TOKEN_FILE")
	}
	if path == "" {
		return nil
	}

	token, err := readTokenFile(path)
	if err != nil {
		return err
	}
	flags.Token = token
	return nil
}
