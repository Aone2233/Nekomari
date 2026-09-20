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

// HiddenClients returns an immutable snapshot. Successful client writes invalidate
// it immediately; only uuid/hidden are loaded, never tokens or hardware details.
func HiddenClients() (map[string]bool, error) {
	db := dbcore.GetDBInstance()
	visibilityCache.Lock()
	defer visibilityCache.Unlock()
	revision := dbcache.Revision("clients")
	if visibilityCache.hidden != nil && visibilityCache.revision == revision {
		return visibilityCache.hidden, nil
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
	return hidden, nil
}
