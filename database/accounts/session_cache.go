package accounts

import (
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"sync"
	"time"
)

type cachedSession struct {
	record models.Session
	until  time.Time
}

var sessionCache = struct {
	sync.Mutex
	revision     uint64
	userRevision uint64
	entries      map[string]cachedSession
}{}
var sessionActivity = struct {
	sync.Mutex
	times map[string]time.Time
}{times: make(map[string]time.Time)}

func loadSession(token string) (models.Session, error) {
	db := dbcore.GetDBInstance()
	sessionCache.Lock()
	defer sessionCache.Unlock()
	revision := dbcache.Revision("sessions")
	userRevision := dbcache.Revision("users")
	if sessionCache.entries == nil || sessionCache.revision != revision || sessionCache.userRevision != userRevision {
		sessionCache.entries = make(map[string]cachedSession)
		sessionCache.revision = revision
		sessionCache.userRevision = userRevision
	}
	now := time.Now()
	if entry, ok := sessionCache.entries[token]; ok && now.Before(entry.until) {
		return entry.record, nil
	}
	var record models.Session
	err := db.Select("sessions.*").Joins("JOIN users ON users.uuid = sessions.uuid").Where("sessions.session = ?", token).First(&record).Error
	if err == nil {
		if len(sessionCache.entries) >= 4096 {
			clear(sessionCache.entries)
		}
		sessionCache.entries[token] = cachedSession{record, now.Add(30 * time.Second)}
	}
	return record, err
}

func touchSession(token, useragent, ip string) error {
	now := time.Now().UTC()
	sessionActivity.Lock()
	if last := sessionActivity.times[token]; now.Sub(last) < time.Minute {
		sessionActivity.Unlock()
		return nil
	}
	if len(sessionActivity.times) >= 4096 {
		clear(sessionActivity.times)
	}
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
