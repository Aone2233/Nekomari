package panelhistory

import (
	"context"
	"testing"
)

func TestSamplerSkipsOverlapAndHonorsCancellation(t *testing.T) {
	var sampler Sampler
	sampler.mu.Lock()
	if err := sampler.Collect(context.Background()); err != nil {
		t.Fatalf("overlapping sample: %v", err)
	}
	sampler.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sampler.Collect(ctx); err != context.Canceled {
		t.Fatalf("cancelled sample: %v", err)
	}
}
