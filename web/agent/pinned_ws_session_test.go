package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/web/connection"
)

const (
	pinnedTestInstanceA = "0123456789abcdef0123456789abcdef"
	pinnedTestInstanceB = "fedcba9876543210fedcba9876543210"
)

func TestPinnedWSHandshakeFencesReplacementAndExpiry(t *testing.T) {
	const uuid = "pinned-session-test"
	a, b := &connection.SafeConn{}, &connection.SafeConn{}
	t.Cleanup(func() { ResetClient(uuid) })
	now := time.Now()
	if _, ok := beginPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, 1, func() time.Time { return now }); ok {
		t.Fatal("unregistered socket received a challenge")
	}
	SetConnectedClients(uuid, a)
	for _, instance := range []string{"", "ABCDEF0123456789abcdef0123456789", strings.Repeat("g", 32), strings.Repeat("a", 33)} {
		if _, ok := beginPinnedWSHandshakeAt(uuid, a, instance, 1, func() time.Time { return now }); ok {
			t.Fatalf("accepted invalid instance %q", instance)
		}
	}
	if _, ok := beginPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, 2, func() time.Time { return now }); ok {
		t.Fatal("accepted unsupported capability version")
	}
	challengeA, ok := beginPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, 1, func() time.Time { return now })
	if !ok || !validPinnedWSID(challengeA) || PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now) {
		t.Fatal("challenge did not start inactive")
	}
	if ackPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, strings.Repeat("0", 32), func() time.Time { return now }) || ackPinnedWSHandshakeAt(uuid, b, pinnedTestInstanceA, challengeA, func() time.Time { return now }) {
		t.Fatal("wrong challenge/socket activated capability")
	}
	if !ackPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, challengeA, func() time.Time { return now }) || !PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now) {
		t.Fatal("current socket could not activate capability")
	}
	if PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now.Add(pinnedWSLease)) || ackPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, challengeA, func() time.Time { return now.Add(pinnedWSLease) }) {
		t.Fatal("expired lease remained usable or was revived")
	}
	challengeA, ok = beginPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, 1, func() time.Time { return now })
	if !ok || !ackPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, challengeA, func() time.Time { return now }) {
		t.Fatal("fresh handshake failed")
	}
	SetConnectedClients(uuid, b)
	if PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now) || ackPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, challengeA, func() time.Time { return now }) {
		t.Fatal("replaced socket retained capability")
	}
	challengeB, ok := beginPinnedWSHandshakeAt(uuid, b, pinnedTestInstanceB, 1, func() time.Time { return now })
	if !ok || !ackPinnedWSHandshakeAt(uuid, b, pinnedTestInstanceB, challengeB, func() time.Time { return now }) {
		t.Fatal("replacement handshake failed")
	}
	DeleteClientConditionally(uuid, a)
	if !PinnedWSSessionCurrent(uuid, b, pinnedTestInstanceB, challengeB, now) {
		t.Fatal("old disconnect retired replacement")
	}
	DeleteClientConditionally(uuid, b)
	if PinnedWSSessionCurrent(uuid, b, pinnedTestInstanceB, challengeB, now) {
		t.Fatal("disconnect retained session")
	}
}

func TestPinnedWSSessionRotationResetAndRenewal(t *testing.T) {
	const uuid = "pinned-session-reset"
	conn := &connection.SafeConn{}
	t.Cleanup(func() { ResetClient(uuid) })
	now := time.Now()
	SetConnectedClients(uuid, conn)
	first, ok := beginPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, 1, func() time.Time { return now })
	if !ok || !ackPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, first, func() time.Time { return now }) {
		t.Fatal("first handshake failed")
	}
	second, ok := beginPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, 1, func() time.Time { return now.Add(time.Second) })
	if !ok || first == second || ackPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, first, func() time.Time { return now.Add(time.Second) }) {
		t.Fatal("rehandshake accepted stale nonce")
	}
	if !ackPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, second, func() time.Time { return now.Add(time.Second) }) ||
		!ackPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, second, func() time.Time { return now.Add(pinnedWSLease - time.Second) }) ||
		!PinnedWSSessionCurrent(uuid, conn, pinnedTestInstanceA, second, now.Add(pinnedWSLease+time.Second)) {
		t.Fatal("current lease failed to renew")
	}
	ResetClient(uuid)
	if PinnedWSSessionCurrent(uuid, conn, pinnedTestInstanceA, second, now) || ackPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, second, func() time.Time { return now }) {
		t.Fatal("reset retained old capability")
	}
}

func TestPinnedWSConcurrentReplacementNeverRevivesOldSession(t *testing.T) {
	const uuid = "pinned-session-race"
	a, b := &connection.SafeConn{}, &connection.SafeConn{}
	t.Cleanup(func() { ResetClient(uuid) })
	now := time.Now()
	for i := 0; i < 100; i++ {
		SetConnectedClients(uuid, a)
		challenge, ok := beginPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, 1, func() time.Time { return now })
		if !ok {
			t.Fatal("challenge failed")
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			ackPinnedWSHandshakeAt(uuid, a, pinnedTestInstanceA, challenge, func() time.Time { return now })
		}()
		go func() { defer wg.Done(); SetConnectedClients(uuid, b) }()
		wg.Wait()
		if PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challenge, now) {
			t.Fatal("old capability survived replacement")
		}
	}
}

func TestPinnedWSHandshakeSamplesLeaseTimeInsideLock(t *testing.T) {
	const uuid = "pinned-session-clock"
	conn := &connection.SafeConn{}
	SetConnectedClients(uuid, conn)
	t.Cleanup(func() { ResetClient(uuid) })
	mu.Lock()
	started, done := make(chan struct{}), make(chan bool, 1)
	now := time.Now()
	go func() {
		close(started)
		_, ok := beginPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, 1, func() time.Time {
			return now.Add(10 * time.Second)
		})
		done <- ok
	}()
	<-started
	mu.Unlock()
	if !<-done {
		t.Fatal("begin failed")
	}
	mu.Lock()
	s := pinnedWSSessions[uuid]
	s.expires = now.Add(time.Second)
	pinnedWSSessions[uuid] = s
	mu.Unlock()
	if ackPinnedWSHandshakeAt(uuid, conn, pinnedTestInstanceA, s.challenge, func() time.Time { return now.Add(2 * time.Second) }) {
		t.Fatal("expired session was renewed")
	}
}
