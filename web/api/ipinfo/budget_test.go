package ipinfo

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
)

func TestConcurrentLookupsShareUpstreamWork(t *testing.T) {
	h, upstream, _ := newTestHandler(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, _, err := h.svc.Lookup(context.Background(), net.ParseIP("8.8.8.8"), false); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	total := upstream.hitsFor("geo") + upstream.hitsFor("ripe") + upstream.hitsFor("ipapi")
	if total != 4 {
		t.Fatalf("8 same-IP lookups made %d provider requests, want 4", total)
	}
}

func TestLookupCapacityRejectsWithoutUpstreamWork(t *testing.T) {
	h, upstream, _ := newTestHandler(t)
	for i := 0; i < cap(h.svc.lookupSlots); i++ {
		h.svc.lookupSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(h.svc.lookupSlots); i++ {
			<-h.svc.lookupSlots
		}
	}()
	if _, _, err := h.svc.Lookup(context.Background(), net.ParseIP("8.8.8.8"), false); !errors.Is(err, ErrBusy) {
		t.Fatalf("capacity error: %v", err)
	}
	if upstream.hitsFor("geo") != 0 {
		t.Fatal("busy lookup contacted upstream")
	}
}
