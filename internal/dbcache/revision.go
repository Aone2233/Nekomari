// Package dbcache tracks writes to small configuration tables. Caches retain
// values only while the table revision is unchanged; metrics are never cached here.
package dbcache

import (
	"gorm.io/gorm"
	"sync/atomic"
)

var revisions = map[string]*atomic.Uint64{
	"configs": {}, "clients": {}, "sessions": {}, "users": {},
}

// Revision returns the current revision of a watched table. A table that is not
// watched reports 0 instead of panicking: callers derive the name from the data
// they cache, and a nil map entry used to dereference nil. 0 is stable for an
// unwatched table, so a caller comparing two readings still sees "unchanged".
func Revision(table string) uint64 {
	revision, ok := revisions[table]
	if !ok {
		return 0
	}
	return revision.Load()
}

func InvalidateAll() {
	for _, revision := range revisions {
		revision.Add(1)
	}
}

func Watch(db *gorm.DB) {
	changed := func(tx *gorm.DB) {
		if tx.Error == nil {
			if revision := revisions[tx.Statement.Table]; revision != nil {
				revision.Add(1)
			}
		}
	}
	_ = db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register("nekomari:cache", changed)
	_ = db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("nekomari:cache", changed)
	_ = db.Callback().Delete().After("gorm:commit_or_rollback_transaction").Register("nekomari:cache", changed)
	_ = db.Callback().Raw().After("gorm:raw").Register("nekomari:cache", func(tx *gorm.DB) { InvalidateAll() })
	InvalidateAll()
}
