package pingguard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultRosterOrdersBothOperationDirections(t *testing.T) {
	for _, first := range []string{"node", "task"} {
		t.Run(first, func(t *testing.T) {
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- WithDefaultRoster(t.Context(), func() error { close(entered); <-release; return nil })
			}()
			<-entered
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			err := WithDefaultRoster(ctx, func() error { t.Error("roster operations overlapped"); return nil })
			cancel()
			close(release)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("contending roster error=%v", err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
