package metric

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

// The in-memory coarse rollup tree is the one metric structure whose size grows
// with the number of series rather than with the query window, so it deserves a
// measured regression bound instead of a guess. The 2026-10-18 audit estimated
// 55-70 KB per series from code reading plus t-digest sizes
// (docs/audits/2026-10-18/backend-memory.md §3.7) without being able to stage
// the tree; this harness stages it for real by injecting the batch clock.
//
// 内存中的 coarse rollup 树是唯一随序列数（而不是查询窗口）增长的指标结构，因此
// 值得用一个实测的回归上限而不是估算值。2026-10-18 审计在无法真正建立该树的情况下
// 由代码与 t-digest 尺寸推算为每序列 55–70 KB（backend-memory.md §3.7）；本
// harness 通过注入批次时钟把该树真实地建起来。
const (
	// coarseRollupBytesPerSeriesLimit is deliberately loose: it is a regression
	// tripwire for an order-of-magnitude change (a tier that stops sealing, a
	// digest that stops compressing), not a tight budget.
	//
	// coarseRollupBytesPerSeriesLimit 刻意宽松：它用于捕捉数量级回归（某层不再
	// 封口、digest 不再压缩），而不是精确预算。
	coarseRollupBytesPerSeriesLimit = 80 << 10

	// coarseRollupSimulatedSamplesPerMinute mirrors the ~3 s agent report
	// interval, so one simulated minute fills a minute bucket the way production
	// does and the per-bucket digests reach their real size.
	//
	// coarseRollupSimulatedSamplesPerMinute 对齐 agent 约 3 秒的上报间隔，使模拟的
	// 一分钟像生产环境一样填满分钟桶，各桶 digest 达到真实大小。
	coarseRollupSimulatedSamplesPerMinute = 20

	// coarseRollupMapEntryOverhead approximates the hmap slot + bucket
	// amortization charged per map entry when accounting for a tree in memory.
	//
	// coarseRollupMapEntryOverhead 近似每条 map 记录分摊到的 hmap 槽位与桶开销。
	coarseRollupMapEntryOverhead = 48
)

// coarseRollupSnapshot is the resident shape of Store.coarse after a simulation.
//
// coarseRollupSnapshot 是一次模拟之后 Store.coarse 的常驻形态。
type coarseRollupSnapshot struct {
	// series is the number of simulated series (entities) in the tree.
	series int
	// entries is the number of staged parents.
	entries int
	// children is the total number of child buckets across all parents.
	children int
	// childrenByTier counts child buckets per parent tier.
	childrenByTier map[time.Duration]int
	// digestCentroids is the total centroid capacity retained by child digests.
	digestCentroids int
	// structuralBytes is the accounted size of the tree: structs, map slots,
	// key layout and owned centroid arrays.
	structuralBytes int64
}

func (s coarseRollupSnapshot) bytesPerSeries() float64 {
	if s.series == 0 {
		return 0
	}
	return float64(s.structuralBytes) / float64(s.series)
}

// snapshotCoarseRollups accounts for the resident bytes of the coarse tree.
// Shared string data is not charged again: the strings in a bucket header point
// at JSON that the hot tier and the raw series already hold, so only the header
// itself (inside unsafe.Sizeof(*child)) is a clone-local cost.
//
// snapshotCoarseRollups 统计 coarse 树的常驻字节。共享的字符串数据不重复计入：
// 桶头里的字符串指向 hot 层与 raw 序列已经持有的 JSON，因此只有头本身（包含在
// unsafe.Sizeof(*child) 里）是克隆独占的成本。
func snapshotCoarseRollups(store *Store, series int) coarseRollupSnapshot {
	snapshot := coarseRollupSnapshot{series: series, childrenByTier: make(map[time.Duration]int)}
	store.coarseMu.RLock()
	defer store.coarseMu.RUnlock()
	for key, parent := range store.coarse {
		snapshot.entries++
		snapshot.structuralBytes += int64(unsafe.Sizeof(key)) +
			int64(unsafe.Sizeof(*parent)) +
			coarseRollupMapEntryOverhead +
			int64(cap(parent.higher))*int64(unsafe.Sizeof(time.Duration(0)))
		for _, child := range parent.children {
			snapshot.children++
			snapshot.childrenByTier[key.interval]++
			snapshot.structuralBytes += int64(unsafe.Sizeof(rollupKey{})) +
				int64(unsafe.Sizeof(*child)) +
				coarseRollupMapEntryOverhead
			if child.digest == nil {
				continue
			}
			centroids := cap(child.digest.centroids)
			snapshot.digestCentroids += centroids
			snapshot.structuralBytes += int64(unsafe.Sizeof(*child.digest)) +
				int64(centroids)*int64(unsafe.Sizeof(centroid{}))
		}
	}
	return snapshot
}

// heapAllocAfterGC returns the live heap size with unused garbage collected.
//
// heapAllocAfterGC 在回收无用垃圾后返回存活堆大小。
func heapAllocAfterGC() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// dropCoarseRollupsAndMeasureHeap measures the live heap the coarse tree holds by
// dropping the tree and diffing the live heap after collection. The store is not
// usable for coarse work afterwards, so a caller runs this last.
//
// dropCoarseRollupsAndMeasureHeap 通过丢弃 coarse 树并比较回收后的存活堆来测量它
// 持有的堆内存。调用之后该 Store 不能再做 coarse 相关工作，因此只能在最后调用。
func dropCoarseRollupsAndMeasureHeap(store *Store) int64 {
	withTree := heapAllocAfterGC()
	store.coarseMu.Lock()
	store.coarse = nil
	store.coarseMu.Unlock()
	withoutTree := heapAllocAfterGC()
	if withTree < withoutTree {
		return 0
	}
	return int64(withTree - withoutTree)
}

// simulateCoarseRollupDay feeds one day of minute-resolution ingest through the
// normal write path with an injected clock, so every tier stages and seals its
// parents exactly as it does in production.
//
// simulateCoarseRollupDay 用注入的时钟把一天的分钟级写入送入正常写路径，使每一层
// 都像生产环境一样暂存并封口父桶。
func simulateCoarseRollupDay(tb testing.TB, store *Store, metricName string, series, minutes, samplesPerMinute int) time.Duration {
	tb.Helper()
	ctx := context.Background()
	entities := make([]string, series)
	for i := range entities {
		entities[i] = fmt.Sprintf("node-%03d", i)
	}
	start := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	prepared := make([]preparedMetricPoint, 0, len(entities)*samplesPerMinute)
	stepMillis := int64(0)
	if samplesPerMinute > 0 {
		stepMillis = int64(time.Minute/time.Millisecond) / int64(samplesPerMinute)
	}
	begin := time.Now()
	for minute := 0; minute < minutes; minute++ {
		now := start.Add(time.Duration(minute) * time.Minute)
		prepared = prepared[:0]
		for i, entity := range entities {
			for sample := 0; sample < samplesPerMinute; sample++ {
				prepared = append(prepared, preparedMetricPoint{
					metricName: metricName,
					entityID:   entity,
					tagsHash:   emptyTagsFingerprint,
					tagsJSON:   emptyTagsCanonical,
					labelsHash: emptyTagsFingerprint,
					labelsJSON: emptyTagsCanonical,
					timestamp:  now.UnixMilli() + int64(sample)*stepMillis,
					value:      float64((minute*samplesPerMinute+sample)%17) + float64(i%7),
				})
			}
		}
		if err := store.writePreparedHotRollups(ctx, prepared, now, nil); err != nil {
			tb.Fatalf("write minute %d: %v", minute, err)
		}
	}
	return time.Since(begin)
}

// openCoarseSimulationStore opens an isolated in-memory store with one
// long-retention metric definition, so every default rollup tier survives.
//
// openCoarseSimulationStore 打开一个隔离的内存 Store 并写入一个长保留期的指标
// 定义，使默认的每一层 rollup 都得以保留。
func openCoarseSimulationStore(tb testing.TB, name string) (*Store, string) {
	tb.Helper()
	ctx := context.Background()
	store, err := Open(ctx, SQLite("file:"+name+"?mode=memory&cache=shared"))
	if err != nil {
		tb.Fatalf("open store: %v", err)
	}
	metricName := "coarse.memory.metric"
	if err := store.CreateMetric(ctx, Definition{Name: metricName, Type: TypeGauge, RetentionDays: 30}); err != nil {
		store.Close()
		tb.Fatalf("create metric: %v", err)
	}
	return store, metricName
}

// TestCoarseRollupResidentBytesPerSeries stages a full day of the in-memory
// coarse tree and asserts that it stays inside the regression bound.
//
// Measured on this harness (2026-10-19, 24 h, ~3 s sampling): 5 parents and 46
// child buckets per series (14 five-minute, 9 one-hour, 23 of the day-tier),
// ~828 digest centroids, ~30 KB per series by both the structural account and
// the live-heap diff. That is below the audit's 55-70 KB estimate because the
// audit assumed 24-48 day-tier children and larger per-bucket digests than a
// merging t-digest actually retains.
//
// TestCoarseRollupResidentBytesPerSeries 建立一整天的内存 coarse 树，并断言其
// 常驻字节处于回归上限之内。
func TestCoarseRollupResidentBytesPerSeries(t *testing.T) {
	const series = 10
	const minutes = 24 * 60

	store, metricName := openCoarseSimulationStore(t, t.Name())
	defer func() { _ = store.Close() }()

	elapsed := simulateCoarseRollupDay(t, store, metricName, series, minutes, coarseRollupSimulatedSamplesPerMinute)
	snapshot := snapshotCoarseRollups(store, series)
	heapBytes := dropCoarseRollupsAndMeasureHeap(store)

	heapPerSeries := float64(heapBytes) / float64(series)
	structuralPerSeries := snapshot.bytesPerSeries()
	t.Logf("simulated 24h x %d series in %s: entries=%d children=%d (5m=%d 1h=%d 24h=%d) digestCentroids=%d",
		series, elapsed.Round(time.Millisecond), snapshot.entries, snapshot.children,
		snapshot.childrenByTier[5*time.Minute], snapshot.childrenByTier[time.Hour], snapshot.childrenByTier[24*time.Hour],
		snapshot.digestCentroids)
	t.Logf("resident bytes: heap-diff=%d structural=%d -> %.0f B/series (heap) %.0f B/series (structural), limit %d B/series",
		heapBytes, snapshot.structuralBytes, heapPerSeries, structuralPerSeries, coarseRollupBytesPerSeriesLimit)
	t.Logf("extrapolation at %.1f KB/series: 9 nodes x 21 series = %.1f MiB, 100 nodes x 21 series = %.1f MiB",
		heapPerSeries/1024,
		heapPerSeries*9*21/(1<<20),
		heapPerSeries*100*21/(1<<20))

	// The tree must actually be staged: an empty tree would pass the byte bound
	// while telling us nothing.
	if entriesPerSeries := float64(snapshot.entries) / float64(series); entriesPerSeries < 4 {
		t.Fatalf("coarse parents per series = %.1f, want >= 4 (the tree was not staged)", entriesPerSeries)
	}
	if dayTierChildren := snapshot.childrenByTier[24*time.Hour]; dayTierChildren < 20*series {
		t.Fatalf("day-tier children = %d, want >= %d", dayTierChildren, 20*series)
	}
	if structuralPerSeries > coarseRollupBytesPerSeriesLimit {
		t.Fatalf("coarse rollup structural bytes per series = %.0f, want <= %d", structuralPerSeries, coarseRollupBytesPerSeriesLimit)
	}
	if heapPerSeries > coarseRollupBytesPerSeriesLimit {
		t.Fatalf("coarse rollup live heap bytes per series = %.0f, want <= %d", heapPerSeries, coarseRollupBytesPerSeriesLimit)
	}
}

// BenchmarkCoarseRollupFootprintDay reports the same measurement for the
// 200-series shape the audit extrapolated from. Each iteration stages one
// simulated day, which takes tens of seconds; run it as
//
//	go test ./pkg/metric/ -run '^$' -bench CoarseRollupFootprintDay -benchtime 1x -v
func BenchmarkCoarseRollupFootprintDay(b *testing.B) {
	const series = 200
	const minutes = 24 * 60
	for i := 0; i < b.N; i++ {
		store, metricName := openCoarseSimulationStore(b, fmt.Sprintf("%s-%d", b.Name(), i))
		elapsed := simulateCoarseRollupDay(b, store, metricName, series, minutes, coarseRollupSimulatedSamplesPerMinute)
		snapshot := snapshotCoarseRollups(store, series)
		heapBytes := dropCoarseRollupsAndMeasureHeap(store)
		heapPerSeries := float64(heapBytes) / float64(series)
		b.Logf("24h x %d series in %s: entries=%d children=%d (5m=%d 1h=%d 24h=%d) digestCentroids=%d",
			series, elapsed.Round(time.Millisecond), snapshot.entries, snapshot.children,
			snapshot.childrenByTier[5*time.Minute], snapshot.childrenByTier[time.Hour], snapshot.childrenByTier[24*time.Hour],
			snapshot.digestCentroids)
		b.Logf("resident bytes: heap-diff=%d structural=%d -> %.0f B/series (heap) %.0f B/series (structural)",
			heapBytes, snapshot.structuralBytes, heapPerSeries, snapshot.bytesPerSeries())
		b.Logf("extrapolation at %.1f KB/series: 9 nodes x 21 series = %.1f MiB, 100 nodes x 21 series = %.1f MiB",
			heapPerSeries/1024,
			heapPerSeries*9*21/(1<<20),
			heapPerSeries*100*21/(1<<20))
		_ = store.Close()
	}
}
