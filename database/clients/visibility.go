package clients

import (
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	"sync"
)

var visibilityCache = struct {
	sync.Mutex
	revision uint64
	hidden   map[string]bool
}{}

// HiddenClients returns the uuid/hidden map for every client. Successful client
// writes invalidate the cache immediately; only uuid/hidden are loaded, never
// tokens or hardware details.
//
// The result is a copy the caller owns. It used to hand back the cached map
// itself, so the "immutable snapshot" in the old comment was a convention rather
// than a property: one caller deleting or assigning an entry silently changed
// what every later caller saw, until the next client write refreshed the cache.
func HiddenClients() (map[string]bool, error) {
	db := dbcore.GetDBInstance()
	visibilityCache.Lock()
	defer visibilityCache.Unlock()
	revision := dbcache.Revision("clients")
	if visibilityCache.hidden != nil && visibilityCache.revision == revision {
		return cloneHidden(visibilityCache.hidden), nil
	}
	var rows []models.Client
	if err := db.Select("uuid", "hidden").Find(&rows).Error; err != nil {
		return nil, err
	}
	hidden := make(map[string]bool, len(rows))
	for _, row := range rows {
		hidden[row.UUID] = row.Hidden
	}
	visibilityCache.hidden, visibilityCache.revision = hidden, revision
	return cloneHidden(hidden), nil
}

func cloneHidden(hidden map[string]bool) map[string]bool {
	snapshot := make(map[string]bool, len(hidden))
	for uuid, isHidden := range hidden {
		snapshot[uuid] = isHidden
	}
	return snapshot
}
