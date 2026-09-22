package accounts

import (
	"fmt"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"gorm.io/gorm"
)

// restoreSessionCaches puts the package-level caches back as the tests found them,
// so a later test in this package cannot inherit a half-built map.
func restoreSessionCaches(t *testing.T) {
	t.Helper()
	sessionCache.Lock()
	entries, userRevision := sessionCache.entries, sessionCache.userRevision
	sessionCache.entries = make(map[string]cachedSession)
	sessionCache.Unlock()
	sessionActivity.Lock()
	times := sessionActivity.times
	sessionActivity.times = make(map[string]time.Time)
	sessionActivity.Unlock()
	t.Cleanup(func() {
		sessionCache.Lock()
		sessionCache.entries, sessionCache.userRevision = entries, userRevision
		sessionCache.Unlock()
		sessionActivity.Lock()
		sessionActivity.times = times
		sessionActivity.Unlock()
	})
}

// A full activity map must evict, not clear. clear() dropped the coalescing
// window for every token at once, so the next burst of requests each wrote its
// own UPDATE instead of at most one per token per minute.
func TestSessionActivityPruneKeepsRecentTokens(t *testing.T) {
	restoreSessionCaches(t)
	now := time.Now().UTC()
	sessionActivity.Lock()
	sessionActivity.times = make(map[string]time.Time)
	for i := 0; i < sessionActivityMaxEntries; i++ {
		sessionActivity.times[fmt.Sprintf("stale-%d", i)] = now.Add(-2 * sessionActivityCoalesceInterval)
	}
	sessionActivity.times["active"] = now
	sessionActivity.Unlock()

	pruneSessionActivityLocked(now)

	sessionActivity.Lock()
	defer sessionActivity.Unlock()
	if _, ok := sessionActivity.times["active"]; !ok {
		t.Fatal("prune dropped a token inside the coalescing window")
	}
	if len(sessionActivity.times) != 1 {
		t.Fatalf("stale tokens survived prune: %d left", len(sessionActivity.times))
	}
}

// A full map of *active* tokens still has to be bounded, and the entries kept
// must be the newest ones rather than an arbitrary survivor set.
func TestSessionActivityPruneBoundsActiveTokens(t *testing.T) {
	restoreSessionCaches(t)
	now := time.Now().UTC()
	sessionActivity.Lock()
	sessionActivity.times = make(map[string]time.Time)
	for i := 0; i < sessionActivityMaxEntries; i++ {
		// Sub-second offsets: every entry is inside the one-minute coalescing
		// window, so none of them may be dropped for being stale.
		sessionActivity.times[fmt.Sprintf("active-%d", i)] = now.Add(-time.Duration(i) * time.Millisecond)
	}
	sessionActivity.Unlock()

	pruneSessionActivityLocked(now)

	sessionActivity.Lock()
	defer sessionActivity.Unlock()
	if len(sessionActivity.times) != sessionActivityMaxEntries-1 {
		t.Fatalf("prune left %d entries, want %d", len(sessionActivity.times), sessionActivityMaxEntries-1)
	}
	if _, ok := sessionActivity.times["active-0"]; !ok {
		t.Fatal("newest active token was evicted")
	}
	if _, ok := sessionActivity.times[fmt.Sprintf("active-%d", sessionActivityMaxEntries-1)]; ok {
		t.Fatal("oldest active token survived")
	}
}

// The session cache has the same requirement: one full cache must not become a
// full miss for every logged-in user at once.
func TestSessionCachePruneKeepsLiveEntries(t *testing.T) {
	restoreSessionCaches(t)
	now := time.Now()
	sessionCache.Lock()
	sessionCache.entries = make(map[string]cachedSession)
	for i := 0; i < sessionCacheMaxEntries; i++ {
		sessionCache.entries[fmt.Sprintf("expired-%d", i)] = cachedSession{until: now.Add(-time.Second)}
	}
	sessionCache.entries["live"] = cachedSession{until: now.Add(30 * time.Second)}
	sessionCache.Unlock()

	pruneSessionCacheLocked(now)

	sessionCache.Lock()
	defer sessionCache.Unlock()
	if _, ok := sessionCache.entries["live"]; !ok {
		t.Fatal("prune dropped a live session entry")
	}
	if len(sessionCache.entries) != 1 {
		t.Fatalf("expired entries survived prune: %d left", len(sessionCache.entries))
	}
}

// With no expired entry to drop, the cache must still make room — by evicting the
// entry that expires soonest, not by emptying the map.
func TestSessionCachePruneBoundsLiveEntries(t *testing.T) {
	restoreSessionCaches(t)
	now := time.Now()
	sessionCache.Lock()
	sessionCache.entries = make(map[string]cachedSession)
	for i := 0; i < sessionCacheMaxEntries; i++ {
		// Strictly in the future, so the expired sweep cannot be what frees room.
		sessionCache.entries[fmt.Sprintf("live-%d", i)] = cachedSession{until: now.Add(time.Duration(i+1) * time.Second)}
	}
	sessionCache.Unlock()

	pruneSessionCacheLocked(now)

	sessionCache.Lock()
	defer sessionCache.Unlock()
	if len(sessionCache.entries) != sessionCacheMaxEntries-1 {
		t.Fatalf("prune left %d entries, want %d", len(sessionCache.entries), sessionCacheMaxEntries-1)
	}
	if _, ok := sessionCache.entries["live-0"]; ok {
		t.Fatal("the entry expiring soonest should have been evicted")
	}
	if _, ok := sessionCache.entries[fmt.Sprintf("live-%d", sessionCacheMaxEntries-1)]; !ok {
		t.Fatal("entry expiring last was evicted")
	}
}

// An activity UPDATE bumps the sessions revision. It must not empty the session
// cache: keying the cache on that revision wiped every entry about once a minute,
// so each logged-in user's next request ran its own SELECT — the cache undoing
// itself.
func TestSessionCacheSurvivesActivityWrites(t *testing.T) {
	db := dbcore.GetDBInstance()
	restoreSessionCaches(t)

	uuid := createTestAccount(t, "session-cache")
	token, err := CreateSession(uuid, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetSession(token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := DeleteSession(token); err != nil {
			t.Error(err)
		}
	})

	revision := dbcache.Revision("sessions")
	if err := db.Model(&models.Session{}).Where("session = ?", token).Update("latest_online", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	if dbcache.Revision("sessions") == revision {
		t.Fatal("the activity write did not move the sessions revision, so this test proves nothing")
	}

	queries := 0
	const name = "session-cache:query-count"
	if err := db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "sessions" {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(name)

	if _, err := GetSession(token); err != nil {
		t.Fatal(err)
	}
	if queries != 0 {
		t.Fatalf("an activity write forced %d session SELECTs", queries)
	}
}

// Revoking a session must invalidate it immediately, without waiting for the
// entry's 30-second lifetime: the cache no longer follows the sessions revision.
func TestDeleteSessionInvalidatesCachedEntry(t *testing.T) {
	restoreSessionCaches(t)

	uuid := createTestAccount(t, "session-revocation")
	token, err := CreateSession(uuid, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetSession(token); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(token); err != nil {
		t.Fatal(err)
	}
	if _, err := GetSession(token); err == nil {
		t.Fatal("a revoked session still resolved from the cache")
	}
}
