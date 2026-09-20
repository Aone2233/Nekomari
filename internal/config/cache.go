package config

import (
	"github.com/Aone2233/nekomari/internal/dbcache"
	"sync"
)

var itemCache = struct {
	sync.Mutex
	revision uint64
	items    map[string]ConfigItem
}{}

func cachedItem(key string) (ConfigItem, error) {
	itemCache.Lock()
	defer itemCache.Unlock()
	revision := dbcache.Revision("configs")
	if itemCache.revision != revision || itemCache.items == nil {
		itemCache.items = make(map[string]ConfigItem)
		itemCache.revision = revision
	}
	if item, ok := itemCache.items[key]; ok {
		return item, nil
	}
	var item ConfigItem
	err := db.First(&item, "key = ?", key).Error
	if err == nil && dbcache.Revision("configs") == revision {
		if len(itemCache.items) >= 1024 {
			clear(itemCache.items)
		}
		itemCache.items[key] = item
	}
	return item, err
}
