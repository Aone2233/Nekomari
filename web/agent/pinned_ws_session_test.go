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
	if _, ok := BeginPinnedWSHandshake(uuid, a, pinnedTestInstanceA, 1, now); ok {
		t.Fatal("unregistered socket received a challenge")
	}
	SetConnectedClients(uuid, a)
	for _, instance := range []string{"", "ABCDEF0123456789abcdef0123456789", strings.Repeat("g", 32), strings.Repeat("a", 33)} {
		if _, ok := BeginPinnedWSHandshake(uuid, a, instance, 1, now); ok {
			t.Fatalf("accepted invalid instance %q", instance)
		}
	}
	if _, ok := BeginPinnedWSHandshake(uuid, a, pinnedTestInstanceA, 2, now); ok {
		t.Fatal("accepted unsupported capability version")
	}
	challengeA, ok := BeginPinnedWSHandshake(uuid, a, pinnedTestInstanceA, 1, now)
	if !ok || !validPinnedWSID(challengeA) || PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now) {
		t.Fatal("challenge did not start inactive")
	}
	if AckPinnedWSHandshake(uuid, a, pinnedTestInstanceA, strings.Repeat("0", 32), now) || AckPinnedWSHandshake(uuid, b, pinnedTestInstanceA, challengeA, now) {
		t.Fatal("wrong challenge/socket activated capability")
	}
	if !AckPinnedWSHandshake(uuid, a, pinnedTestInstanceA, challengeA, now) || !PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now) {
		t.Fatal("current socket could not activate capability")
	}
	if PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now.Add(pinnedWSLease)) || AckPinnedWSHandshake(uuid, a, pinnedTestInstanceA, challengeA, now.Add(pinnedWSLease)) {
		t.Fatal("expired lease remained usable or was revived")
	}
	challengeA, ok = BeginPinnedWSHandshake(uuid, a, pinnedTestInstanceA, 1, now)
	if !ok || !AckPinnedWSHandshake(uuid, a, pinnedTestInstanceA, challengeA, now) {
		t.Fatal("fresh handshake failed")
	}
	SetConnectedClients(uuid, b)
	if PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challengeA, now) || AckPinnedWSHandshake(uuid, a, pinnedTestInstanceA, challengeA, now) {
		t.Fatal("replaced socket retained capability")
	}
	challengeB, ok := BeginPinnedWSHandshake(uuid, b, pinnedTestInstanceB, 1, now)
	if !ok || !AckPinnedWSHandshake(uuid, b, pinnedTestInstanceB, challengeB, now) {
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
	first, ok := BeginPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, 1, now)
	if !ok || !AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, first, now) {
		t.Fatal("first handshake failed")
	}
	second, ok := BeginPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, 1, now.Add(time.Second))
	if !ok || first == second || AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, first, now.Add(time.Second)) {
		t.Fatal("rehandshake accepted stale nonce")
	}
	if !AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, second, now.Add(time.Second)) ||
		!AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, second, now.Add(pinnedWSLease-time.Second)) ||
		!PinnedWSSessionCurrent(uuid, conn, pinnedTestInstanceA, second, now.Add(pinnedWSLease+time.Second)) {
		t.Fatal("current lease failed to renew")
	}
	ResetClient(uuid)
	if PinnedWSSessionCurrent(uuid, conn, pinnedTestInstanceA, second, now) || AckPinnedWSHandshake(uuid, conn, pinnedTestInstanceA, second, now) {
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
		challenge, ok := BeginPinnedWSHandshake(uuid, a, pinnedTestInstanceA, 1, now)
		if !ok {
			t.Fatal("challenge failed")
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); AckPinnedWSHandshake(uuid, a, pinnedTestInstanceA, challenge, now) }()
		go func() { defer wg.Done(); SetConnectedClients(uuid, b) }()
		wg.Wait()
		if PinnedWSSessionCurrent(uuid, a, pinnedTestInstanceA, challenge, now) {
			t.Fatal("old capability survived replacement")
		}
	}
}
