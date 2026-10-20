package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Aone2233/nekomari/agent/update"
)

// --version must work before the config file, environment and token are read: it is how an
// operator (or this repository's rollout scripts) checks which binary is actually running.
// It used to be an unknown flag, so every upgrade check had to parse the journal instead.
func TestVersionFlagPrintsWithoutStartingTheAgent(t *testing.T) {
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()

	RootCmd.SetArgs([]string{"--version"})
	defer func() {
		RootCmd.SetArgs(nil)
		printVersion = false
	}()
	runErr := RootCmd.Execute()

	_ = writer.Close()
	out, _ := io.ReadAll(reader)
	if runErr != nil {
		t.Fatalf("--version returned %v", runErr)
	}
	if got := strings.TrimSpace(string(out)); got != update.CurrentVersion {
		t.Fatalf("--version printed %q, want %q", got, update.CurrentVersion)
	}
}
