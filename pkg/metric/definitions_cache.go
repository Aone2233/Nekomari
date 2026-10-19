package metric

import (
	"context"
	"fmt"
)

// definitionsSnapshot is an immutable view of the metric definitions table.
// The ingest path reads it on every batch, so it is built once per definition
// change instead of once per WriteBatch. A snapshot is never mutated after it
// is published, which lets readers share it without copying.
//
// definitionsSnapshot 是指标定义表的不可变快照。写入路径每批都要读取定义，
// 因此改为每次定义变化后重建一次，而不是每个 WriteBatch 都重读全表。快照发布后
// 不再修改，读者无需复制即可共享。
type definitionsSnapshot struct {
	// byName indexes the definitions by metric name.
	//
	// byName 按指标名索引定义。
	byName map[string]Definition
	// sorted holds the definitions ordered by name, matching ListMetrics.
	//
	// sorted 保存按名称升序排列的定义，与 ListMetrics 的顺序一致。
	sorted []Definition
}

// newDefinitionsSnapshot builds an immutable snapshot from freshly read rows.
//
// newDefinitionsSnapshot 用刚从数据库读取的行构建不可变快照。
func newDefinitionsSnapshot(defs []Definition) *definitionsSnapshot {
	snapshot := &definitionsSnapshot{
		byName: make(map[string]Definition, len(defs)),
		sorted: defs,
	}
	for _, def := range defs {
		snapshot.byName[def.Name] = def
	}
	return snapshot
}

// metricDefinitions returns the cached definitions snapshot, loading it from
// the database on first use after an invalidation. The write lock is held for
// the whole load so concurrent callers all observe the same snapshot and a
// definition change can never publish a snapshot read before that change.
//
// metricDefinitions 返回缓存的指标定义快照；缓存失效后的首次读取会从数据库加载。
// 加载期间持有写锁，保证并发调用看到同一份快照，且定义变更不会发布一份在变更前
// 读取的快照。
func (s *Store) metricDefinitions(ctx context.Context) (*definitionsSnapshot, error) {
	s.definitionsMu.RLock()
	cached := s.definitions
	s.definitionsMu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	s.definitionsMu.Lock()
	defer s.definitionsMu.Unlock()
	if s.definitions != nil {
		return s.definitions, nil
	}
	defs, err := s.listMetricsFresh(ctx)
	if err != nil {
		return nil, err
	}
	s.definitions = newDefinitionsSnapshot(defs)
	return s.definitions, nil
}

// invalidateDefinitions drops the cached definitions so the next read reloads
// the table. Every path that inserts, updates or deletes a definitions row, or
// that replaces the definitions table, must call it; otherwise ingestion would
// keep filtering points with a stale retention policy.
//
// The cache assumes this Store is the only writer of its definitions table.
// Maintenance paths (CleanupExpired, restructure, imports) read the committed
// table through listMetricsFresh, so an out-of-band SQL change still affects
// them before the snapshot is refreshed.
//
// invalidateDefinitions 丢弃缓存的指标定义，使下一次读取重新加载定义表。任何
// 插入、更新、删除定义行或整体替换定义表的路径都必须调用它，否则写入仍会按旧的
// retention 策略过滤数据点。
//
// 该缓存假设本 Store 是定义表的唯一写入方。维护路径（CleanupExpired、结构重建、
// 导入）通过 listMetricsFresh 读取已提交的定义表，因此绕过 API 的 SQL 改动在快照
// 刷新前依然对它们生效。
func (s *Store) invalidateDefinitions() {
	s.definitionsMu.Lock()
	s.definitions = nil
	s.definitionsMu.Unlock()
}

// listMetricsFresh reads every metric definition straight from the database.
// Internal maintenance paths use it so they always act on the committed
// definitions rather than on the ingest snapshot.
//
// listMetricsFresh 直接从数据库读取全部指标定义。内部维护路径使用它，以便始终
// 基于已提交的定义而不是写入路径的快照工作。
func (s *Store) listMetricsFresh(ctx context.Context) ([]Definition, error) {
	rows, err := s.reader().QueryContext(ctx, fmt.Sprintf(
		`SELECT name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli FROM %s ORDER BY name ASC`,
		s.tables.definitions,
	))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Definition
	for rows.Next() {
		def, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, rows.Err()
}
