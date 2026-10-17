package accounts

import (
	"errors"
	"sync"
	"time"

	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"gorm.io/gorm"
)

// Both caches are bounded by eviction rather than by clearing. clear() threw away
// every live entry at once: one full session cache turned into a full miss for
// every logged-in user, so the next burst of requests each ran its own SELECT, and
// one full activity map lost the once-per-minute coalescing for every token at
// once, so the next burst each wrote its own UPDATE.
const (
	sessionCacheMaxEntries          = 4096
	sessionActivityMaxEntries       = 4096
	sessionActivityCoalesceInterval = time.Minute
	// sessionNegativeCacheTTL 是「这个 token 没有对应会话」的缓存时长。它比正缓存
	// （30 秒）短：一条刚建好的会话不该被一个陈旧的否定答案挡住。
	sessionNegativeCacheTTL = 10 * time.Second
)

type cachedSession struct {
	record models.Session
	until  time.Time
	// valid 区分「查到了这个会话」与「查过、没有」：负缓存要能返回
	// gorm.ErrRecordNotFound，而不是一个零值 Session 加 nil error。
	valid bool
}

var sessionCache = struct {
	sync.Mutex
	userRevision uint64
	entries      map[string]cachedSession
}{}
var sessionActivity = struct {
	sync.Mutex
	times map[string]time.Time
}{times: make(map[string]time.Time)}

func loadSession(token string) (models.Session, error) {
	db := dbcore.GetDBInstance()

	now := time.Now()
	sessionCache.Lock()
	// Only the users revision empties the cache, because the cached record is a
	// JOIN against users: a deleted or rewritten account must stop resolving.
	//
	// The sessions revision deliberately does NOT. Every coalesced activity UPDATE
	// (touchSession, at most one per token per minute) bumps it, so keying the cache
	// on it wiped all 4096 entries about once a minute and made the next request of
	// every logged-in user run its own SELECT — the cache undoing itself. Revocation
	// invalidates explicitly instead: the session writers in this package call
	// forgetSession / forgetAllSessions.
	userRevision := dbcache.Revision("users")
	if sessionCache.entries == nil || sessionCache.userRevision != userRevision {
		sessionCache.entries = make(map[string]cachedSession)
		sessionCache.userRevision = userRevision
	}
	if entry, ok := sessionCache.entries[token]; ok && now.Before(entry.until) {
		record, valid := entry.record, entry.valid
		sessionCache.Unlock()
		if valid {
			return record, nil
		}
		return models.Session{}, gorm.ErrRecordNotFound
	}
	sessionCache.Unlock()

	// 查询在锁外进行。以前这条 JOIN 是在那把全局锁里跑的，而 token 完全由调用方
	// 控制（任意 session_token cookie，见 web/api/principal.go），于是一串伪造或
	// 过期的 cookie 会让每条请求都查一次库，并且把所有其它会话查找串行化在它后面。
	var record models.Session
	err := db.Select("sessions.*").Joins("JOIN users ON users.uuid = sessions.uuid").Where("sessions.session = ?", token).First(&record).Error

	sessionCache.Lock()
	defer sessionCache.Unlock()
	// 查询期间 users 可能已经变了，所以这里重新对一次 revision，而不是直接写入。
	if current := dbcache.Revision("users"); sessionCache.userRevision != current {
		sessionCache.entries = make(map[string]cachedSession)
		sessionCache.userRevision = current
	}
	pruneSessionCacheLocked(now)
	switch {
	case err == nil:
		sessionCache.entries[token] = cachedSession{record: record, until: now.Add(30 * time.Second), valid: true}
	case errors.Is(err, gorm.ErrRecordNotFound):
		// 负缓存。「没有这个会话」和「有这个会话」一样值得记住：没有它，洪泛伪造
		// cookie 的每一条请求都会落到数据库上。TTL 比正缓存短，这样一条刚刚建好的
		// 会话不会被一个陈旧的否定答案挡住。
		sessionCache.entries[token] = cachedSession{until: now.Add(sessionNegativeCacheTTL)}
	}
	return record, err
}

// forgetSession drops one cached session so a revocation takes effect immediately.
func forgetSession(token string) {
	sessionCache.Lock()
	defer sessionCache.Unlock()
	delete(sessionCache.entries, token)
}

// forgetAllSessions drops the whole cache, for the bulk revocations. A nil map is
// recreated by the next loadSession.
func forgetAllSessions() {
	sessionCache.Lock()
	defer sessionCache.Unlock()
	sessionCache.entries = nil
}

// pruneSessionCacheLocked makes room for one entry. Expired entries go first;
// only if the cache is still full of live entries does it drop the entries that
// expire soonest, which are the ones closest to being reloaded anyway.
func pruneSessionCacheLocked(now time.Time) {
	if len(sessionCache.entries) < sessionCacheMaxEntries {
		return
	}
	for token, entry := range sessionCache.entries {
		if !now.Before(entry.until) {
			delete(sessionCache.entries, token)
		}
	}
	for len(sessionCache.entries) >= sessionCacheMaxEntries {
		oldestToken := ""
		var oldestUntil time.Time
		for token, entry := range sessionCache.entries {
			if oldestToken == "" || entry.until.Before(oldestUntil) {
				oldestToken, oldestUntil = token, entry.until
			}
		}
		if oldestToken == "" {
			return
		}
		delete(sessionCache.entries, oldestToken)
	}
}

func touchSession(token, useragent, ip string) error {
	now := time.Now().UTC()
	sessionActivity.Lock()
	if last := sessionActivity.times[token]; now.Sub(last) < sessionActivityCoalesceInterval {
		sessionActivity.Unlock()
		return nil
	}
	pruneSessionActivityLocked(now)
	sessionActivity.times[token] = now
	sessionActivity.Unlock()
	err := dbcore.GetDBInstance().Model(&models.Session{}).Where("session = ?", token).Updates(map[string]interface{}{
		"latest_online": now, "latest_user_agent": useragent, "latest_ip": ip,
	}).Error
	if err != nil {
		sessionActivity.Lock()
		delete(sessionActivity.times, token)
		sessionActivity.Unlock()
	}
	return err
}

// pruneSessionActivityLocked makes room for one timestamp. Entries older than the
// coalescing interval are free to drop: their next request writes an UPDATE either
// way. Only a genuinely oversized set of *active* tokens falls back to dropping
// the oldest timestamps.
func pruneSessionActivityLocked(now time.Time) {
	if len(sessionActivity.times) < sessionActivityMaxEntries {
		return
	}
	cutoff := now.Add(-sessionActivityCoalesceInterval)
	for token, last := range sessionActivity.times {
		if last.Before(cutoff) {
			delete(sessionActivity.times, token)
		}
	}
	for len(sessionActivity.times) >= sessionActivityMaxEntries {
		oldestToken := ""
		var oldestSeen time.Time
		for token, last := range sessionActivity.times {
			if oldestToken == "" || last.Before(oldestSeen) {
				oldestToken, oldestSeen = token, last
			}
		}
		if oldestToken == "" {
			return
		}
		delete(sessionActivity.times, oldestToken)
	}
}
