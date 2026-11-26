package server

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type startupSignalWriter struct{ sent bool }

func (writer *startupSignalWriter) Write(line []byte) (int, error) {
	if !writer.sent && bytes.Contains(line, []byte("Starting server on")) {
		writer.sent = true
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			return 0, err
		}
	}
	return len(line), nil
}

type idleStartupGuide struct{}

func (idleStartupGuide) Activate()             {}
func (idleStartupGuide) Deactivate()           {}
func (idleStartupGuide) Register(*gin.Engine)  {}
func (idleStartupGuide) Done() <-chan struct{} { return nil }

func TestSIGTERMAtListenerStartupRunsCleanup(t *testing.T) {
	const modeKey = "NEKOMARI_TEST_STARTUP_SIGTERM"
	if mode := os.Getenv(modeKey); mode != "" {
		writer := &startupSignalWriter{}
		slog.SetDefault(slog.New(slog.NewTextHandler(writer, nil)))
		app := New(Options{ListenAddr: "127.0.0.1:0"})
		cleaned := false
		app.addCriticalCleanup("fixture", func(context.Context) error { cleaned = true; return nil })
		switch mode {
		case "panel":
			app.engine = gin.New()
			if err := app.Run(); err != nil {
				t.Fatal(err)
			}
		case "guide":
			completed, err := app.runGuideServer(idleStartupGuide{}, guideServerConfig{logMessage: "Starting server on %s"})
			if completed || err != nil {
				t.Fatalf("guide completed=%v err=%v", completed, err)
			}
		default:
			t.Fatalf("unexpected mode %q", mode)
		}
		if !writer.sent || !cleaned {
			t.Fatalf("signal sent=%v, cleanup ran=%v", writer.sent, cleaned)
		}
		return
	}
	for _, mode := range []string{"panel", "guide"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSIGTERMAtListenerStartupRunsCleanup$")
			child.Env = append(os.Environ(), modeKey+"="+mode)
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("startup shutdown: %v\n%s", err, output)
			}
		})
	}
}
