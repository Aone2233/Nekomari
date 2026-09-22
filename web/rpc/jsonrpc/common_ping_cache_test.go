package jsonrpc

import (
	"testing"

	"github.com/Aone2233/nekomari/database/models"

	cache "github.com/patrickmn/go-cache"
)

// TestGetAllPingTasksCachedServesFromCache 钉住 ping 任务表的缓存。
//
// 背景：include_ping 缺省为 true，common:getNodesLatestStatus 每次轮询都会整表
// 读取 ping_tasks；而该表不在 internal/dbcache 的监听范围内（只监听
// configs/clients/sessions/users），所以只能在这里做短 TTL 缓存。
//
// 预置一个哨兵值：如果实现绕过缓存去查库，本用例会因为 dbcore 未初始化而失败，
// 而不是静默退化成「每次轮询都全表读取」——那正是这个改动要消除的开销。
func TestGetAllPingTasksCachedServesFromCache(t *testing.T) {
	sentinel := []models.PingTask{{Id: 42, Name: "cached"}}
	pingTasksCache.Set(pingTasksCacheKey, sentinel, cache.DefaultExpiration)
	t.Cleanup(func() { pingTasksCache.Delete(pingTasksCacheKey) })

	got, err := getAllPingTasksCached()
	if err != nil {
		t.Fatalf("getAllPingTasksCached() error = %v", err)
	}
	if len(got) != 1 || got[0].Id != 42 || got[0].Name != "cached" {
		t.Fatalf("getAllPingTasksCached() = %+v, want the cached sentinel", got)
	}
}
