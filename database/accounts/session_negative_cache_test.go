package accounts

import (
	"errors"
	"testing"

	"gorm.io/gorm"
)

// 这次改动引入的两条性质。
//
//  1. 「没有这个会话」要被缓存。token 完全由调用方控制（任意 session_token
//     cookie，见 web/api/principal.go），没有负缓存时，一串伪造 cookie 会让每条
//     请求都落到数据库上 —— 而那条 JOIN 以前还在全局锁里跑，于是顺带把所有其它
//     会话查找串行化。
//  2. 命中负缓存必须仍然返回 ErrRecordNotFound。负缓存里存的是零值
//     models.Session；如果它被当成命中直接返回，调用方会拿到「nil error + 空会话」，
//     也就是把「没有会话」读成「有一个空会话」。
func TestLoadSessionCachesAMissAndStillReportsNotFound(t *testing.T) {
	restoreSessionCaches(t)

	const token = "no-such-session-token"
	if _, err := loadSession(token); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("first lookup error = %v, want ErrRecordNotFound", err)
	}

	sessionCache.Lock()
	entry, cached := sessionCache.entries[token]
	sessionCache.Unlock()
	if !cached {
		t.Fatal("a miss was not cached; every forged cookie would reach the database")
	}
	if entry.valid {
		t.Fatal("a negative entry must not claim to be a valid session")
	}

	if _, err := loadSession(token); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second lookup error = %v, want ErrRecordNotFound: a cached miss must not read as success", err)
	}
}

func TestLoadSessionCachesAHit(t *testing.T) {
	restoreSessionCaches(t)

	uuid := createTestAccount(t, "session-cache-hit")
	token, err := CreateSession(uuid, 3600, "test-agent", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	record, err := loadSession(token)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if record.Session != token || record.UUID != uuid {
		t.Fatalf("record = %+v, want session %q for %q", record, token, uuid)
	}

	sessionCache.Lock()
	entry, cached := sessionCache.entries[token]
	sessionCache.Unlock()
	if !cached {
		t.Fatal("a hit was not cached")
	}
	if !entry.valid {
		t.Fatal("a positive entry must be marked valid, or a hit would read as ErrRecordNotFound")
	}

	// 命中缓存时也必须给出同一个会话，而不是零值。
	again, err := loadSession(token)
	if err != nil || again.Session != token {
		t.Fatalf("cached lookup = (%+v, %v), want the same session", again, err)
	}
}
