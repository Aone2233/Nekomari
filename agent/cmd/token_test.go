package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	// WriteFile is subject to umask, so set the mode explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
	return path
}

func TestReadTokenFileAcceptsBareToken(t *testing.T) {
	path := writeTemp(t, "token", "s3cret-token\n", 0o600)
	token, err := readTokenFile(path)
	if err != nil {
		t.Fatalf("readTokenFile: %v", err)
	}
	if token != "s3cret-token" {
		t.Fatalf("token = %q, want %q", token, "s3cret-token")
	}
}

func TestReadTokenFileAcceptsEnvironmentFileFormat(t *testing.T) {
	// One file has to serve both `--token-file` and systemd's
	// `EnvironmentFile=`, or the unit needs the token written twice.
	for _, content := range []string{
		"AGENT_TOKEN=env-token\n",
		"TOKEN=env-token\n",
		"AGENT_TOKEN=\"env-token\"\n",
		"AGENT_TOKEN='env-token'\n",
	} {
		path := writeTemp(t, "token", content, 0o600)
		token, err := readTokenFile(path)
		if err != nil {
			t.Fatalf("readTokenFile(%q): %v", content, err)
		}
		if token != "env-token" {
			t.Fatalf("readTokenFile(%q) = %q, want %q", content, token, "env-token")
		}
	}
}

func TestReadTokenFileIgnoresTrailingContent(t *testing.T) {
	// A file that grew a second line must not produce a token made of both.
	path := writeTemp(t, "token", "first-token\nsecond-token\n", 0o600)
	token, err := readTokenFile(path)
	if err != nil {
		t.Fatalf("readTokenFile: %v", err)
	}
	if token != "first-token" {
		t.Fatalf("token = %q, want %q", token, "first-token")
	}
}

func TestReadTokenFileRejectsEmpty(t *testing.T) {
	for _, content := range []string{"", "\n", "   \n", "AGENT_TOKEN=\n"} {
		path := writeTemp(t, "token", content, 0o600)
		if _, err := readTokenFile(path); err == nil {
			t.Fatalf("readTokenFile(%q) succeeded, want an error", content)
		}
	}
}

func TestReadTokenFileRejectsWorldReadable(t *testing.T) {
	// Fail closed: a 0644 file looks like the exposure was fixed while leaving
	// every local user able to read the token, which is the whole problem.
	// Windows reports 0666 for an ordinary file and has no POSIX bits, so the
	// check is asserted directly there rather than through a file's mode.
	if runtime.GOOS == "windows" {
		if tokenFileReadableByOthers(0o644) {
			t.Fatal("the permission check must be inert on Windows, which has no mode bits")
		}
		t.Skip("Windows does not report POSIX permission bits; the mode check is covered by POSIX CI")
	}
	path := writeTemp(t, "token", "s3cret-token\n", 0o644)
	_, err := readTokenFile(path)
	if err == nil {
		t.Fatal("readTokenFile accepted a world-readable token file")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("error should say how to fix it, got: %v", err)
	}
}

func TestReadTokenFileRejectsGroupReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not report POSIX permission bits")
	}
	path := writeTemp(t, "token", "s3cret-token\n", 0o640)
	if _, err := readTokenFile(path); err == nil {
		t.Fatal("readTokenFile accepted a group-readable token file")
	}
}

func TestReadTokenFileRejectsMissingAndDirectory(t *testing.T) {
	if _, err := readTokenFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("readTokenFile accepted a missing file")
	}
	if _, err := readTokenFile(t.TempDir()); err == nil {
		t.Fatal("readTokenFile accepted a directory")
	}
}

func TestTokenFileFromEnvFile(t *testing.T) {
	path := writeTemp(t, "agent.env", "# comment\n\nAGENT_TOKEN=env-token\nOTHER=1\n", 0o600)
	token, err := tokenFileFromEnvFile(path)
	if err != nil {
		t.Fatalf("tokenFileFromEnvFile: %v", err)
	}
	if token != "env-token" {
		t.Fatalf("token = %q, want %q", token, "env-token")
	}

	empty := writeTemp(t, "empty.env", "# nothing here\n", 0o600)
	if _, err := tokenFileFromEnvFile(empty); err == nil {
		t.Fatal("tokenFileFromEnvFile accepted a file with no AGENT_TOKEN")
	}
}

func TestResolveTokenPrecedence(t *testing.T) {
	// The order is the contract: an operator who set -t or AGENT_TOKEN means it,
	// and the panel's generated install command still uses -t.
	t.Setenv("AGENT_ENV_FILE", "")
	t.Setenv("AGENT_TOKEN_FILE", "")
	t.Setenv("AGENT_TOKEN", "")

	file := writeTemp(t, "token", "from-file\n", 0o600)
	envFile := writeTemp(t, "agent.env", "AGENT_TOKEN=from-env-file\n", 0o600)

	saved := *flags
	t.Cleanup(func() { *flags = saved })

	// 1. An explicit -t / AGENT_TOKEN wins over every file.
	*flags = saved
	flags.Token = "from-flag"
	flags.TokenFile = file
	t.Setenv("AGENT_ENV_FILE", envFile)
	if err := resolveToken(); err != nil {
		t.Fatalf("resolveToken: %v", err)
	}
	if flags.Token != "from-flag" {
		t.Fatalf("token = %q, want the explicit one", flags.Token)
	}

	// 2. EnvironmentFile wins over --token-file, so a unit can set the token
	//    with no arguments at all.
	*flags = saved
	flags.TokenFile = file
	t.Setenv("AGENT_ENV_FILE", envFile)
	if err := resolveToken(); err != nil {
		t.Fatalf("resolveToken: %v", err)
	}
	if flags.Token != "from-env-file" {
		t.Fatalf("token = %q, want %q", flags.Token, "from-env-file")
	}

	// 3. --token-file.
	*flags = saved
	flags.TokenFile = file
	t.Setenv("AGENT_ENV_FILE", "")
	if err := resolveToken(); err != nil {
		t.Fatalf("resolveToken: %v", err)
	}
	if flags.Token != "from-file" {
		t.Fatalf("token = %q, want %q", flags.Token, "from-file")
	}

	// 4. AGENT_TOKEN_FILE when no flag was given.
	*flags = saved
	t.Setenv("AGENT_TOKEN_FILE", file)
	if err := resolveToken(); err != nil {
		t.Fatalf("resolveToken: %v", err)
	}
	if flags.Token != "from-file" {
		t.Fatalf("token = %q, want %q", flags.Token, "from-file")
	}

	// 5. Nothing configured: leave the token empty rather than failing, because
	//    --auto-discovery is a legitimate way to start with no token.
	*flags = saved
	t.Setenv("AGENT_TOKEN_FILE", "")
	if err := resolveToken(); err != nil {
		t.Fatalf("resolveToken with no source: %v", err)
	}
	if flags.Token != "" {
		t.Fatalf("token = %q, want empty", flags.Token)
	}

	// 6. A configured-but-broken file is an error, not a silent fallback: a node
	//    that starts with no identity reports nothing and looks like an outage.
	*flags = saved
	flags.TokenFile = filepath.Join(t.TempDir(), "absent")
	if err := resolveToken(); err == nil {
		t.Fatal("resolveToken ignored an unreadable token file")
	}
}

func TestCommandLineTokenSpellings(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"short separate", []string{"komari-agent", "-t", "abc"}, "abc", true},
		{"short equals", []string{"komari-agent", "-t=abc"}, "abc", true},
		{"long separate", []string{"komari-agent", "--token", "abc"}, "abc", true},
		{"long equals", []string{"komari-agent", "--token=abc"}, "abc", true},
		{"absent", []string{"komari-agent", "-e", "http://panel"}, "", false},
		{"token file only", []string{"komari-agent", "--token-file", "/etc/agent-token"}, "", false},
		{"dangling", []string{"komari-agent", "-t"}, "", false},
		{"other value not mistaken", []string{"komari-agent", "-e", "http://panel", "-i", "5"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := commandLineToken(tc.args)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("commandLineToken(%v) = (%q, %v), want (%q, %v)", tc.args, got, ok, tc.want, tc.ok)
			}
		})
	}
}
