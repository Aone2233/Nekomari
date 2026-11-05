package agent

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/Aone2233/nekomari/web/connection"
)

// Handshake and dispatch reservation state is serialized by mu together with
// connectedClients. No production scheduler consumes the reservation yet.
type pinnedWSSession struct {
	conn      *connection.SafeConn
	instance  string
	challenge string
	expires   time.Time
	active    bool
	sequence  uint64
	pending   map[string]pinnedPermitEntry
}

const pinnedWSLease = 90 * time.Second

var pinnedWSSessions = make(map[string]pinnedWSSession)

func validPinnedWSID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func newPinnedWSChallenge() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// BeginPinnedWSHandshake requires the caller's current-credential admission.
// The socket identity is checked under the same lock as replacement/reset.
func BeginPinnedWSHandshake(uuid string, conn *connection.SafeConn, instance string, version int) (string, bool) {
	return beginPinnedWSHandshakeAt(uuid, conn, instance, version, time.Now)
}

func beginPinnedWSHandshakeAt(uuid string, conn *connection.SafeConn, instance string, version int, now func() time.Time) (string, bool) {
	if uuid == "" || conn == nil || !validPinnedWSID(instance) || version != 1 {
		return "", false
	}
	challenge, err := newPinnedWSChallenge()
	if err != nil {
		return "", false
	}
	mu.Lock()
	defer mu.Unlock()
	if connectedClients[uuid] != conn {
		return "", false
	}
	retirePinnedWSLocked(uuid)
	pinnedWSSessions[uuid] = pinnedWSSession{conn: conn, instance: instance, challenge: challenge, expires: now().Add(pinnedWSLease)}
	return challenge, true
}

// AckPinnedWSHandshake activates or renews only the exact, unexpired session.
func AckPinnedWSHandshake(uuid string, conn *connection.SafeConn, instance, challenge string) bool {
	return ackPinnedWSHandshakeAt(uuid, conn, instance, challenge, time.Now)
}

func ackPinnedWSHandshakeAt(uuid string, conn *connection.SafeConn, instance, challenge string, clock func() time.Time) bool {
	mu.Lock()
	defer mu.Unlock()
	now := clock()
	s, ok := pinnedWSSessions[uuid]
	if !ok || conn == nil || connectedClients[uuid] != conn || s.conn != conn || s.instance != instance || s.challenge != challenge {
		return false
	}
	if !now.Before(s.expires) {
		retirePinnedWSLocked(uuid)
		return false
	}
	s.active = true
	s.expires = now.Add(pinnedWSLease)
	prunePinnedPermitsLocked(&s, now)
	pinnedWSSessions[uuid] = s
	return true
}

// PinnedWSSessionCurrent is intentionally not an authorization to dispatch.
// Event delivery and result admission still need their own atomic fences.
func PinnedWSSessionCurrent(uuid string, conn *connection.SafeConn, instance, challenge string, now time.Time) bool {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := pinnedWSSessions[uuid]
	return ok && conn != nil && connectedClients[uuid] == conn && s.conn == conn && s.active && s.instance == instance && s.challenge == challenge && now.Before(s.expires)
}
