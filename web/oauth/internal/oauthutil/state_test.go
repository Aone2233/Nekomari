package oauthutil

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStateConsumedOnceUnderConcurrency(t *testing.T) {
	var states States
	if !states.Add("nonce") {
		t.Fatal("admission failed")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if states.Consume("nonce") {
				successes.Add(1)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("accepted %d callbacks", successes.Load())
	}
}

func TestStateExpiryCapacityAndClear(t *testing.T) {
	var states States
	states.entries = map[string]time.Time{"expired": time.Now().Add(-time.Second)}
	if states.Consume("expired") {
		t.Fatal("expired state accepted")
	}
	for i := range maxStates {
		if !states.Add(fmt.Sprint(i)) {
			t.Fatalf("admission %d failed", i)
		}
	}
	if states.Add("overflow") {
		t.Fatal("capacity exceeded")
	}
	states.entries["0"] = time.Now().Add(-time.Second)
	if !states.Add("replacement") {
		t.Fatal("expired entry did not free capacity")
	}
	states.Clear()
	if states.Consume("replacement") || !states.Add("new") {
		t.Fatal("clear failed")
	}
}
