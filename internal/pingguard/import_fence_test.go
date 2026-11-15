package pingguard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestImportFenceOrdersCreationAndImportBothWays(t *testing.T) {
	for _, creationFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "import-first", true: "creation-first"}[creationFirst], func(t *testing.T) {
			first, second := WithConfigImport, WithTaskCreation
			if creationFirst {
				first, second = second, first
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- first(t.Context(), func() error { close(entered); <-release; return nil })
			}()
			<-entered
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			err := second(ctx, func() error { t.Error("crossed fence"); return nil })
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("contending operation returned %v", err)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := second(t.Context(), func() error { return nil }); err != nil {
				t.Fatalf("fence did not release: %v", err)
			}
		})
	}
}
