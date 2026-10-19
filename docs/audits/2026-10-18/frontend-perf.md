# 前端性能 / 渲染审计（2026-10-18）

- 仓库：`C:\Users\SRTco\Nekomari`，版本 v1.6.12（HEAD `b48963c`）。审计方式：只读源码 + 对已构建产物 `frontend/dist*` 与 `web/public/defaultTheme/dist.tar.zst` 实测。
- 构建产物与 HEAD 同批次：`frontend/bundle-analysis.html`（mtime 2026-10-17）与 `frontend/dist` 的 chunk 哈希一致（`entry-index-BU_mihmi.js`），下述数字均为该构建实测，不是估算。
- 按 Lead 指示、并因 `docs/ROADMAP.md` B1/§G 已定价，本次**不把** Service Worker 预缓存策略、编辑器 chunk（Monaco 3 MB）、CSS 体积当作新发现；相关实测只在 §3 作为补充证据列出。

---

## 1. 结论摘要（Top 5，按性价比排序）

| 排名 | ID | 结论 | 严重度 | 首访/运行时收益（实测） | 工作量 | 风险 |
|---|---|---|---|---|---|---|
| 1 | **F1** | **5 种语言全部静态打进 entry（及其后每个 standalone 页）**：entry 785 KiB raw / 263 KiB gzip，其中 5 个 locale JSON 合计 407 KiB raw / **125.0 KiB gzip**（en 23.3、ja_JP 27.1、zh_CN 25.3、zh_TW 25.4、id_ID 23.8），5 份键集 1467 键完全对齐（`npm test` 的 i18n parity 4 条通过）。改成“en 同步 + 当前语言动态 import”即可 | P1 | entry **−约 100 KiB gzip（263→约 163，−38%）**；5 个 standalone 页同样各 −约 100 KiB gzip（如 `sla.js` 230 KiB gzip 中约 43%） | M（1–2 天，含 5 个入口 + 语言菜单） | 中低：首帧需等语言 chunk（可先渲染 HTML 静态骨架） |
| 2 | **F2** | **面板自身不压缩任何 JS/CSS**：`serveAsset` 只设头后 `http.ServeContent`，`web/**/*.go` 无 gzip/brotli 中间件，参考部署 `deploy/nginx-nekomari.conf` 也没有 `gzip on`。本机静态服务（与 Go 面板行为一致）实测**首访 1703 KiB**（JS 931 KiB/49 请求、CSS 763 KiB/2、API 6、HTML 3），最大两项 `entry 786 KiB`、`index CSS 763 KiB` 均无 `Content-Encoding` | P1 | **1703 KiB → 约 405 KiB gzip / 约 340 KiB brotli**（按文件级实测：entry 263 gz/202 br、CSS 95/60），单次首访省约 **1.3 MB**；直连/自托管场景全额受益 | M（1–2 天 + `web/public` 测试） | 中：Range/ETag/immutable 语义要一起测 |
| 3 | **F3** | **公开首页为“悬停 3 秒才出现”的迷你图，首访就下载整个 recharts**：`Node.tsx:272` 静态 `import MiniPingChartFloat`，而 `MiniPingChartFloat.tsx:82-89` 只在 `open` 时才渲染 `MiniPingChart`（后者 `MiniPingChart.tsx:3` 引 recharts）。首访请求闭包里 `chunk-metricSeries-*.js` = **363 KiB raw / 107 KiB gzip**（recharts 占该 chunk 669/1000 KiB，另含 d3/es-toolkit/@reduxjs） | P1 | 首访 **−107 KiB gzip / −363 KiB raw（约首访 JS raw 的 39%）**；代价是首次悬停时多一次 chunk 加载 | S（0.5 天，两处 `React.lazy`） | 极低 |
| 4 | **F4** | **公开首页两条轮询叠加，且静态元数据 5 s 一次**：`LiveDataProvider.tsx:8,52` 每 2 s 拉 `common:getNodesLatestStatus`（全节点状态），`Index.tsx:148` 又每 5 s 拉 `common:getNodes`（只含名称/价格等静态字段）；另 `PriceTags.tsx:42-44` 每个节点卡片各起一个 60 s interval（N 节点 = N timer + N 次/分重渲染） | P1（浪费确定，不影响正确性） | 可见标签页 0.7 req/s；把 `getNodes` 5 s→60 s 可令该路请求 −92%；50 节点时消除 50 个 timer/50 次每分重渲染 | S（0.5 天） | 低（新节点最多晚 60 s 出现） |
| 5 | **F5** | **实例页图表每次 2 s 实时 tick 在渲染体里重算全部图表数据**：`LoadChart.tsx:1549-1572` 在 `charts.map()` 内调用 `buildRowsFromMetricSeries` / `mergeBuiltChartData` / `prepareChartData` / `applyMetricEwma` / `trimMetricChartBoundaryRows` / `metricChartBoundaryTicks`，而 `data` prop 每 2 s 换新数组（`instance/index.tsx:142-167`，150 点 FIFO；历史最大 700 点/图，3 图） | P2 | 每次 tick 的 O(points×series×charts) 重算与对象分配消失；**假设** 5–15 ms/帧，低端机更差（待 profiling 验证） | S–M | 低 |

补充（不在 Top 5，但值得进同一批）：**RPC2 重连无退避且 15 s 后放弃**（F6）、**SW 静默自动更新 + chunk 哈希每版全变导致打开中的标签页可能 404**（F7）、**首访 49 个 JS 请求的 chunk 碎片化**（F8）、**内嵌主题里 admin 被打包两份（zstd 实测多 1.40 MiB）**（F9）。

---

## 2. 详细发现

测量方法（全部在本机实跑；Node v22.23.2 / npm 12.2.0 / Python 3.14 + Playwright Chromium）：

```powershell
# 体积：对每个文件 raw / gzip(level 9) / brotli(q11) 逐一实测（Node zlib）
# 首访：python -m http.server 8799 --directory frontend/dist（= 面板 Go 服务行为：不压缩）
#       再跑 frontend/script/first-visit-transfer.py http://127.0.0.1:8799 1
# 预缓存：解析 frontend/dist/sw.js 的 precacheAndRoute([...]) 数组
# 主题归档：Node zstd 解压 dist.tar.zst -> 解析 tar 头 -> 逐文件 sha1 比对
```

### F1 — i18n：5 种语言全部静态进入 entry 与每个 standalone 页面（P1）

- 证据：`frontend/src/i18n/config.ts:3-11` 静态 import 5 个 locale：

```ts
import en from "./locales/en.json";
import zh_CN from "./locales/zh_CN.json";
import zh_TW from "./locales/zh_TW.json";
import ja_JP from "./locales/ja_JP.json";
import id_ID from "./locales/id_ID.json";
```

  `config.ts:17-88` 把 5 份 JSON 全部塞进 `resources` 并**同步** `init()`（`:71-87`）；`App.tsx:12` 再 import 一次（同一模块，不重复）。
- 实测体积：5 个 locale 合计 407 KiB raw / **125.0 KiB gzip**；`bundle-analysis.html` 中 `src/src/i18n` 的 gzipLength = **124.3 KiB**（与逐文件 gzip 吻合），占 entry raw 的 24.3%。
- 实测键数：每个 locale 展平后 **1467 键**（不是任务描述里的 1461，计数口径不同），5 份之间 missing=0 / extra=0，无重复键；`npm test` 140/140（含 i18n parity 4 条）。
- 影响：entry（首访必经）与 5 个 standalone 页（`sla/bulk/maintenance/config/forecast` 各自 `import "../i18n/config"`）都背着 5 种语言；英语/单语用户首访下载 4 份永远用不到的语言。
- 改法：
  1. `config.ts` 只同步保留 `en`（fallback，23.3 KiB gzip），其余 4 份改 `const load = (lang) => import("./locales/xx.json")`；
  2. 检测到语言后 `await` 对应 chunk + `i18n.addResourceBundle`，再 `createRoot().render()`（或在 App 顶部用 `i18n.init` 的 promise 包一层 Suspense；`index.html` 已有内联脚本可放静态骨架/背景色，不会白屏）；
  3. `Language.tsx:10` 目前用 `Object.entries(resources)` 生成语言菜单，改为常量 `[{code,name}]` 列表（不依赖 resources）；
  4. 5 个 standalone 入口自动同享收益。
- 收益：entry 263 KiB gzip → 约 163 KiB gzip（−约 100 KiB，−38%）；英文用户省全部 4 份，中文用户省 3 份（en+zh_CN 保留）。每个 standalone 页同样 −约 100 KiB gzip。
- 工作量 M（1–2 天）；风险：首帧多一次 locale chunk 往返（可用 `modulepreload`，或 en 先渲染、语言 chunk 到达后切换，避免阻塞）；i18n parity 测试与 `i18n-sync` 不受影响。
- 注：`docs/IMPROVEMENT-AUDIT-2026-10-13.md` §5.2 只处理了“缺键/一致性”，**没有**处理“全量静态打包”，故这是新增项。

### F2 — 面板自身零压缩：首访 1703 KiB（P1）

- 证据链：
  - `web/public/public.go:121-168`：`serveAsset` 只 `Set("Content-Type"/"ETag"/"Cache-Control")` 后 `http.ServeContent`，无压缩；
  - 全仓库 `web/**/*.go` 搜索 `gzip|Content-Encoding|brotli`：只有**请求解压**（`web/api/client/report_v2.go:29-30`）和文件传输 `Content-Encoding: identity`，没有任何响应压缩中间件；
  - `deploy/nginx-nekomari.conf:61-91` 参考反代只有 `proxy_pass`，无 `gzip on`；
  - `docs/PERFORMANCE.md:124-136` 已记录“面板 Go 服务不压缩，首访 1669 KiB”，但没有列为待办。
- 本机实测（`python -m http.server` 提供 `frontend/dist`，即无压缩源站）：

```
first visit: 1703 KiB (1.66 MiB)
    js          931 KiB   49 requests
    css         763 KiB    2 requests
    api           6 KiB    9 requests
    html          3 KiB    1 requests
biggest:
         786 KiB enc=-  /assets/entry-index-BU_mihmi.js
         763 KiB enc=-  /assets/index-CLnsijGw.css
warm visit: 4 KiB
caches: {'workbox-precache-v2-...': 163}
```

- 文件级压缩实测（gzip9 / brotli11）：entry 785 KiB → **263 gz / 202 br**；index CSS 763 KiB → **95 gz / 60 br**；因此首访压缩后约 **405 KiB gzip / 约 340 KiB brotli**（−76%~−80%）。
- 改法（二选一，建议 A）：
  - **A. Go 层 gzip 中间件**（如 `gin-contrib/gzip`，只对 `text/*`、`application/javascript`、`application/json`、`image/svg+xml` 且 >1 KiB 的响应生效；对 `assets/*` 的 immutable 响应保留 ETag，压缩时补 `Vary: Accept-Encoding`，避免与 `http.ServeContent` 的 Range 冲突：压缩分支禁用 Range 或对 Range 请求走 identity）；
  - **B. 构建期预压缩**：为每个 asset 生成 `.gz/.br`，`serveAsset` 命中 `Accept-Encoding` 时直接发预压缩字节；运行时 CPU 为零，但要改归档格式与 `web/public` 测试。
- 收益：单次首访 1703 KiB → 约 405 KiB gzip（省约 1.3 MB）；重复访问已由 immutable 缓存覆盖不受影响。Cloudflare 后的用户当前由 CF 压缩，因此这条主要打“无 CDN / 直连 nginx”的自托管场景（本仓库的参考部署就属于此类）。
- 工作量 M（1–2 天含测试）；风险中：`http.ServeContent` 的 Range/ETag、`sw.js`/HTML 的 no-store 语义不能被破坏；建议加一条“响应带 Content-Encoding 时长度与解压一致”的 Go 测试。

### F3 — 首访即加载 recharts（仅为 hover 迷你图）（P1）

- 证据：`frontend/src/components/Node.tsx:272` 静态 `import MiniPingChartFloat from "./MiniPingChartFloat";`，使用处 `Node.tsx:94-108`（每张节点卡的“图表”图标）；`MiniPingChartFloat.tsx:82-89` 中 `MiniPingChart` 仅在 `open` 时渲染（悬停 3 s 或点击后才 `setOpen(true)`，见 `:32-49`）；`MiniPingChart.tsx:3` 静态引 recharts。
- 影响链：首访路由 `/` → `Index.tsx:12` lazy `NodeDisplay` → `Node.tsx`（静态 Float）→ Float（静态 MiniPingChart）→ recharts。实测首访 JS 请求闭包里确实出现 `assets/chunk-metricSeries-D6aLpQBx.js`：363 KiB raw / **107 KiB gzip**（visualizer 明细：recharts 669 KiB/212 gz、es-toolkit 65、decimal.js-light 48、@reduxjs/toolkit 30、d3-* 合计约 84）。
- 改法：`Node.tsx` 改为 `const MiniPingChartFloat = React.lazy(() => import("./MiniPingChartFloat"));` 并用 `<Suspense fallback={<IconButton .../>}>` 包住；或在 Float 内部把 `MiniPingChart` 改成 lazy（Float 本身很轻，radix Popover 已在 entry）。
- 收益：公开首页首访 **−107 KiB gzip / −363 KiB raw**（首访 JS raw 931 KiB → 约 568 KiB），不影响任何非悬停用户；首访 JS 请求数也少 1–2 个。
- 工作量 S（0.5 天）；风险极低（图表本来就在打开时才发 `public:getPingMetricStats` 请求，`MiniPingChart.tsx:108-116`）。

### F4 — 公开首页轮询叠加 + 每卡片一个 60 s timer（P1）

- 证据：
  - `frontend/src/contexts/LiveDataProvider.tsx:8` `const LIVE_DATA_INTERVAL_MS = 2000;`，`:49-81` 串行 `common:getNodesLatestStatus { include_ping: false }`（返回全部节点状态）；
  - `frontend/src/pages/Index.tsx:138-165`：`:148 interval = window.setInterval(refresh, 5000)`，`refresh` 来自 `NodeListProvider` 的 `common:getNodes`（只含名称/OS/价格/到期等静态字段，`NodeListProvider.tsx:31-102`）；
  - `frontend/src/components/PriceTags.tsx:42-44`：每个 `PriceTags`（每个节点卡片一个，`Node.tsx:81-90`）各起一个 `setInterval(..., 60_000)` 只为刷新“剩余天数”的 `now`。
- 影响：可见标签页 = 0.7 req/s（2 s 一路 + 5 s 一路），其中 5 s 那路每 12 次里最多 1 次有变化；N 节点时 N 个 interval、N 次/分重渲染（卡片有 `React.memo`，但 `now` 变化必然绕过 memo）。
- 改法：① `getNodes` 5 s → 60 s（或只在 `visibilitychange` / 节点数变化时刷新；实时状态已由 2 s 那路覆盖）；② `PriceTags` 的 `now` 提升为单例 ticker（`NodeGrid` 里一个 60 s interval，把 `now` 作为 prop 下传，或 `useSyncExternalStore` 共享 store）；③ 2 s 的 LiveData 可做成可配置（3–5 s）——但它同时驱动实例页实时曲线，建议保留 2 s、只修前两项。
- 收益：`getNodes` 请求 −92%（12 次/分 → 1 次/分）；50 节点时消除 50 个 timer 和 50 次/分渲染；对移动端电量/弱网更明显。
- 工作量 S（0.5 天）；风险低（新节点出现最多晚 60 s；如需即时，可在 2 s 响应里比对 uuid 集合触发一次 getNodes）。

### F5 — 实例图表每次实时 tick 在 render 体内全量重算（P2）

- 证据：`frontend/src/pages/instance/LoadChart.tsx:1549-1572`：

```tsx
{charts.map((chart) => {
  const metricBuilt = buildRowsFromMetricSeries(metricSeries, chart, definitionMap, pingTaskMap, t);
  const rawBuilt = isRealtime ? mergeBuiltChartData(buildRowsFromRealtime(data, chart, node, pingTaskMap, t), metricBuilt) : metricBuilt;
  const built = prepareChartData(rawBuilt, chart.metrics, pingTaskMap);
  const chartRows = trimMetricChartBoundaryRows(applyMetricEwma(built.rows, built.series, ewmaEnabled), ...);
  const chartTicks = metricChartBoundaryTicks(chartRows);
  const chartConfig = toChartConfig(built.series);
  const latestText = getLatestText(chartRows, built.series);
```

  这些都在渲染函数体内，没有 `useMemo`；`data` 每 2 s 换新（`instance/index.tsx:148-161` 用 `[...prev, newRecord].slice(-length)`，`length = 30*5 = 150`，`:29`），历史视图 `HISTORY_MAX_POINTS = 700`（`LoadChart.tsx:195`）+ `max_points: 700`（`:1169`）。
- 影响：每 2 s、每次图表内部状态变化都会对 150–700 点 × 每图 1–3 序列 × 最多 3 图重算并新建对象；`ChartContainer`/recharts 再 diff 一遍。**假设** 5–15 ms/次（低端机更高），未做 profiling，属“待验证”。
- 改法：把每个 chart 的 `built/chartRows/chartTicks/chartConfig` 移进 `useMemo`（依赖 `chart`、`metricSeries`、`dataVersion`），或把单图表拆成 `React.memo` 子组件只传 `chart` 与“已归并的行数据”；`setRecent` 追加时先浅比较避免无变化 tick 产生新数组（`updated_at` 相同已跳过，见 `instance/index.tsx:152-157`）。
- 工作量 S–M（半天到 1 天）；风险低（纯计算搬移，可用现有 `load-chart.browser.spec.py` 回归）。

### F6 — RPC2 重连：固定 3 s × 5 次、无退避、无恢复（P2）

- 证据：`frontend/src/lib/rpc2.ts:46-58`（`reconnectInterval: 3000`、`maxReconnectAttempts: 5`、`heartbeatInterval: 15000`、`requestTimeout: 30000`）；`:551-564` `attemptReconnect()` 固定 3 s 一次、`reconnectAttempts++`，超过 5 次后不再调度；`:390-402` 未连接时每次 `call` 还会 `autoConnect()` 并回退 HTTP。
- 影响：面板重启 >15 s 时，WS 永久停在断开；此后每 2 s 的 LiveData 调用都走 HTTP POST（多一套请求头/连接开销），直到用户刷新。功能不坏（HTTP 回退可用），但弱网/重启场景下体验与流量都退化。
- 改法：指数退避 + 抖动（0.5 s → 30 s 封顶）+ 只在 `document.visibilityState === "visible"` 时重试 + 恢复后重置计数（已有 10 s 稳定窗口逻辑，`:36,418-423`）；顺带把 `heartbeatInterval` 与 `LIVE_DATA_INTERVAL_MS` 对齐说明。
- 工作量 S；风险低。

### F7 — SW 静默自动更新 + 哈希 chunk 每版全变 → 打开中的标签页可能 404（P2）

- 证据：`frontend/vite.config.ts:84` `registerType: "autoUpdate"`，而 `dist/sw.js` 开头实测 `self.skipWaiting(), s.clientsClaim()`（构建产物），新 SW 立即接管并清理旧预缓存；`frontend/src/components/PWAUpdatePrompt.tsx:1-4` 直接 `return null`（“更新提示已移除”）；`vite.config.ts:135` `navigateFallback: null`，无离线兜底。
- 叠加放大因素：`docs/OPEN-WORK.md` 已记录“前端构建不可复现，重建一次 143 个 chunk 文件名变化”（`:126`）；即**源码没变的重建**也会让新版 SW 把大部分预缓存换掉，更新流量接近全量，同时打开中的旧页面引用的旧 chunk 在新版服务器上已不存在。
- 影响：部署后仍开着的标签页，下一次懒加载路由可能白屏/报错（旧 chunk 404）；用户只有手动刷新才恢复。
- 改法（择一）：① `registerType` 改 `"prompt"` 并恢复 `PWAUpdatePrompt` 的“有新版本，刷新”提示；② 保持 autoUpdate，但服务端保留上一版 `dist` 一个版本、`navigateFallback` 指到预缓存 shell；③ 至少给懒加载失败加一次 `import()` 重试 + 整页 `location.reload()` 兜底。
- 工作量 S（①半天）；风险低。

### F8 — 首访 49 个 JS 请求：chunk 碎片化（P2）

- 证据：`frontend/dist/assets` 共 **238 个 JS 文件**，其中 **42 个 <0.5 KiB、65 个 <1 KiB**（这些小于 1 KiB 的文件合计仅 31 KiB）；本机实测首访 **49 个 JS 请求 / 931 KiB**（平均 19 KiB/请求）。`vite.config.ts:230-235` 明确写了“Do not use manualChunks, use React.lazy() and <Suspense> instead”，这是有意的取舍。
- 影响：HTTP/2 下 49 个并行流尚可，但每个请求仍带头部开销与调度；HTTP/1.1 反代（部分自托管用户没有 http2）下 6 并发连接会让首屏串行化。**未实测 HTTP/1.1 场景，属假设/待验证。**
- 改法：给稳定大依赖做少量 `manualChunks`（react-dom、@radix-ui/themes + floating-ui、lucide-react 合并、i18next），把“0–1 KiB 图标 chunk”收敛；或至少把 `lucide-react` 改成按图标集静态导入（现状每个图标一个 chunk）。
- 收益：请求数 **49 → 约 25–30**（估算），体积基本不变；工作量 S；风险低–中（`manualChunks` 与主题共存时 chunk 命名在 `/assets/` 下，需回归 standalone 页面不被主题遮挡的既有约束）。

### F9 — 内嵌默认主题把 admin 打包两份（P2，构建/归档）

- 证据：`script/embed-theme.mjs:10-16` 有意注释“`frontend/dist/standalone/admin` also at `admin/` … It is copied rather than moved”；实测解压 `web/public/defaultTheme/dist.tar.zst`（5.39 MB → 33.73 MiB tar），`admin/` 与 `standalone/admin/` 各 **248 个文件、8.26 MiB，逐文件 sha1 完全相同**；而 `frontend/dist/standalone/admin/index.html` 的资源路径是绝对 `/admin/assets/...`（`vite.admin.config.ts:13` `base: "/admin/"`），因此 `/standalone/admin/...` 这份副本没有任何页面/路由会用到（全仓库引用只有注释与 `web/public/standalone_pages_test.go:19-20` 的断言）。
- 实测 zstd 收益：把 tar 去掉 `standalone/admin/` 后，以同等级（zstd 19）重压缩：**4.62 MiB → 3.23 MiB，差 1.40 MiB**（对应已提交的 `dist.tar.zst` 约 5.39 MB → 约 4.0 MB）。
- 改法：`script/embed-theme.mjs` 暂存后 `rm(stage/standalone/admin)`（`admin/` 已在 `:134` 复制好），或直接从 `dist-admin` 复制 `admin/`；同步修改 `web/public/standalone_pages_test.go` 与 `admin_theme_test.go` 的断言。
- 收益：内嵌二进制减小约 1.4 MiB（压缩后），Go 启动解码后的常驻内存少 8.26 MiB（属后端口径）；对首访传输无影响。工作量 S；风险低（有 Go 测试兜底）。

### F10 — PWA 离线导航事实上只对 60 s 内有效（P2）

- 证据：HTML 不进预缓存（`vite.config.ts:113` `globPatterns: ["**/*.{js,css,ico,png,svg}"]`，无 html；`:135` `navigateFallback: null`），外壳 HTML 的缓存策略是 `web/public/public.go:246` `const shellCacheControl = "public, max-age=60, must-revalidate"`；`first-visit-transfer.py` 的“offline mounts=True”是在最后一次导航的 60 s 窗口内测的（本机复现：`caches: 163`、`offline app_mounted=True`）。
- 影响：安装为 PWA 后，隔一段时间离线打开会得到浏览器错误页而不是应用壳；与“可离线”的宣传不符（`docs/PERFORMANCE.md:117-121` 只验证了窗口内）。
- 改法：加一个极小的 `offline.html`（内联样式+“离线”文案，<2 KiB）进预缓存，并把 `navigateFallback` 指向它；在线时它永远不会出现。工作量 S；风险低（需确认不影响 `public.go` 的动态外壳逻辑）。
- 注：这条与“预缓存体积”无关，是离线策略行为；若 Lead 认为并入 B1 跟踪即可，可不再单列。

### F11 — 公开节点列表无虚拟化（P2，规模相关，待验证）

- 证据：`NodeDisplay.tsx:74-108` 过滤/排序后 `:234-260` 全量渲染；`Node.tsx:275-325` `NodeGrid` 对 `sortedNodes` 直接 `.map`；卡片本身 `React.memo`（`Node.tsx:27`），但每次 LiveData tick（2 s）父层重渲染、`onlineSet` / `[...nodes].sort()` 重算（`:285`）。
- 影响：节点数 N 时首屏 DOM 与 diff 成本线性；**假设** >100 节点开始可感（未验证，需要 200 节点 fixture；OPEN-WORK 里 `admin-node-table.browser.spec.py` 只有小规模挂载）。仓库没有虚拟列表依赖（`package.json` 无 `react-window` / `react-virtuoso`）。
- 改法：节点网格加窗口化（`@tanstack/react-virtual`）或“分组折叠 + 分页”；`NodeGrid` 的 sort 移入 `useMemo`。工作量 M；风险中（滚动/键盘/搜索交互要回归）。
- 建议：先测 200 节点 fixture 再决定，不阻塞 v1.6.13。

### F12 — 可访问性 / 感知性能（P2–P3）

- 已实跑：`C:\Python314\python.exe script/chart-a11y.spec.py -v` → **6/6 通过**（keyboard focus 有 name/outline/2px、tooltip 有 live region、动画更新、图例装饰性、主题色、序列名兜底）。`ChartContainer` 具备 `aria-label` 或序列名兜底（`LoadChart.tsx:1728`、`dashboard.tsx:927-931`、`MiniPingChart.tsx:322-328`）。
- 不足：
  1. `ErrorBoundary.tsx:66-68` 在生产环境把完整 `error.stack` 渲染给用户（信息泄露 + 噪音），`:80` 的“返回首页”硬编码 `/`（`VITE_BASE_URL` 子路径部署会 404，与 OPEN-WORK 的绝对资源路径问题同源）。
  2. 加载态是整页 spinner（`components/loading.tsx:14-44`，`Index.tsx:167-169`），无骨架屏；Dashboard/Instance 首屏是“先空白再整页出现”，感知性能差。建议首屏用与卡片同尺寸的 skeleton（纯 CSS，无额外请求），可让 LCP 从“等待数据”变成“立即有内容”。工作量 S。
  3. 首屏 stale 提示已有 `role="status"`（`Index.tsx:199-211`）、首载失败有 `role="alert"` + 重试（`:173-194`），这部分 v1.6.12 已修好，可访问性合格。
- `chart-a11y` 测试是 keyboard-only，OPEN-WORK 已记录“需要真实读屏器”的缺口，本次不重复。

### F13 — 依赖卫生（P3，不产生首访字节）

- `package.json` 中 0 处 import 的运行时依赖：`motion`（12.x）、`twemoji`、`uuid`、`react-toastify`、`github-markdown-css`、`http-proxy-middleware`（grep `frontend/src` 无 import）。它们不进 bundle（rollup 只打包被 import 的模块），但增大 `npm install` 与 CI 时长，且 `motion` 是误以为“已有动画库”的诱因。建议 `npm uninstall` 后跑 build/lint/test 验证。工作量 S；风险低。
- 另：`@zxing/text-encoding` 无直接 import，但被 `chardet` 依赖，其 `encoding-indexes` chunk 608 KiB raw/186 gzip 只在终端/文件管理器用，已 lazy——无需处理。

---

## 3. 与 OPEN-WORK 的关系

| OPEN-WORK 现有条目 | 本次关系 |
|---|---|
| D10 “~33 处 `toLocaleString*`、~24 处 mutation 信封、8 处 `dangerouslySetInnerHTML`” | 未新增重复项；审计中未把这三项当新发现 |
| `frontend/vite.config.ts` 构建不可复现（rollup chunk 分配） | **补充证据**（并入 F7）：这也是 SW 更新近乎全量、旧标签页 chunk 404 的放大因素：`docs/OPEN-WORK.md:126` 的“重建一次 143 个 chunk 名变化” |
| `docs/ROADMAP.md` B1 / §G：SW 预缓存策略、编辑器 chunk、CSS 已测量，“不是杠杆” | 本报告**不把它们列为新发现**。仅补充一组实测供决策：当前 `sw.js` 164 条 / 4.40 MiB raw / **1.34 MiB gzip**，其中公开首屏闭包 41 条 / 524 KiB gzip，**admin/终端/关于页专用 119 条 / 764 KiB gzip**（xterm 190、encoding-indexes 183、about 52、proxy 40…）。若将来重开该决策，这是“全保留”缺的那半张价目表；本次不作为待办 |
| 前端 a11y：`chart-a11y.spec.py` 仅键盘、无读屏 pass | **补充证据**：本次实跑 6/6 通过（`ChartContainer` 的 name/outline/live region 有回归）；读屏缺口仍然存在，维持 OPEN-WORK 表述 |
| `TerminalDialog` 无 Tab 陷阱、焦点不返回 | 未涉及（v1.6.12 已修 Enter 破坏性确认，剩余焦点顺序需决策，非性能） |
| “绝对资源 URL 忽略 base”（`Flag.tsx` + 2 处） | **补充证据**：`ErrorBoundary.tsx:80` 的 `window.location.href = "/"` 是第四处同类问题（子路径部署返回首页 404），建议并进同一批修 |
| “Locale 字符串以 raw HTML 注入 8 处 / `escapeValue:false`” | 未新增；与 F1 改法无冲突（i18n 懒加载不改变 `escapeValue`） |

**新增项（OPEN-WORK 中没有的）**：F1（i18n 全量静态打包）、F2（零压缩）、F3（首访加载 recharts）、F4（轮询叠加/per-card timer）、F5（LoadChart 渲染期重算）、F6（RPC2 重连无退避）、F9（主题归档 admin 双份）、F10（离线导航 60 s）、F11（列表无虚拟化，待验证）、F13（未使用依赖）。

---

## 4. 建议实施顺序

按“收益/风险/工作量”排，前 3 项是 v1.6.13 的候选：

1. **F3（recharts 懒加载，0.5 天）**：两行 `React.lazy`，首访 −107 KiB gzip，风险最低；建议先合，作为“首访瘦身”的样板与回归（用 `first-visit-transfer.py` 对比）。
2. **F1（i18n 按需，1–2 天）**：首访与 5 个 standalone 页各 −约 100 KiB gzip；先做 `config.ts` 异步化 + `Language.tsx` 常量表，再用 5 个入口的 build 产物验证；注意首帧骨架与 `modulepreload`。
3. **F2（Go 层 gzip/预压缩，1–2 天）**：直连/自托管首访 1703 KiB → 约 405 KiB gzip，是唯一“1 MB 级”的单项收益；需要与 `Cache-Control` / `ETag` / Range 的既有测试一起补。若想避开 Go 改动，先只做构建期 `.gz` + `Accept-Encoding` 命中（收益相同、运行时零 CPU）。
4. **F4（轮询去重，0.5 天）**：`getNodes` 5 s→60 s + `PriceTags` 单 ticker；与 F3 同批发布，回归 `Index.tsx` / `NodeGrid` / `admin/index.tsx`。
5. **F5 + F6（0.5–1 天）**：LoadChart 计算搬进 memo、RPC2 退避；纯内部重构，用现有 `load-chart.browser.spec.py` 与 `admin-node-table.browser.spec.py` 回归。
6. **F7、F9、F10（各 0.5 天）**：SW 更新提示、主题归档去重（−1.4 MiB 压缩后体积）、离线兜底页；F9 只改 `embed-theme.mjs` + Go 测试，值得单独一个构建批次。
7. **F11（1–2 天，待验证）**：先做 200 节点 fixture 测量，再决定虚拟化；F12/F13 随手收尾（ErrorBoundary base path、骨架屏、依赖清理）。

**回归/验证建议（Lead 可复用）**：

- 体积：`npm run build` 后对比 `frontend/dist/assets/entry-*.js` 与 `index-*.css` 的 raw/gzip/brotli；`dist/sw.js` 的 precache 条数与总字节。
- 首访：`python frontend/script/first-visit-transfer.py http://127.0.0.1:25774 3`（需真实实例；本次用的是同构建的静态服务，仅测传输）。
- 轮询：DevTools Network 过滤 `/api/rpc2`，确认 60 s 内请求数从约 42 降到约 32（F4 后）。
- 图表：`python frontend/script/chart-a11y.spec.py`（6/6 基线）+ `load-chart.browser.spec.py`。

---

## 附：本次未运行 / 无法验证的项

- `npm run build` 未重跑（避免与 Lead 的构建/测试争用；`dist` 为 2026-10-17 构建，哈希与 `bundle-analysis.html` 一致，体积结论不受影响）。
- `first-visit-transfer.py` 未对生产/真实实例运行（本机无 25774 监听），用的是同一 `frontend/dist` 的静态服务，因此数字代表“无压缩源站”，不代表 Cloudflare 后的线上传输（线上已压缩，见 `docs/PERFORMANCE.md`）。
- 未做真实读屏器 / 真机 LCP 测量：F11（节点数阈值）、F5（每 tick 毫秒数）、F10（离线窗口）标注为假设/待验证。
- `frontend/script/` 下其余 Python browser spec（admin-node-table、bulk-edit、file-manager 等）未逐条运行，只跑了本领域最相关的 `chart-a11y.spec.py`；`npm test` 140/140、`npm run lint` 0 error、`npm run audit:compiler` 0 findings（均为本轮实跑）。
