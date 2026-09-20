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

func Revision(table string) uint64 { return revisions[table].Load() }

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
