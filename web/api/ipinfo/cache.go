package ipinfo

import (
	"sync"
	"time"
)

// cacheItem 是缓存条目：expiresAt 之后不再「新鲜」，但会保留到 staleUntil，
// 供上游全部失败时兜底返回（此时 meta.stale=true）。
type cacheItem[T any] struct {
	value      T
	updatedAt  time.Time
	expiresAt  time.Time
	staleUntil time.Time
}

// cacheHit 是一次缓存读取的结果。
type cacheHit[T any] struct {
	Value      T
	UpdatedAt  time.Time
	ExpiresAt  time.Time
	StaleUntil time.Time
	Fresh      bool
}

// ttlCache 是带兜底期的极简 TTL 缓存。
//
// 为什么不用项目里已有的 go-cache：这里需要「过期但保留」的语义，并且要能注入
// 时钟做测试。go-cache 的 janitor 会在过期后直接删除条目，且时间不可注入，
// 所以单独实现一个无后台 goroutine、无定时器的版本（依赖注入的 now 推进时间）。
type ttlCache[T any] struct {
	mu      sync.Mutex
	items   map[string]cacheItem[T]
	maxSize int
	now     func() time.Time
}

func newTTLCache[T any](maxSize int, now func() time.Time) *ttlCache[T] {
	if maxSize <= 0 {
		maxSize = 1024
	}
	if now == nil {
		now = time.Now
	}
	return &ttlCache[T]{items: make(map[string]cacheItem[T]), maxSize: maxSize, now: now}
}

// get 读取条目。ok=false 表示完全没有可用数据（包括已过兜底期）。
func (c *ttlCache[T]) get(key string) (cacheHit[T], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, found := c.items[key]
	if !found {
		return cacheHit[T]{}, false
	}
	now := c.now()
	if now.After(item.staleUntil) {
		// 连兜底期都过了，顺手清掉。
		delete(c.items, key)
		return cacheHit[T]{}, false
	}
	return cacheHit[T]{
		Value:      item.value,
		UpdatedAt:  item.updatedAt,
		ExpiresAt:  item.expiresAt,
		StaleUntil: item.staleUntil,
		Fresh:      !now.After(item.expiresAt),
	}, true
}

// set 写入条目。ttl 是新鲜期，staleTTL 是新鲜期结束后仍保留用于兜底的时长。
func (c *ttlCache[T]) set(key string, value T, ttl, staleTTL time.Duration) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if staleTTL < 0 {
		staleTTL = 0
	}
	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) >= c.maxSize {
		c.evictLocked(now)
	}
	c.items[key] = cacheItem[T]{
		value:      value,
		updatedAt:  now,
		expiresAt:  now.Add(ttl),
		staleUntil: now.Add(ttl + staleTTL),
	}
}

// delete 主动失效某个键（测试与强制刷新使用）。
func (c *ttlCache[T]) delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

func (c *ttlCache[T]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// evictLocked 先清掉彻底过期的条目；仍然超限时淘汰最先过兜底期的那个。
// 只在写满时触发，复杂度 O(n)，对默认容量来说可以接受。
func (c *ttlCache[T]) evictLocked(now time.Time) {
	for key, item := range c.items {
		if now.After(item.staleUntil) {
			delete(c.items, key)
		}
	}
	if len(c.items) < c.maxSize {
		return
	}
	var oldestKey string
	var oldest time.Time
	for key, item := range c.items {
		if oldestKey == "" || item.staleUntil.Before(oldest) {
			oldestKey, oldest = key, item.staleUntil
		}
	}
	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}
