# Nekomari 改进审查 — 2026-10-18

**对象。** `C:\Users\SRTco\Nekomari`，`main` @ `b48963c`（v1.6.12 之后一个文档提交）。工作区原有改动：`M README.md`、`?? docs/OC424-MTPROTO.md`、`?? topology/`；本次审查**未修改任何源文件**，只新增 `docs/audits/2026-10-18/`。
**范围。** 内存/资源、性能、功能完整性。三份领域报告与本汇总同目录：

| 报告 | 内容 |
|---|---|
| [backend-memory.md](./backend-memory.md) | Go 服务端内存/分配实测（临时 benchmark 在仓库外） |
| [frontend-perf.md](./frontend-perf.md) | 首访体积、轮询、渲染、PWA/RPC2 |
| [feature-gaps.md](./feature-gaps.md) | 功能缺口与已交付功能的闭环问题（20 条） |

**基线（本次实跑）。** `go build ./...`、`go vet ./...` 通过；`go test ./...`（server）与 `agent` 全绿；前端 `npm test` 140/140、`lint` 0、`audit:compiler` 0、`chart-a11y` 6/6。三份报告的结论都建立在“代码已经是绿的”这一前提上。

---

## 0. 一句话结论

这个 fork 的工程质量与文档密度明显高于一般同类项目：构建、测试、查询预算、保留策略、敏感操作门、pprof 页面都已存在。**剩余改进不在“补功能”，而在三类“已做但没做完整”的地方**：

1. **常驻内存还有一块很大的固定成本**：内嵌默认主题在启动时被完整解压进堆（实测约 **33–34 MB 常驻**、启动瞬时约 **70 MB**），其中 **8.26 MB 是逐字节重复的 admin 副本**。
2. **几条热路径的分配/瞬时内存与当前预算不匹配**：写入每点约 817 B 分配；单次图表查询分配上限可达约 **133 MB**（预算按“行数”而非“字节”计）；`http.Server` 缺 `IdleTimeout`。
3. **有 5 个已交付页面没有任何入口，2 个安全敏感 RPC 没进敏感操作门，维护窗口只做了一半**——这些是 ROI 最高、工作量最小的部分。

---

## 1. 内存优化（按收益/风险排序）

### M1（P1，S–M）内嵌主题在启动时整体解压进内存：约 34 MB 常驻 + 8.26 MB 重复副本【本次实测】

- 证据：`web/public/public.go:237-244` 的 `init()` 调 `loadEmbeddedDist()`；`web/public/embedded.go:20-66` 用 `zstd.DecodeAll` 解出整份 tar，再逐文件 `io.ReadAll` 进 `map[string][]byte`（`defaultDistFiles`）。`web/public/public.go:474-500` 每次静态请求都从这个 map 取内容。
- 实测 `web/public/defaultTheme/dist.tar.zst`：压缩 **5.14 MiB**；解压后 tar **33.7 MiB / 1,062 个文件 / 32.95 MB 文件内容**；`DecodeAll` 耗时 **41.7 ms**；加载期间累计分配 **251 MB**；保留 tar 时堆增量 **70.2 MB**（生产代码释放 tar 后稳态约 **33–34 MB**）。
- 重复：归档里 `admin/`（248 个文件，**8.26 MB**）与 `standalone/admin/`（248 个文件，**8.26 MB**）**逐字节相同**；另外 `standalone` 非 admin 部分 7.23 MB、`assets` 9.2 MB。
- 影响：参考部署文档记录的容器内存 94 MB 中，约 **36%** 是这块常驻；与当前主题是否启用无关（它是所有主题的回退资源）。1000 个并发静态请求共享同一份字节，不会按请求放大，所以问题在“固定成本”而不在“泄漏”。
- 改法（可组合）：
  1. **去重（S，先做）**：打包时不再写 `standalone/admin/**`，`openAsset` 把 `/standalone/admin/<p>` 重写到 `admin/<p>`（两处内容完全相同，前端测试 `standalone_pages_test.go` 需要同步断言）；内存与归档各省 **8.26 MB / 约 1.4 MiB 压缩**。
  2. **按需解码（M）**：把归档拆成 core（`assets` + 根文件，约 9 MB）、admin、standalone 三份，或首次访问某组时才解压；普通访客稳态从 33 MB 降到约 9–10 MB。
  3. **磁盘缓存（M，收益最大）**：启动时把归档解到 `data/.theme-cache/<version>/`（33 MB 磁盘），静态请求走 `os.Open` + `ServeContent`；RSS 几乎归零，失败时回退到当前内存 map。代价是 33 MB 磁盘与一次 42 ms 解压。
- 验收：`runtime.MemStats.HeapAlloc` 在只加载首页后不包含 33 MB 级别增量；`/themes/<id>/…`、`/admin/**`、`/standalone/**` 全部 200 且缓存头不回归。

### M2（P1/待验证，M）coarse rollup 内存树可能随节点数线性增长

- 证据（backend-memory 报告 §3.7）：`pkg/metric/coarse_rollup.go:100` 对每个父桶的每个子桶 `clone(true)`（连 t-digest 一起复制）；封口条件是“桶结束 + 10 min ≤ now”（`:47`），因此 **24h 父桶几乎整日驻留**，一个 series 的 24h 父桶会累积最多 24 个 1h 子桶副本。
- 估算：按实测 t-digest 尺寸（1 min 桶 231 B、1 h 桶 775–983 B）约 **55–70 KB/series** → 9 节点 × 21 系列 ≈ **10–13 MB**；100 节点 ≈ **130 MB**；500 节点 ≈ **650 MB**。**这是代码推断，尚未实测**：现有 harness 无法加速时钟（`hot_rollup.go:25/159` 用真实 `time.Now()`）。
- 改法：先注入时钟（或接 2 小时真实流量 + pprof heap）确认；若成立，已封口的 1h 子桶应在合并进 24h 父桶后立即从父桶的 `children` 删除（晚到样本只在 10 min 宽限内可能改写），把 24h 父桶从“最多 24 个全量副本”降为“1 个聚合桶 + 少量未封口子桶”。预计工作 2–3 天。

### M3（P1，M）读取预算按“行数”封顶，最坏单请求瞬时分配约 133 MB

- 证据：`web/rpc/jsonrpc/budget.go:12-14` 允许 25 万“samples/rollup rows/dictionary entries”，`budget.go:84` 把它作为读预算；实测 3 h × 14 指标 × 9 节点 = 22,680 行时分配 **12.1 MB / 71,347 allocs**（约 **534 B/行**，backend-memory 报告 §3.1）。按上限折算单请求瞬时分配最高约 **133 MB**；公共图表并发槽是 4（`budget.go:37`），理论上可叠加到数百 MB。
- 改法：把预算从“行数”改成“估算字节数”（每行 534 B + 字典项固定成本），或对单请求内存设上限（超限返回“缩小窗口/节点数”）；同时让 `needDigest=false` 的聚合走紧凑累加器。配合 M8 的 `GOMEMLIMIT` 能避免 OOM-kill 而不是 GC 抖动。

### M4（P2，S–M）写入路径：每点约 817 B 分配，两个确定性热点

- 实测（backend-memory 报告 §3.2）：`WriteBatch` 180 点 = **147 KB / 2,573 allocs**；pprof：`filterDisabledMetricPoints → ListMetrics` 约 **29%**、`tagsFingerprint`（每点对 tags 与 labels 各一次 JSON + SHA256 + hex）约 **28.8%**、`Point.normalized` 为 nil map 各分配空 map 约 **12.9%**。
- 代码证据：`pkg/metric/store.go:922-942` 每次写批全表 `ListMetrics`；`pkg/metric/raw_points.go:120-147` 每点调两次 `tagsFingerprint`；`pkg/metric/json.go:76-83` 是 JSON+SHA256+hex；`pkg/metric/types.go:258-266` 对 nil 也建空 map。
- 改法：Store 内缓存指标定义（`CreateMetric/UpsertMetric/DeleteMetric/UpdateMetricRetention` 失效，或挂 `dbcache.Revision`）；按 `(metric, entity, tagsJSON, labelsJSON)` 缓存指纹（写路径上标签集合稳定）；nil map 不分配，直接按“空”处理。预计砍掉该路径 **50–60%** 分配和每批一次全表查询（1–1.5 天，可用现有 benchmark 回归）。

### M5（P2，S）插件 hook：32 MB 缓冲 + 再复制一份 + 每请求一个 30 s 计时器

- 证据：`internal/plugin/plugin.go:66` `maxHookBufferBytes = 32 << 20`；`internal/plugin/hook.go:502-506` `bodyBytes()` 整份复制；`hook.go:316-321` 每次 hook 用 `time.After(timeout)`（默认 30 s，请求提前完成后计时器仍活到超时）。若插件 hook 了 `/*`，单个大响应峰值约 **64 MB**，并发即倍数；无插件时零成本。
- 改法：上限降到 1–2 MB（超出走已有 passthrough 分支，`hook.go:474-486`），`time.NewTimer` + `Stop`，`runResponseHook` 直接读 `b.body.Bytes()` 不再复制。

### M6（P2，S）小项：几个只增不减的 map

- `web/filemanager/transfer.go:73-119` `downloadChunkSizes` 只写不删（每个曾下载的 uuid 一条）→ 节点删除时 delete 或 TTL 清扫。
- `web/agent/connections.go:19-30,125` `presenceOnly` 的过期项只被跳过、不被删除 → 节点反复增删时留下过期条目（每条几十字节，量级很小）。
- `web/agent/v2_events.go:31-41` `v2EventQueues` 每 uuid 常驻（**OPEN-WORK 已跟踪**）；补充：每个事件还有一次 `close(signal)+make(chan)` 分配。
- `web/agent/connections.go:16,180-210` `recentReports` 保留最近 1 分钟（默认 3 s 上报 ⇒ 每节点约 20 条完整 Report，含磁盘/GPU 切片）。9 节点很小，1000 节点时按每条约 1–3 KB 估算可达 **40–80 MB**；若目标是大舰队，建议只保留 `getClientRecentRecords` 实际用到的字段或缩短到 30 s。

### M7（P2，S）主 `http.Server` 没有 `IdleTimeout`

- 证据：`internal/server/runtime.go:141-145` 与 `internal/server/guide_server.go:60-64` 都只有 `ReadHeaderTimeout`；全仓库无其他 `IdleTimeout/ReadTimeout/WriteTimeout`。Go 的默认 `IdleTimeout=0` 会回退到 `ReadTimeout=0`，即 **keep-alive 空闲连接永不回收**：每条空闲连接常驻 1 个 goroutine + 读写缓冲（约几 KB），公网面板/反代连接池会持续占住。
- 改法：`IdleTimeout: 120 * time.Second`（不要加 `WriteTimeout`，会砍断下载/流；WebSocket 升级后由 gorilla 接管，不受影响）。

### M8（P2，S）没有内存上限意识（`GOMEMLIMIT` / cgroup）

- 全仓库无 `debug.SetMemoryLimit` / `GOMEMLIMIT` / cgroup 读取。Go 的 GC 默认按“存活堆 ×2”设目标，容器 `-m 1g` 或 systemd `MemoryMax` 对它不可见；在 M3 的峰值查询或 M2 的舰队规模下，进程会先被 OOM-kill 而不是提前加压 GC。
- 改法：启动时读 `/sys/fs/cgroup/memory.max`（或 `KOMARI_MEMORY_LIMIT` 环境变量），`debug.SetMemoryLimit(limit*0.9)`，并在 pprof 页显示当前上限；对“明明有内存却 OOM”的现场是低成本保险。风险：设得过低会让 GC 空转，必须可关闭/可覆盖。

---

## 2. 性能优化

### P1（P1，M）面板自身不压缩任何 JS/CSS：自托管首访约 1.7 MB，压缩后约 0.4 MB

- 证据：`web/public/public.go:121-168` `serveAsset` 只设头后 `http.ServeContent`；`web/**/*.go` 无响应压缩中间件；`deploy/nginx-nekomari.conf` 没有 `gzip on`；而 Dockerfile 直接 `EXPOSE 25774`，自托管用户常常不带压缩代理。`docs/PERFORMANCE.md` 已记录“面板不压缩，首访 1669 KiB”，但未列为待办。
- 实测（frontend-perf 报告 §F2）：本机静态服务模拟 Go 面板，首访 **1703 KiB**（JS 931 KiB/49 请求、CSS 763 KiB/2）；文件级 gzip 后 entry 263 KiB、CSS 95 KiB，首访约 **405 KiB gzip（−76%）**；Cloudflare 后的用户当前由 CF 压缩，所以这条主要打“直连/自托管 nginx 没开 gzip”的场景，而参考部署正属于后者。
- 改法：A) Go 层 gzip 中间件，只压 `text/*`、`application/javascript`、`application/json`、`image/svg+xml` 且 >1 KiB 的响应，`Vary: Accept-Encoding`，Range 请求走 identity；或 B) 构建期生成 `.gz/.br` 由 `serveAsset` 按 `Accept-Encoding` 直发（运行时 CPU 为零，但要改归档与测试）。注意不要破坏 `ETag/immutable`、`sw.js` 的 no-store 与 Range 语义。

### P2（P1，M）i18n：5 种语言全部静态进 entry，首访多下约 100 KiB gzip

- 证据：`frontend/src/i18n/config.ts:3-11` 静态 import 5 份 locale 并同步 `init`；实测 5 份合计 **407 KiB raw / 125 KiB gzip**，entry 为 **785 KiB raw / 263 KiB gzip**，其中 i18n 占 **24%**；`sla/bulk/config/forecast/maintenance` 五个 standalone 入口各自也 import 同一份 config，同样背着 5 种语言。
- 改法：只同步保留 `en`（fallback），其余 4 份改动态 `import()`；检测到语言后 `addResourceBundle` 再渲染（index.html 已有静态骨架，不会白屏）；语言菜单改为常量列表而不是 `Object.entries(resources)`。预计 entry **−约 100 KiB gzip（−38%）**，5 个 standalone 页同享；工作量 1–2 天。

### P3（P1，S）公开首页为“悬停才用”的迷你图首访加载整个 recharts

- 证据：`frontend/src/components/Node.tsx:272` 静态 import `MiniPingChartFloat`，而 `MiniPingChartFloat.tsx:82-89` 只在 `open` 时才渲染 `MiniPingChart`（后者静态引 recharts）。首访闭包里已有 `chunk-metricSeries-*.js`：**363 KiB raw / 107 KiB gzip**（recharts 占该 chunk 约 2/3）。
- 改法：`Node.tsx` 对 `MiniPingChartFloat` 用 `React.lazy` + `Suspense`（或在 Float 内部 lazy 图表）。首访 **−107 KiB gzip / −363 KiB raw**，代价是首次悬停多一次 chunk 加载；0.5 天，风险极低。

### P4（P1，S）公开首页轮询叠加 + 每节点一个 60 s 定时器

- 证据：`frontend/src/contexts/LiveDataProvider.tsx:8` 每 **2 s** 拉全节点状态；`frontend/src/pages/Index.tsx:148` 又每 **5 s** 拉一次 `common:getNodes`（只含名称/价格等静态字段）；`frontend/src/components/PriceTags.tsx:42-44` 每个节点卡片各起一个 60 s interval，只为刷新“剩余天数”。
- 影响：可见标签页约 0.7 req/s，其中 5 s 那路绝大多数请求无变化；N 节点 = N 个 timer + N 次/分重渲染（`now` 变化必然绕过 memo）。
- 改法：`getNodes` 5 s → 60 s（或只在 visibility/节点数变化时刷新，实时状态已由 2 s 那路覆盖）；`PriceTags` 的 `now` 提升为单例 ticker/共享 store。该路请求 **−92%**，50 节点时消除 50 个 timer。

### P5（P2，S–M）实例页图表每 2 s 在 render 体内全量重算

- 证据：`frontend/src/pages/instance/LoadChart.tsx:1549-1572` 在 `charts.map()` 内直接调用 `buildRowsFromMetricSeries / mergeBuiltChartData / prepareChartData / applyMetricEwma / trimMetricChartBoundaryRows` 等，没有 `useMemo`；实时数组每 2 s 换新（150 点），历史 700 点/图、最多 3 图。估算 5–15 ms/次（**待 profiling**）。
- 改法：把每个 chart 的派生数据移进 `useMemo`（依赖 chart/数据版本），或拆 `React.memo` 单图组件；用现有 browser spec 回归。

### P6（P2，S）RPC2 重连固定 3 s × 5 次、无退避，15 s 后永久放弃

- 证据：`frontend/src/lib/rpc2.ts:46-58,551-564`。面板重启 >15 s 后 WS 不再重连，此后每 2 s 的调用都退化为 HTTP POST，直到刷新页面。
- 改法：指数退避 + 抖动（0.5 s → 30 s 封顶）、仅在可见时重试、恢复后重置计数。工作量 0.5 天。

### P7（P2，S）SW 静默自动更新 + chunk 哈希每版全变 → 打开中的标签页可能加载到 404 的旧 chunk

- 证据：`frontend/vite.config.ts:84` `registerType: "autoUpdate"`，产物 `sw.js` 立即 `skipWaiting/clientsClaim` 并清理旧预缓存；`PWAUpdatePrompt.tsx:1-4` 直接返回 null；`OPEN-WORK` 也记录“重建即可变 143 个 chunk 名”。部署后仍开着的页面，下一次懒加载路由可能白屏。
- 改法：改回 `prompt` 并恢复“有新版本，刷新”提示；或在服务端保留上一版 dist；至少给懒加载失败加一次重试 + 整页 reload 兜底。

### 其他（记为补充，不作为新发现）

- PWA 预缓存：实测 **164 条 / 4.40 MB raw / 1.34 MB gzip**；`ROADMAP` B1 已明确“预缓存策略不是杠杆（一次访问实际 480 KiB）”，本报告不重复提。
- 首访 49 个 JS 请求、238 个 JS 文件（42 个 <0.5 KiB）：请求数可通过少量 manualChunks 收敛，但 `vite.config.ts:230-235` 写明这是有意取舍；仅在 HTTP/1.1 自托管时值得重估（**待验证**）。
- 默认主题的 Monaco 编辑器 chunk（3 MB raw）只出现在 admin/standalone 产物、且已排除出预缓存；按 `ROADMAP` §G 不作为新项。

---

## 3. 功能优化（已交付功能的“最后一公里”）

> 完整 20 条见 [feature-gaps.md](./feature-gaps.md)；以下为已由 Lead 独立复核的关键项。

### F0（P0，S）两个安全敏感 RPC 不在敏感操作门内【已复核】

- 证据：敏感名单只有 3 条（`web/rpc/jsonrpc/sensitive_stepup_test.go:15-19`：`admin:dbExec`、`admin:dbQuery`、`admin:exec`），`web/rpc/jsonrpc/transport.go:51-63` 只对 `rpc.IsSensitive(method)` 的方法追加 TOTP 校验。`admin:exportConfig`（`web/rpc/jsonrpc/admin.configxport_apply.go:29-36`，`include_secrets=true` 时导出全部 agent token 与通知凭据）与 `admin:rollAgentUpdate`（`admin.agentupdate.go:35`，可让整队 agent 安装任意已发布版本）**从未登记**。
- 影响：一个被盗的、已通过 admin 角色的会话（即使账号启用了 2FA）不需要 TOTP 就能导出全部节点凭据或整队替换 agent 二进制；而 `admin:exec` 已经要求。同类还包含 `admin:getClientToken`（返回单个 agent token），是否纳入属于策略决定。
- 改法：两处 `rpc.MarkSensitive` + 允许名单测试加两条 + 导出页在 `include_secrets` 为真时收集验证码。注意：`OPEN-WORK` 已记录“API key 豁免敏感 2FA”，该豁免会让这条修复对 API key 无效，需同一批做策略决定。

### F1（P1，S）H1–H5 五个已交付页面没有任何入口【已复核】

- 证据：`frontend/src/config/menuConfig.json` 无 `standalone` 条目（全文 180 行）；`frontend/src` 里 `/standalone/` 只出现在 4 个 entry 的注释；`theme-luminaplus/src` 0 命中；而产物确有 `/standalone/{sla,bulk,config,forecast,maintenance}/<name>.html`，且有 Go 契约测试保证“内嵌、自包含”，但没有测试断言“可到达”。
- 影响：SLA 报表、批量编辑、维护窗口、配置导入导出、流量预测五个功能实际上只能手打 URL 或直接调 RPC；`admin:agentVersions/rollAgentUpdate`（H6）更是完全无 UI。这正是 `ROADMAP` H0 记录的“功能上线但不可达”在新页面上的复现。
- 改法：在 `menuConfig.json` 增加 5 条（`newTab: true`），并加一条“菜单引用 == standalonePages 契约测试里列出的页面”的测试，把可达性变成会变红的断言。

### F2（P1，S）维护窗口只抑制离线告警，ping/负载告警照发【已复核，与 H3 验收矛盾】

- 证据：`utils/notifier/offline.go:95` 有 `For(now, clientID)` 判断；`utils/notifier/load.go:63-106` 的 `executeLoadNotificationTask` → `sendLoadNotification`（`:305-323`）没有任何维护判断，而 ping 规则也走同一出口（`:74-78`）。`ROADMAP.md:368-381` 的 H3 验收写的是“offline **and ping** notifications are suppressed”。
- 改法：在 `sendLoadNotification` 按 client 过滤 `For/Decide`（点位告警只抑制、不延迟），补一条窗口内 ping 规则不发送的测试。0.5 天。

### F3（P1，M）维护窗口不进节点状态、也不进 SLA【已复核】

- 证据：`web/rpc/jsonrpc/latest_status.go:40-41,67` 只有 `Online bool`；`internal/sla` 无任何 maintenance 引用。`ROADMAP` H3 验收“status page distinguishes maintenance from down”未满足：计划重启在状态页显示为 down、在 SLA 里被计为丢包/事件。
- 改法：状态输出增加 `maintenance`（含窗口结束时间/原因），前端加徽标；SLA 查询排除窗口内桶（需要先定义“窗口覆盖多少算排除”并写测试）。1–3 天。

### F4（P1，M）H6 的“钉版本”不持久，回滚会在 agent 重启后静默失效【已复核】

- 证据：`agent/update/target.go:35-41` 的注释称“面板每次 update 事件都发，连接时也发，所以重启能重新学到”，但面板侧下发点只有 `web/rpc/jsonrpc/admin.agentupdate.go:156` 一处；全仓库没有“把 target 存库并在连接时重发”的实现。agent 只把它放在内存 `atomic.Value`（`:41-59`）。
- 链式后果：指定回滚到旧版本 → agent 安装后 `os.Exit(42)` 重启 → `RequestedVersion()` 为空 → 回到“跟随最新” → 下一次自更新又升到最新；面板只显示“dispatched”。
- 改法：面板为每个节点持久化 target（新列或 config 键），在 v2 连接建立/拉取事件时重发；注释与实现二者必须一致。1–3 天。

### F5（P1，S）节点表勾选后没有任何批量动作【已复核】

- 证据：`frontend/src/pages/admin/index.tsx:42,77,87-88` 只把 `selectedNodes` 传给表格；`NodeTable.tsx:96,281-288` 只维护勾选与“全选”；批量动作都在独立的 `pages/bulk/index.tsx`。`CHANGELOG` v0.1.36 描述的“Multi-select the node table”流程并不存在。
- 改法：选中后显示批量操作条，跳转 `/standalone/bulk/bulk.html` 并带上 uuid；或暂时移除勾选框，避免误导。0.5 天。

### F6（P1–P2）其他“差一点就完整”的能力

| 项 | 证据 | 建议 | 工作量 |
|---|---|---|---|
| 大盘/告警用的 uptime 无历史 | uptime 已上报并实时展示（`latest_status.go:41,67`；`Node.tsx`），但指标定义无 `system.uptime`；`OPEN-WORK` 对应条目措辞已过时 | 增加 gauge 写入，用下降沿推导重启次数 | S–M |
| 预测只有流量没有成本 | `internal/forecast/forecast.go` 无 price/currency | 用 `clients.price/billing_cycle/currency` 加 `cost/overage` 列 | S–M |
| 审计日志无筛选/导出 | `admin.system.go` 支持 `msg_type`，前端只传 `limit/page` | 传 `msg_type` + 时间/操作者筛选，加 CSV | S |
| 远程执行无历史入口 | 后端有 `getTasks/getTaskResultsByTaskId`，前端 0 命中 | 执行页加历史任务列表 | S–M |
| 无指标/记录 CSV 导出 | 全仓库无 CSV 输出（除配置导出） | 新增限行流式导出 RPC | M |
| CPU 温度、磁盘 I/O 缺失 | 指标定义无 temp/diskio；agent 无 hwmon/diskstats | agent 采集 → 新指标 → 图表（**需发版**） | L |
| HTTP 探测只有延迟，无状态码/证书到期 | `agent/server/task.go:519-522` | 上报 status/tls_expiry → 指标 → 告警（需发版） | L |
| 单账号、无用户/角色 CRUD | 无用户 RPC/页面；`models.User` 无角色 | 要么加 admin/viewer，要么明确记录“单账号设计” | M–L |

---

## 4. 与现有 OPEN-WORK 的关系

`docs/OPEN-WORK.md` 的绝大多数条目仍然有效（token 轮换、integrity_check、`common:getRecords` 无界 `hours`、`v2EventQueues`、ipinfo 空国家 48 h、SQLite 池等待计数器、主题/插件两套解压标准等）。本次**新增**的主要是：

- 内存：M1（内嵌主题 eager decode + admin 重复）、M8（GOMEMLIMIT）、M5 的定时器/缓冲细节、M3 的“按字节预算”建议。
- 性能：P1（面板零压缩）、P2（i18n 全量静态打包）、P3（recharts 首访）、P4（轮询叠加）、P7（SW 更新与旧 chunk）。
- 功能：F0（敏感操作门缺口）、F1（五个页面不可达）、F2/F3（维护窗口只做一半）、F4（H6 不持久）、F5（勾选无动作）。
- **建议升级**：OPEN-WORK 的“v2EventQueues 不删除”与“`/api/clients` 每帧重识别”可以并入下一批内存/性能小修（都有现成改法）；“SQLite 连接池无等待计数器”在 M3 改成字节预算之前仍可暂缓。

---

## 5. 建议路线图（按“风险最低、感知最强”排序）

**批次 A — 一天级、低风险、可单独发版（建议 v1.6.13）**
1. F0：`exportConfig(include_secrets)`、`rollAgentUpdate` 加 `MarkSensitive` + 名单测试（S）。
2. F1：`menuConfig.json` 加 5 条入口 + 可达性测试（S）。
3. F2：`sendLoadNotification` 加维护窗口过滤 + 测试（S）。
4. P1：Go 层 gzip 中间件（或预压缩）+ 压缩语义测试（M）。
5. P3：`Node.tsx` 的 `React.lazy`（S）。
6. M7：`IdleTimeout: 120s`（S）。
7. M1.1：归档去重 admin 副本（S）。

**批次 B — 一个迭代、中风险（建议 v1.6.14）**
1. M1.2/M1.3：内嵌主题按需/磁盘缓存（M，最大内存收益）。
2. P2：i18n 按语言懒加载（M）。
3. M4：写路径定义缓存 + 指纹缓存 + 空 map（M，可 benchmark 回归）。
4. P4/P5：轮询合并、`PriceTags` 单 ticker、LoadChart `useMemo`（S–M）。
5. P6/P7：RPC2 退避、SW 更新提示（S）。
6. F4：H6 目标持久化 + 连接时重发（M）。
7. F3：维护窗口进状态页/SLA（M）。

**批次 C — 需要测量或动 agent**
1. M2：coarse rollup 注入时钟实测，再决定重构（测量 1 天 + 改 2–3 天）。
2. M3：读取预算改为字节预算（M）。
3. M5/M8：插件 hook 收紧、`GOMEMLIMIT`（S）。
4. F6 中的 uptime 历史、成本预测、CSV 导出、执行历史、日志筛选（逐项 S–M）。
5. CPU 温度/磁盘 I/O、HTTP 状态码/证书到期（需 agent 发版，归入下一次 fleet 变更）。

---

## 6. 方法与限制

- **实跑**：`go build ./...`、`go vet ./...`、`go test ./... -skip '^TestIpInfo$|^TestIpApi$|^TestGeojs$'`、`(cd agent && go test ./... -skip 'TestICMPPing|TestTCPPing|TestHTTPPing')`；`BenchmarkRollupReadAccessPath`、`BenchmarkReportParamsDecode`（`-benchmem`）；用独立 Go 程序解压 `defaultTheme/dist.tar.zst` 统计文件数/字节/重复/解码耗时/堆增量；Node 脚本统计 `frontend/dist` 体积、gzip、SW 预缓存条目；三份领域报告另有其 own 的 benchmark/浏览器/脚本证据。
- **未验证**：M2 的 coarse rollup 实际 RSS（需可注入时钟或真实流量）；M3 的 133 MB 是“预算上限 × 实测每行成本”的算术，不是抓到的生产峰值；P5 的 5–15 ms 是估算；HTTP/1.1 下 49 请求的影响；任何需要真实浏览器/生产流量的视觉与交互结论。
- **原则**：本报告只列“代码里能证明的”，推理处都标注“待验证”；没有把 `ROADMAP` B1/§G 已定价的预缓存、Monaco chunk、CSS 体积重新包装成新发现。

---

## 附：三份领域报告的阅读顺序

1. [feature-gaps.md](./feature-gaps.md) — 先看，因为 F0/F1/F2 是投入产出比最高的。
2. [backend-memory.md](./backend-memory.md) — 内存/分配数字与 benchmark 命令。
3. [frontend-perf.md](./frontend-perf.md) — 首访体积、轮询、渲染细节。
