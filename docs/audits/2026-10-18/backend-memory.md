# 后端内存/性能审计 — 2026-10-18

范围：面板 Go 服务端（`pkg/metric` 存储引擎、缓存、goroutine/ticker、HTTP/WebSocket、文件传输、DB 池与 SQLite 调优、插件 goja VM、agent 足迹）。
方法：只读源码 + 在本仓库之外（`%TEMP%\nkbench`，独立 module + `replace` 指向本仓库）写临时 benchmark 实测分配，未改任何源文件，未跑全量测试。
机器：Windows amd64、AMD Ryzen 7 9800X3D、`go1.27.2`；本文所有 ns/B/allocs 数字都是本机实测，不是推算（推算处均已标注）。仓库版本：Nekomari v1.6.12（CHANGELOG 顶部），OPEN-WORK 最后改写 2026-10-13。

---

## 1. 结论摘要（Top 5，按性价比排序）

1. **【F2，P1】写入路径有 2 个确定性热点，占该路径分配的一半以上，且都可无损消除。**
   每 `WriteBatch`（180 点）实测 **147 KB / 2,573 allocs**；pprof 归因：`filterDisabledMetricPoints → ListMetrics` 链约 **29%**（每批全表重读指标定义并重建 map），`tagsFingerprint`（JSON + SHA256 + hex，每点两次）约 **28.8%**，`Point.normalized` 为 nil tags/labels 各分配一个空 map 占 **12.9%**。
   改法收益：定义缓存 + 指纹缓存 + 不分配空 map，可砍掉该路径约 **50–60%** 的分配与一次全表查询；工作量 1–1.5 天，可用现有 benchmark 直接回归。

2. **【F3，P1】主 HTTP 服务器只设了 `ReadHeaderTimeout`，没有 `IdleTimeout`/`ReadTimeout`/`WriteTimeout`。**
   `internal/server/runtime.go:141-145`。空闲 keep-alive 连接永远不回收：每条空闲连接常驻 1 个 goroutine + 约 4–16 KB 读写缓冲（常量估算，**待验证**），公网面板可被慢速/空闲连接持续占住。改法：加 `IdleTimeout: 120 * time.Second` 一行（WS 升级后被 hijack，长轮询 25 s < 120 s，不受影响），0.5 天含验证。

3. **【F1，P1】图表/历史查询的每请求分配随“节点数 × 指标数 × 桶数”线性增长。**
   实测 3 小时窗口 × 14 指标 × 9 节点 = 22,680 行 rollup：**24.6 ms / 12.1 MB / 71,347 allocs**（≈534 B/行）。pprof 归因：`consume` 40.7%、`newRollupBucketWithDigest` 22.8%、`points()` 23.7%。
   配合 `maxMetricTotalPoints = 250000`（`web/rpc/jsonrpc/budget.go:14`），单请求内存上限约 **250,000 × 534 B ≈ 133 MB** 瞬时分配；100 节点的整队图表请求 ≈ 11 倍（~134 MB/请求）。改法：`needDigest=false` 时用紧凑累加器代替 `rollupBucket`、按 tags_hash 复用序列字典解码，预计砍掉 30–45%。

4. **【F4/F5，P2】插件 hook 路径有两个被低估的放大项。**
   `runHookTurnTimeout` 每次用 `time.After(timeout)`（`internal/plugin/hook.go:316-321`）：请求完成得再快，计时器也要活到 30 s 默认超时；`bufferedResponseWriter` 的上限是 **32 MB**（`internal/plugin/plugin.go:66`），且 `bodyBytes()` 再整份复制一次（`hook.go:502-506`），单个被 hook 的响应峰值可达 ~64 MB。无插件时零成本，一旦插件 hook 了 `/*` 就是每响应一份。

5. **【F10，P1/待验证】最可能造成 RSS 随节点数线性增长的常驻结构是 `pkg/metric` 的 coarse rollup 内存树。**
   `s.coarse` 为每个 series 保留 5m/1h/24h 三层“未封口”父桶，每个子桶用 `clone(true)` 连 t-digest 一起复制（`pkg/metric/coarse_rollup.go:100`），封口条件是“桶结束 + 10 min ≤ now”（`:47`、`:232`、`:244`），所以 24h 父桶几乎整日驻留。按代码与实测 t-digest 尺寸（1 min 桶 231 B、1 h 桶 775–983 B）估算每 series **约 55–70 KB**：9 节点 × 21 系列 ≈ **10–13 MB**，100 节点 ≈ **130 MB**，500 节点 ≈ **650 MB**。
   **这条是推断，不是实测**：我搭的时钟加速 harness 无效（`addMinuteParents` 用真实 `time.Now()`，见 `hot_rollup.go:25/159`），需要可注入时钟或 2 小时真实流量采样后才能定级；建议先测量再改。

---

## 2. 详细发现表

| ID | 严重度 | 位置 | 证据（实测/代码） | 影响 | 改法 | 工作量 |
|---|---|---|---|---|---|---|
| F1 | P1 | `pkg/metric/batch_series.go:182-197,512-549,600-657,227-254`、`web/rpc/jsonrpc/budget.go:14,84` | 3h×14指标×9节点 = 24.6 ms / 12.1 MB / 71,347 allocs；pprof：consume 40.7%、rollupBucket 22.8%、points 23.7% | 每请求分配 ∝ 行数；budget 封顶时单请求 ~133 MB 瞬时分配；并发图表叠加成 GC 压力 | needDigest=false 用紧凑累加器；tags_hash 缓存字典解码；把预算换成“估算字节” | 2–3 天 |
| F2 | P1 | `pkg/metric/store.go:922-942`+`:577-599`、`raw_points.go:120-147`、`json.go:76-83`、`types.go:258-267` | 180 点/批 = 147 KB / 2,573 allocs；pprof：ListMetrics 链 29%、hex+fingerprint 28.8%、normalized 12.9% | 1000 节点 ≈ 3.3 MB/s 可避免的 GC churn；每批一次全表定义读 | Store 内定义缓存（写路径失效）；写路径 (metric,entity) 指纹缓存；normalized 复用空 map | 1–1.5 天 |
| F3 | P1 | `internal/server/runtime.go:141-145`、`:45`、`guide_server.go:60-64` | 全仓库 `http.Server{` 仅这些；只有 ReadHeaderTimeout | 空闲连接永久占用 goroutine + fd + 缓冲（估算 4–16 KB/连接，待验证） | 加 `IdleTimeout`（120 s）；流式路由保持无 WriteTimeout | 0.5 天 |
| F4 | P2 | `internal/plugin/hook.go:316-321` | `case <-time.After(timeout)` 在 hook 调用栈上，每次请求新建 | 每个被 hook 的请求留一个计时器到超时（默认 30 s），高频路由下堆积 | `t := time.NewTimer(timeout); defer t.Stop()` | 0.5 小时 |
| F5 | P2 | `internal/plugin/plugin.go:66`、`hook.go:197,418-434,502-506` | `maxHookBufferBytes = 32 << 20`；`bodyBytes()` 复制整份 | 单个被 hook 响应峰值 ~64 MB；并发即倍数 | 上限降到 1–2 MB（大响应走 passthrough）；buffer 用 `sync.Pool`；去掉复制 | 0.5–1 天 |
| F6 | P2 | `web/connection/safe_conn.go:138-146` | 每次 `WriteJSON` 新建 `bytes.Buffer` | 每帧 1 次 buffer 分配；终端/文件流/V2 事件都走它 | 池化 buffer + `json.Encoder` 复用 | 0.5 天 |
| F7 | P2 | `pkg/metric/raw_points.go:311-343`、`batch_series.go:406-431,439-483` | 每次查询遍历全部 `s.raw`/`s.hot`/`s.coarse`，每个条目一次 `spendReadBudget(ctx,0)`（ctx.Value 查找） | 查询成本 ∝ 内存中全部 series 数；1000 节点 ≈ 21k 次/查询 | 按 metric 建二级索引；把预算检查移出逐条目循环 | 1 天 |
| F8 | P2 | `pkg/metric/store.go:1158-1207`、`rollup_query.go:205-235` | `Stats` 逐行解码 t-digest、构造 representativePoints，且**不消耗读预算**；生产无调用者（仅测试） | 潜在“接回即热点”，且绕过 250k 预算保护 | 删除或补 `spendReadBudget` + 流式聚合 | 0.5 天 |
| F9 | P2 | `web/filemanager/transfer.go:73-74,105-119` | `downloadChunkSizes` 只有写没有删除 | 每个曾下载的 uuid 一条；admin 可传任意 uuid 时无界 | `delete` on client removal 或加 TTL 清扫 | 0.5 天 |
| F10 | P1/待验证 | `pkg/metric/coarse_rollup.go:39-63,100,227-266`、`hot_rollup.go:25,159` | 代码推断 + t-digest 实测尺寸（1min 231 B、1h 775–983 B） | 若成立：~55–70 KB/series，随节点数线性增长（见摘要 5） | 先做可注入时钟的 harness 测真实 footprint，再决定“按层浅化/按天落盘” | 测量 1 天；改 2–3 天 |
| F11 | 补充证据 | `web/agent/v2_events.go:29-41,99-103,196,227` | OPEN-WORK:70 已跟踪；补充：每事件一次 `close(signal)+make(chan)` | 每 uuid 常驻一个队列（≤128 事件）+ 每事件 1 次 channel 分配 | 客户端删除时 delete 队列；signal 用广播代替 chan 轮换 | 0.5 天 |
| F12 | 补充证据 | `web/api/ws.go:40-107` | OPEN-WORK:133 已跟踪；补充每帧还有 `HiddenClients()`、2 次 config 读、全量 `GetLatestReport()` 复制、整队 JSON 序列化 | 每帧 1 次 DB 查询 + 整队 map/JSON | 连接时识别一次 principal；状态按 revision 增量推送 | 1–2 天 |
| F13 | 信息 | `pkg/jsruntime/runtime.go:413-425`、`internal/plugin/plugin.go:279-297` | 实测 8 个 NodeJS runtime = 7,680 KiB heap、goroutines 增加 8（约 960 KiB + 1 goroutine/个） | 插件数 × ~1 MB 常驻；生命周期正确（Close→Terminate） | 无需改；文档化“每个启用插件约 1 MB” | 0 |
| F14 | 已验证无新问题 | `agent/**`（详见 §3.6） | 所有长生命周期 map 均有上限或删除；ticker 均有 Stop 或由 Stop 管理 | agent 侧未见泄漏；与 AGENT-FOOTPRINT 15.8–24.3 MiB 相符 | 无 | 0 |
| F15 | 基线确认 | `pkg/metric/rollup_read_index_test.go:275`、`web/api/client/report_v2_test.go:93` | series_index 2.74 ms vs resolution_index 179 ms；raw_params 4.4 µs/1,632 B/13 allocs vs untyped 15.4 µs/7,396 B/167 | C2/C4 的既有修复未回归 | 无（应进 CI 基线） | 0 |
| F16 | P2 | `internal/sqlitetune/connector.go:148-177`、`database/dbcore/dbcore.go:245-250,692-701`、`internal/metricstore/config.go:115-116` | 主库 1 连接 cache 8 MB；metrics 库 1 写 + 2 读 × 4 MB；WAL autocheckpoint 256 / 4000 页；全仓库无 `ANALYZE`/`PRAGMA optimize` | SQLite 页缓存 ~20 MB 常量（正常）；长期无统计信息，rollup 查询靠 `INDEXED BY` 兜底 | retention cleanup 后 `PRAGMA optimize`；metrics 库 checkpoint 页数降到 ~1000 以平滑 WAL 峰值 | 0.5 天 |

---

## 3. 明细与证据

### 3.1 F1 图表查询：每请求分配与 pprof 归因

代码路径：`web/rpc/jsonrpc/public.metric.go:303`（fleet/多实体）或 `:1346` → `Store.SeriesBatch`（`pkg/metric/batch_series.go:63`）→ `seriesBatchAt`。

- 每个“输出桶 × 实体”在 `consume`（`batch_series.go:512-549`）里新建一个 `rollupAggregateState`，内部再 `newRollupBucketWithDigest`（`rollup.go:215-226`）分配一个 `rollupBucket`；
- `points()`（`:600-657`）为每个 aggregation 生成一整片 `[]AggregatePoint`；
- `loadSeriesDictionary`（`:227-254`）为**每个**匹配的 series 解码一份 tags map。

实测（临时 benchmark，3 小时窗口、1 分钟 tier、9 实体）：

```
BenchmarkChartSeries3h-16           310   3690976 ns/op   1736278 B/op   10373 allocs/op   (2 指标, 3,240 行)
BenchmarkChartSeries3h14Metrics-16   48  24600227 ns/op  12104367 B/op   71347 allocs/op   (14 指标, 22,680 行)
```

pprof（`-memprofile`，3h×1 指标×9 实体，1,620 行；370 MB 总分配中）：

```
40.69%  metric.(*metricSeriesAccumulator).consume
23.72%  metric.(*metricSeriesAccumulator).points
22.76%  metric.newRollupBucketWithDigest
```

预算算术：`web/rpc/jsonrpc/budget.go:14` `maxMetricTotalPoints = 250000`，`budget.go:84` `metric.WithReadBudget(bounded, maxMetricTotalPoints)`。按 534 B/行，单请求瞬时分配上限 ≈ **133 MB**；且这 250k 行是“允许扫描的行数”，不是“允许的字节数”。100 节点、14 指标的 3 小时整队图 → 约 252k 行，正好撞到预算。

### 3.2 F2 写入路径：每点 817 B 分配，两个可消除热点

`WriteBatch` → `filterDisabledMetricPoints`（`store.go:922-942`，**每次都** `s.ListMetrics` 全表读 + 重建 map）→ `prepareMetricPoints`（`raw_points.go:120-147`，每点 `point.normalized()` 两次 `tagsFingerprint`）→ `writeRawPointsAt` → `writePreparedHotRollups`。

实测（临时 benchmark，20 指标 × 9 实体 = 180 点/批）：

```
BenchmarkLightIngest20Metrics-16   3000   145750 ns/op   147134 B/op   2573 allocs/op
```

pprof（3,000 次调用的分配总量 408.87 MB）：

```
28.77%  encoding/hex.EncodeToString            <- tagsFingerprint 的 hex(sha256) 每点 2 次
61.46%  metric.prepareMetricPoints (cum)       <- 含上面那条 + json.Marshal
27.55%  metric.(*Store).filterDisabledMetricPoints (cum)
14.22%  metric.(*Store).ListMetrics (cum)
12.90%  metric.Point.normalized                <- nil Tags/Labels 各 make 一个空 map
 7.43%  metric.(*Store).writeRawPointsAt
```

要点：
- `ListMetrics`（`store.go:577-599`）返回 **全量定义**（含 `metadata` JSON 解码、`scanDefinition` 的 reflect 分配），每个 3 s flush 批一次；指标定义极少变，完全可缓存（`CreateMetric/UpsertMetric/DeleteMetric/UpdateMetricRetention` 失效，或直接挂 `dbcache.Revision`）。
- `tagsFingerprint`（`json.go:76-83`）每点对 tags 和 labels 各做一次 `json.Marshal` + `sha256` + `hex.EncodeToString`（hex 是 2 倍长度，64 B 字符串）。写路径里 `(metric, entity)` 的 tags/labels 是稳定的，**值**可缓存。
- `Point.normalized`（`types.go:258-267`）对 nil map 也 `map[string]string{}`，每点 2 次。
- 量级：1000 节点 × 20 指标 / 5 s ≈ 4,000 点/s × 817 B ≈ **3.3 MB/s** 的 GC churn；改成缓存后约 1.3–1.6 MB/s。

### 3.3 F3 HTTP 服务器超时

```go
// internal/server/runtime.go:141
a.server = &http.Server{
    Addr:              a.listenAddr,
    Handler:           plugin.HTMLInjectHandler(plugin.WrapHandler(a.engine)),
    ReadHeaderTimeout: httpReadHeaderTimeout,   // 只有这一个
}
```

`ReadHeaderTimeout` 只覆盖“请求头读完”这段，不覆盖 keep-alive 空闲期，也不覆盖慢请求体。第 2 个服务器（`guide_server.go:60-64`）同样只有它。项目在 `docs/HEALTH-REVIEW-2026-09-19.md` 里记录过“加了 ReadHeaderTimeout”，但 idle 这一半没做。

建议：`IdleTimeout: 120 * time.Second`（默认 0 = 跟随 ReadTimeout = 0 = 永不超时）。WebSocket 在 Upgrade 后由 gorilla 接管连接，不受影响；`MethodAgentPull` 的 25 s 长轮询 < 120 s。不要加 `WriteTimeout`（会砍断文件下载/流）。

### 3.4 F4/F5 插件 hook 的计时器与缓冲

```go
// internal/plugin/hook.go:316
select {
case <-done:
    return true, false
case <-time.After(timeout):
    return true, true
}
```

`time.After` 的 timer 在到期前不会被 GC；hook 走的是**每个 HTTP 请求**的路径。`internal/plugin/hook.go:319`。

```go
// internal/plugin/plugin.go:66
var maxHookBufferBytes = 32 << 20
// internal/plugin/hook.go:502
func (b *bufferedResponseWriter) bodyBytes() []byte {
    return append([]byte(nil), b.body.Bytes()...)   // 整份复制
}
```

配合 `hook.go:197` 的 `bw := newBufferedResponseWriter(w)`：一个注册了 response hook 的插件把每个匹配响应的前 32 MB 全留在 `bytes.Buffer` 里，`bodyBytes()` 再复制一份。建议上限 1–2 MB（超出走既有的 passthrough 分支，`hook.go:474-486`），buffer 池化，并让 `runResponseHook` 直接读 `b.body.Bytes()`（回调期间 body 不会再增长）。

### 3.5 F6/F7/F8/F9

- **F6**：`safe_conn.go:140-146`。`WriteJSON` 每帧 `var buf bytes.Buffer` + `json.NewEncoder`。`/api/clients`、V2 事件推送、终端控制帧都走它。建议 `sync.Pool` 复用 buffer，或直接 `json.Marshal` 到池化 `[]byte`。
- **F7**：`queryRawPointsBatch`（`raw_points.go:311-343`）在 `rawMu.RLock` 下遍历**所有** series（不只目标 metric），并对每个条目调 `spendReadBudget(ctx,0)`（一次 `ctx.Value` 查找）；`scanHotRollupGroup`（`batch_series.go:406-431`）、`scanCoarseRollupGroup`（`:439-483`）同理。1000 节点 × 21 系列 ≈ 21k 次/查询。建议在 `Store` 里维护 `map[string][]rawSeriesKey` 二级索引（写时维护），并把预算检查移到循环外（每 64 条检查一次）。
- **F8**：`Stats`（`store.go:1158-1207`）先 `scanRollupRowsBetween`（`rollup_query.go:165-192`）把整段窗口的行**全部**读进 `[]storedRollup`，每行 `digestFromRollup` 解码 t-digest（`:221-227`），再 `representativePoints` 构造点。全仓库无生产调用者（`grep '\.Stats\(' web internal` 只有 `upload.DefaultStore.Stats()`，是另一个类型），一旦被接回就是绕过预算的热点。
- **F9**：`web/filemanager/transfer.go:105-119` `rememberDownloadChunkSize` 只写；`:73-74` 声明。加删除或 TTL 清扫即可。

### 3.6 F14 agent 足迹：未发现新问题（明确结论）

逐项核对过长生命周期结构，均有限界或删除：
- `agent/server/file_stream.go:336-342` `pruneUploadLifecyclesLocked` 按 TTL 清扫；`:401-403` 删除 `uploadWriteLocks`；
- `agent/server/files.go:583,741-743` 删除 `uploadChunks`；
- `agent/server/websocket.go:336-356` `v2SeenEvents` 有 TTL + 数量上限 + 最旧淘汰；
- `agent/terminal/terminal.go:202-207` 关闭时删除 session；
- `agent/monitoring/netstatic/static.go:681-693` `Stop()` 停两个 ticker 并 `close(stopCh)`；
- `agent/dnsresolver/resolver.go:204-213` 的 `httpClients` 只按 resolver 配置数增长（常量级）。

74 处 `go func` 中未发现无界 spawn；15 处 `time.NewTicker` 中只有 3 个进程级循环（`agent/server/basicInfo.go:22`、`agent/server/unlock.go:31`、`agent/update/update.go:245`）没有 `Stop`——它们与进程同寿命，不构成泄漏。与 `docs/AGENT-FOOTPRINT.md` 的 15.8–24.3 MiB 实测一致；该文档列的“未测峰值/长跑磁盘增长”仍是缺口，但本次没有新增证据。

### 3.7 F10 coarse rollup 内存树（待验证）

```go
// pkg/metric/coarse_rollup.go:100
parent.children[childKey] = child.clone(true)   // 连 t-digest 一起克隆
```

封口条件（`:47`、`:232`、`:244`）：`bucket + interval + 10min <= now` 才移出内存。于是每个 series 在 `s.coarse` 里同时保留：

| 层 | 驻留窗口 | 子桶数/series | 子桶内容 |
|---|---|---|---|
| 5 min 父 | ~15 min | 3 父 × 5 = 15 | 1-min 桶 + digest（实测编码 231 B） |
| 1 h 父 | ~70 min | 2 父 × 12 = 24 | 5-min 桶 + digest（实测 983 B） |
| 24 h 父 | ~24 h10 min | 1–2 父 × 24 = 24–48 | 1-h 桶 + digest（实测 775–951 B） |

若按“in-memory centroid 数组 ≈ 编码大小同量级 + 结构体/map 键 ~400 B”估算：约 **55–70 KB/series**。9 节点 × 21 系列 ≈ 10–13 MB；100 节点 ≈ 130 MB；500 节点 ≈ 650 MB。这与“9 节点实测容器 RSS ~94 MB（`docs/PERFORMANCE.md`）”是自洽的（1 GB 容器足够），但节点数量级上去后会成为第一项。

**为什么没有直接测出来**：`addMinuteParents` 的封口判断用真实 `time.Now()`（经由 `hot_rollup.go:25/159` 的 `writePreparedHotRollups(..., time.Now().UTC(), ...)`），我的加速 harness 写入的是 27 小时前的时间戳，`eligible` 恒为空，所以 `s.coarse` 从未建立。要验证需要：给 `Store` 注入时钟，或在一台测试面板上跑 2 小时真实流量后读 `runtime.MemStats`（两种方案的对比：同一份流量下 4 层 tier vs 1 层 tier 的 HeapAlloc 差值）。

### 3.8 F15/F16 基线确认与 SQLite 常数

已跑的两个既有 benchmark（本机）：

```
BenchmarkRollupReadAccessPath/resolution_index  179120183 ns/op  197002 B/op  16347 allocs/op
BenchmarkRollupReadAccessPath/series_index        2743495 ns/op  197002 B/op  16347 allocs/op
BenchmarkReportParamsDecode/untyped_map_round_trip  15361 ns/op  7396 B/op  167 allocs/op
BenchmarkReportParamsDecode/raw_params               4446 ns/op  1632 B/op   13 allocs/op
```

即 `OPTIMIZATION-REVIEW-2026-09-22.md` C2（165.7 → 2.6 ms）与 C4（29.1 → 8.4 µs）的结论没有回归；这两个 benchmark 值得进 CI 作为回归门。

SQLite 常量（`internal/sqlitetune/connector.go:148-177`）：主库 `SetMaxOpenConns(1)` + cache 8 MB（`dbcore.go:245-250`、`:692-701`）；metrics 库 1 写 + 2 读，每个连接 cache 4 MB（`internal/metricstore/config.go:115-116`）→ 页缓存合计约 **20 MB 常量 RSS**。全仓库无 `ANALYZE`/`PRAGMA optimize`（`pkg/metric/maintenance.go:244` 只做 MySQL `OPTIMIZE TABLE`），rollup 读靠 `INDEXED BY` 提示绕开缺统计信息的问题（`pkg/metric/rollup_read_sql.go`）。

---

## 4. 与 OPEN-WORK 的关系

**新增（OPEN-WORK 未跟踪）**：F1、F2、F3、F4、F5、F6、F7、F8、F9、F10（待验证）、F16。

**补充证据（已跟踪，本次只是把量级/机制说清，不重复派活）**：
- `v2EventQueues` 只增不减：OPEN-WORK:70 / IMPROVEMENT-AUDIT-2026-10-13 §8.2。本次补充：`EnqueueV2Event` 每个事件还做一次 `close(q.signal); q.signal = make(chan struct{})`（`v2_events.go:102-103`）；`WaitV2Events`/`TakeV2Events` 对任意 uuid 都会 `getV2EventQueueLocked` 建条目（`:196`、`:227`）。
- `/api/clients` 每帧重新识别主体：OPEN-WORK:133 / 审计 §8.6。本次补充：每帧还有 `clients.HiddenClients()` 一次 DB 查询（`ws.go:60`）、两次 `config.GetAs`（`:56`）、`GetLatestReport()` 全量 map 复制（`:88`）和整队 JSON 序列化（`:103`）。
- 主库单连接（C3 deferred）：本文 F7/F16 的测量**不构成重开 C3 的证据**——没有观察到 pool 等待，只是把它的固有成本（所有 DB 操作串行）记下来。

**与 AGENT-FOOTPRINT 的关系**：其列出的缺口（峰值、Windows/macOS、长跑磁盘）本次仍未测，但“agent 侧没有新增常驻增长点”是本次的明确结论（F14）。

**未触碰**：`docs/OPEN-WORK.md` 的 D 系列决策、安全项、前端项。

---

## 5. 建议实施顺序

1. **F3（0.5 天）**：加 `IdleTimeout`——一行改动，先于一切内存优化，消除“空闲连接永久占用”这一类放大。
2. **F4 + F5（合计 1–1.5 天）**：`time.After` → `NewTimer/Stop`；hook 缓冲上限 32 MB → 1–2 MB。无插件部署零影响，有插件时是 32–64 MB/响应的风险。
3. **F2（1–1.5 天）**：定义缓存 + 写路径指纹缓存 + `normalized` 空 map。用本文的临时 benchmark 做改前/改后对照（147 KB/2,573 allocs → 目标 ≤70 KB/≤1,200 allocs）。
4. **F1（2–3 天）**：紧凑累加器 + 字典解码缓存；同时把 `maxMetricTotalPoints` 的“行预算”补成“字节预算”或降低上限。收益最大但要动查询热路径，需在前一步的基准和 CI 基线下做。
5. **F10 先测量（1 天）**：注入时钟或真实 2 小时流量，得到 `s.coarse` 的 B/series；若 ≥30 KB/series，再做“父桶浅化/按天落盘”的结构改动（2–3 天）。
6. **F6、F7、F9、F16（各 0.5 天）**：`WriteJSON` 池化、series 二级索引、`downloadChunkSizes` 清理、`PRAGMA optimize`。可与 3/4 并行。
7. **F8（0.5 天）**：删除 `Stats` 或补预算，避免以后接回时绕过保护。
8. **F15（0.5 天）**：把 `BenchmarkRollupReadAccessPath`、`BenchmarkReportParamsDecode` 纳入 CI（-benchmem + 阈值），否则本次和 C2/C4 的收益会随时间悄悄退化。

---

## 6. 附：测量方法与可复现命令

**仓库内既有 benchmark（不加 -count，避免跑全量测试）**：

```powershell
Set-Location C:\Users\SRTco\Nekomari
go test ./pkg/metric -run '^$' -bench BenchmarkRollupReadAccessPath -benchmem -count 1 -timeout 300s
go test ./web/api/client -run '^$' -bench BenchmarkReportParamsDecode -benchmem -count 1 -timeout 300s
```

**仓库外临时 harness**（`%TEMP%\nkbench`，独立 `go.mod` + `replace github.com/Aone2233/nekomari => C:/Users/SRTco/Nekomari`；`GOFLAGS=-mod=mod GOPROXY=off` 走本机 module cache；未向仓库写入任何文件）：

- 图表读：`SeriesBatch`，fixture = 3 小时 × 每分钟 × 9 实体 × N 指标；
- 写路径：`WriteBatch`（20 指标 × 9 实体 = 180 点/批）；
- 原始窗口：`QueryBatch`（10 分钟 × 14 指标 × 9 实体）；
- 插件 VM：`jsruntime.New(NodeJS:true)` × 8，GC 后读 `HeapAlloc` 与 `NumGoroutine`；
- t-digest 尺寸：`TDigest.Encode()` 长度（1 min=231 B、5 min=983 B、1 h=775 B）。

命令示例：

```powershell
Set-Location $env:TEMP\nkbench
$env:GOFLAGS='-mod=mod'; $env:GOPROXY='off'
go test -run '^$' -bench . -benchmem -count 1 -timeout 900s
go test -run '^$' -bench 'ChartSeries3h' -benchmem -memprofile mem.out -count 1
go tool pprof -top -alloc_space -nodecount=20 nkbench.test.exe mem.out
```

**未验证/边界**：
- 空闲连接单条内存（4–16 KB）是 Go `net/http` 的常量估算，未在本进程实测；
- coarse 树 B/series 是代码 + t-digest 尺寸的推算，需可注入时钟后实测（§3.7 已说明失败原因）；
- 本机为 Windows/amd64，Linux/arm64 的分配数字可能略有差异，但热点归因与量级不变；
- 未跑全量 `go test ./...`（按任务约束留给 Lead）。
