# 功能缺口与功能优化审计 — 2026-10-18

**基线。** 分支 `main` @ `b48963c`（`git describe` = `v1.6.12-1-gb48963c`，即 v1.6.12 之后一个文档提交）。
工作区与 2026-10-13 审计时相同：` M README.md`、`?? docs/OC424-MTPROTO.md`、`?? topology/`。
**方法。** 只读源码 + 针对性命令（`git log`、`Select-String`、路由/RPC 注册表对照前端调用点）。未修改任何源文件；
未运行面板、数据库或全量测试（Lead 会跑），因此涉及"渲染后行为"的结论都标了**待验证**。
**口径。** 本文只写两类东西：(a) `docs/OPEN-WORK.md` **没有**的缺口；(b) 已交付但"差一点就完整"的优化点。
凡 `OPEN-WORK.md` 已有条目，一律在第 3 节以"已有条目 + 是否建议升级"出现，不重复计数。

---

## 1. 结论摘要 — Top 5（按性价比排序）

| # | 发现 | 为什么值 |
|---|---|---|
| 1 | **FG-02：H1–H5 五个已交付页面（SLA / 批量 / 维护 / 配置导入导出 / 预测）在面板、主题、文档里都没有任何入口**，只能手打 URL | 5 个功能已上线、有测试、有 Go 契约测试，但操作员找不到。修法是往 `menuConfig.json` 加 5 条菜单（**S**），一次改动把 5 个功能从"存在"变成"可用"，风险极低 |
| 2 | **FG-01：`admin:exportConfig`（`include_secrets=true`）与 `admin:rollAgentUpdate` 没有标 `MarkSensitive`** | 被窃 session / API key 可一次性导出全部 agent token + 通知凭据，或整队替换 agent 二进制，均不需要 TOTP；而 `admin:exec`、`admin:dbQuery` 已经要求。**P0，S** |
| 3 | **FG-04：H6 的"固定版本"不持久**——面板只在点击时下发一次，agent 存在内存里；注释声称"每次连接都发"，但下发点只有一处。节点重启后回到"跟随最新" | 回滚/钉版本会在下一次重启后静默失效，甚至反向自更新到最新版。**P1，M** |
| 4 | **FG-06：维护窗口只压离线告警，ping/负载告警照常发送** | H3 的验收写的是"offline **and ping** notifications are suppressed"（ROADMAP:369）；实际 `sendLoadNotification` 没有任何维护判断。计划内网络调整仍会把手机叫醒。**P1，S** |
| 5 | **FG-07：维护窗口不进节点状态、也不进 SLA** | H3 验收"status page distinguishes maintenance from down"（ROADMAP:381）未满足；计划重启在 SLA 里表现为任务丢包/事件。**P1，M** |

> 紧随其后的高性价比项：FG-08（节点表勾选后无任何批量动作，**S**）、FG-03（H6 完全无 UI，**S**）、FG-14（uptime 已实时展示但无历史/重启次数，**S–M**，且 `OPEN-WORK` 对应条目的措辞已过时）。

---

## 2. 详细发现

> 严重度口径：**P0** = 安全/正确性（先修）；**P1** = 运维效率/已交付功能不可用；**P2** = 体验与能力补齐。
> 工作量：**S** ≤ 0.5 天，**M** 1–3 天，**L** > 3 天或需要 agent 发版。

### 总表

| ID | 严重度 | 位置 | 证据（摘要） | 影响 | 改法 | 工作量 |
|---|---|---|---|---|---|---|
| FG-01 | P0 | `web/rpc/jsonrpc/admin.configxport_apply.go:29`、`admin.agentupdate.go:35` | 两者只注册 `rpc.RoleAdmin`；敏感名单只有 3 条（`sensitive_stepup_test.go:15-19`） | 失窃会话可导出全部凭据 / 整队换二进制，无需 TOTP | 两条 `rpc.MarkSensitive` + 扩名单测试；导出页补验证码输入 | S |
| FG-02 | P1 | `frontend/src/config/menuConfig.json`、`components/admin/AdminPanelBar.tsx:22` | 全仓库 `frontend/src` 内 `/standalone/` 只出现在 4 个 entry 的注释里；主题 `theme-luminaplus/src` 0 处；文档 URL 还写错（见 FG-19） | 5 个已交付功能不可发现 | menuConfig 加 5 条（`newTab: true`），指向 `/standalone/<page>/<page>.html` | S |
| FG-03 | P1 | `web/rpc/jsonrpc/admin.agentupdate.go:35,45` | `grep rollAgentUpdate frontend/src` = 0 命中；无页面、无按钮 | 面板触发升级只能手调 RPC，功能等于未交付 | 增加"节点升级"页/卡片，展示 `agentVersions` + 逐节点结果 | S |
| FG-04 | P1 | `admin.agentupdate.go:22-26,156`；`agent/update/target.go:35-41`；`agent/server/websocket.go:443` | 下发点仅 `:156`；agent 只存内存 `atomic.Value`，注释却称"it also sends one on connection" | 重启后钉版本丢失 → 回滚失效/反向自更新 | 面板持久化每节点目标并在连接时重发（或落库列） | M |
| FG-05 | P1 | `agent/update/update.go:41,341,425` | 只访问 `api.github.com`，且下载校验 `parsed.Host == "github.com"` | `ROADMAP` H6 的动机（PZYC 无法访问 release 资产）未被解决 | 面板代传二进制（新 RPC + 校验 + agent 侧下载源） | M |
| FG-06 | P1 | `utils/notifier/load.go:305-323`；`offline.go:95-111` | 只有 offline 分支调用 `For(now, clientID)`；`sendLoadNotification` 无维护判断，且 ping 规则走同一出口（`load.go:75`） | 维护期内 ping/负载告警照发 | 在 `sendLoadNotification` 按 client 过滤 `For/Decide`（点位告警只抑制、不延迟） | S |
| FG-07 | P1 | `web/rpc/jsonrpc/latest_status.go:40-41,67`；`internal/sla/*`；`internal/maintenance`（仅被 notifier + admin RPC 引用） | `latestStatus` 只有 `online bool`；`grep maintenance internal/sla` = 0 | 计划停机被当故障；SLA 可用性被计划工作拉低 | 节点状态输出 `maintenance` 字段 + 前端徽标；SLA 查询排除窗口内桶（需定义并测试） | M |
| FG-08 | P1 | `frontend/src/pages/admin/index.tsx:42,77,87`；`autoDiscovery/Header.tsx:59-63` | 勾选状态只用于显示 `(N selected)`；`NodeTable.tsx:312-319`；批量动作只在独立页 `pages/bulk/index.tsx` | 交互误导；v0.1.36 说明"Multi-select the node table"的流程不存在 | 选中后显示批量操作条 → 跳批量页并带上 uuid；或暂时移除勾选框 | S |
| FG-09 | P2 | `frontend/src/pages/admin/index.tsx:47-57` | 仅 `node.name.includes(searchTerm)`；公开首页反而有分组筛选（`components/NodeDisplay.tsx:41-79`） | 有分组的舰队在管理端无法按组作业 | Header 加分组/标签下拉，`useMemo` 内过滤，可选 URL 参数 | S |
| FG-10 | P2 | `frontend/src/pages/admin/log.tsx:44`；`web/rpc/jsonrpc/admin.system.go:31-40,93-98` | 前端只传 `limit`/`page`；后端支持 `msg_type` 且日志表已有 60+ 写入点 | "昨晚谁执行了 exec"无法在面板里回答 | 传 `msg_type` + 时间/操作者筛选；加 CSV 导出 | S |
| FG-11 | P2 | 全仓库 `grep "text/csv\|\.csv" -- '*.go'` 仅命中 MIME 表 | 除配置导出与整库备份外，没有任何指标/记录导出 | 容量报告、账单核对只能查库 | 新增指标/记录 CSV 导出 RPC（限行、按保留期、流式） | M |
| FG-12 | P2 | `web/rpc/jsonrpc/admin.task.go:37-140`；`frontend/src/pages/admin/exec.tsx` | 后端有 `getTasks`/`getTaskResultsByTaskId`/`getTasksByClientId`；前端 0 命中 | 一周前那条命令的输出找不回来 | 执行页加"历史任务"列表 + 结果抽屉 | S–M |
| FG-13 | P2 | `internal/metricstore/definitions.go:64-92`；`agent/monitoring/monitoring.go:16-34,84-90` | 指标定义无 cpu 温度/磁盘 I/O；温度只有 GPU；agent 无 hwmon/diskstats 读取 | 过热、磁盘饱和无法从面板发现 | agent 采集 CPU 温度 + 磁盘 I/O → 新指标 → 图表（**需发版**） | L |
| FG-14 | P2 | `agent/monitoring/monitoring.go:152-157`；`web/rpc/jsonrpc/latest_status.go:41,67`；前端 `components/Node.tsx:79` | 实时 uptime 已上报、已展示；但指标定义无 uptime，且 `report_batcher` 只用于连续性判断 | 无法回答"这个月重启几次/在线多久" | 增加 `system.uptime` gauge 写入 + 由下降推导重启次数；修正 OPEN-WORK 措辞 | S–M |
| FG-15 | P2 | `internal/forecast/forecast.go`（无 price/currency）；`ROADMAP.md:436` | H5 标题是 "Traffic **and cost** forecasting"，实现只有流量 | 超量费用无法预估 | 预测响应加 `cost`/`overage`（用 `clients.price/billing_cycle/currency`），页面加列 | S–M |
| FG-16 | P2 | `ROADMAP.md:316-320`；`frontend/src/components/bulk/BulkEditPage.tsx:139-301` | H2 范围含 "copy this node's settings to…" 与 notification policy；实际字段只有 group/tags/weight/hidden/price/billingCycle/currency/trafficLimit/trafficLimitType | "照这台配另外三台"仍需逐台 | 加"从节点复制"动作 + `offline notification` 字段 | S–M |
| FG-17 | P2 | `database/accounts/accounts.go:63`；唯一业务调用 `web/install/install.go:175` | 没有用户 CRUD RPC/页面；`models.User` 无角色字段 | 无法建只读运维/审计员，只能共享管理员口令 | 用户 + 角色（admin/viewer）+ 路由级校验；或明确记录单账号设计并关闭该项 | M–L |
| FG-18 | P2 | `frontend/src/pages/admin/nodeTable/mutationResult.ts:25-35`；`web/rpc/jsonrpc/admin.client.go:112` | 服务端错误串（如 "Invalid or missing UUID"）经 toast 原样展示 | 中文界面出现英文错误且无处置建议 | RPC 错误加稳定 code，前端映射 i18n；先覆盖高频 20 条 | M |
| FG-19 | P2 | `ROADMAP.md:96`；`web/public/public.go:334`；实际 `frontend/dist/standalone/sla/sla.html` | 文档/注释写 `/standalone/sla.html`、`/sla.html`；契约测试钉的是 `standalone/<name>/<name>.html`（`web/public/standalone_pages_test.go:34,57`） | 按文档访问 404，看起来像功能没上 | 改正两处文本；在契约测试里断言"文档 URL == 构建产物路径" | S |
| FG-20 | P2 | `agent/server/task.go:519-522`；`agent/update` 无关；`internal/metricstore` 无状态码指标 | `http` 任务只测延迟；无状态码、无证书到期 | 网站"慢"与"证书 3 天后过期"分不出来 | agent 上报 `status_code`/`tls_expiry_days` → 指标 → 告警（**需发版**） | L |

### 2.1 关键发现的详细证据

#### FG-01（P0）配置导出含密钥 / 整队升级 未纳入敏感操作二次验证

**用户场景。** 管理员在咖啡店用旧笔记本登录面板后 cookie 泄露；攻击者不需要 TOTP：`POST /api/rpc2 {"method":"admin:exportConfig","params":{"include_secrets":true}}` 就能拿到全部 agent token（可冒充节点/写数据）、通知渠道凭据；`admin:rollAgentUpdate` 可让所有节点去装任意已发布版本。

```go
// web/rpc/jsonrpc/admin.configxport_apply.go:28-36
RegisterWithGroupAndMeta("exportConfig", rpc.RoleAdmin, adminExportConfig, &rpc.MethodMeta{
    Params: []rpc.ParamMeta{{Name: "include_secrets", ...}},
})
...
// :61-70 若含密钥，只写一条审计，没有任何 step-up
document, err := configxport.Export(exportStore{}, params.IncludeSecrets, utils.CurrentVersion)
if params.IncludeSecrets { auditlog.Log(ip, actor, "export configuration including secrets", "warn") }
```
```go
// web/rpc/jsonrpc/admin.agentupdate.go:35
RegisterWithGroupAndMeta("rollAgentUpdate", rpc.RoleAdmin, adminRollAgentUpdate, ...)
// 对比 web/rpc/jsonrpc/admin.system.go:46
rpc.MarkSensitive("admin:exec")
```
敏感名单是显式白名单（`web/rpc/jsonrpc/sensitive_stepup_test.go:15-19` 只有 dbExec/dbQuery/exec），所以这两条不是"登记了但没生效"，是**从未登记**。

**改法。** 两处 `rpc.MarkSensitive`；允许名单测试同步加两条；`frontend/src/components/config/ConfigExportPage.tsx` 在 `includeSecrets` 为真时收集一次性验证码（与 `pages/admin/account.tsx` 的 2FA 输入一致）。**依赖/风险：** 会话若来自 API key 仍会绕过（`OPEN-WORK` 已有条目，需策略决定）；无 UI 的 `rollAgentUpdate` 会先由 FG-03 接 UI。**验收：** 带 2FA 的账号不带 code 调用两者均返回 `PermissionDenied`；名单测试失败即提醒审阅。

#### FG-02（P1）五个独立页面没有任何入口

**用户场景。** 运维想给 10 台机器批量改分组、想看本月 SLA、想导出配置、想做流量预测——这四件事 2026-09 都已交付，但界面上找不到入口；能发现它们的人只有读过 ROADMAP/CHANGELOG 并记住 URL 的人。

**证据（这是同一类问题的第三次）。**
```powershell
# 1) 面板导航是静态 JSON，里面没有独立页面
Select-String frontend/src/config/menuConfig.json -Pattern 'standalone'   # 0 命中（全文 180 行）
# 2) 前端源码里 /standalone/ 只出现在 4 个 entry 的文档注释
Select-String -Path frontend/src/**/*.ts* -Pattern '/standalone/'         # 仅 entries/{bulk,config,forecast,maintenance}.tsx 注释
# 3) 主题也不提供入口
Select-String -Path theme-luminaplus/src/**/*.ts* -Pattern '/standalone/' # 0 命中
```
产物确实在：`frontend/dist/standalone/{sla,bulk,config,forecast,maintenance}/<name>.html`，且有 Go 契约测试
（`web/public/standalone_pages_test.go:42-48,52-73`）保证"内嵌、自包含、不引用主题 `/assets/`"——但**没有任何测试断言"能从界面到达"**，
这正是 ROADMAP H0 的教训（"nothing asserted that it could be *reached*"）在新页面上的复现。

**改法（S）。** 在 `menuConfig.json` 增加一个"运营工具"分组（或并入现有 `operations.title` 子项），5 条：
`/standalone/sla/sla.html`、`/standalone/bulk/bulk.html`、`/standalone/maintenance/maintenance.html`、
`/standalone/config/config.html`、`/standalone/forecast/forecast.html`，全部 `newTab: true`（与 `documentation`/`home` 一致）。
**为什么主题不影响：** 未命中主题目录时会回退内嵌主题（`web/public/public.go:838-842` + `openAsset` 的 defaultTheme 回落），
所以已装主题的部署同样可达。**顺手做 FG-19**：把文档与注释里的 `/standalone/sla.html` 改对。
**验收：** 新增一个 `script/panel-nav.test.mjs`（或扩 `standalone-pages.test.mjs`），从 `menuConfig.json` 读出所有面板内路径，
断言每个 `standalonePages` 名称至少被一条菜单引用——把"可达性"变成会变红的测试。

#### FG-03 / FG-04 / FG-05（P1）H6 面板触发升级：交付了 RPC 与 agent 侧，但缺 UI、缺持久、缺代传

**证据链。**
- RPC 存在且注释完整：`web/rpc/jsonrpc/admin.agentupdate.go:14-50`；面板下发唯一位置 `:156` `agentrt.DispatchV2Event(..., v2.MethodAgentUpdate, ...)`。
- agent 侧只存内存：`agent/update/target.go:35-41` "Held in memory, not written to disk, and that is deliberate: the panel re-sends its target with every update event, **and it also sends one on connection**, so a restart re-learns it."
- 但面板没有"连接时重发"：全仓库 `MethodAgentUpdate` 在 `web/` 只有 `:156` 一处；`grep "TargetVersion\|target_version" *.go` = 0（面板没有为节点保存目标）。
- 后果链：指定回滚到 `v1.4.x` → agent 安装后 `os.Exit(42)` 重启（`target.go:119`）→ 重启后 `RequestedVersion()` 为空 → 回到"跟随最新" → 下一次自更新又升到最新。

**用户场景。** 一次 canary 回滚在"成功"报告之后悄悄失效，而面板只显示"dispatched"；操作员以为节点被钉住了。

**改法。** （a）面板为每个节点持久化 target（新列或 config 键），在 v2 连接建立/拉取事件时重发；（b）UI 读 `admin:agentVersions`（`admin.agentupdate.go:82-103`）展示版本分布 + 逐节点结果（`:105-200` 已返回 per-node outcomes）；（c）`agent/update/target.go` 的注释与实现二者必须改成一致。**风险：** 目标落库后"操作员清除目标 + 面板不可达"的旧顾虑（注释里写的）需要用"重发只发生在连接时、且以库为准"来化解。**验收：** 钉版本 → 重启节点 → 版本不变；rollback 后 10 分钟仍在该版本。

**FG-05（代传）** 是 H6 原始动机（`ROADMAP.md:459-474`：PZYC 无法访问 release 资产，"the agent fetches the binary from the panel (not GitHub)"）。
现状 `agent/update/update.go:41` `githubAPIBaseURL = "https://api.github.com"`、`:341` `parsed.Host != "github.com"` 即拒绝。
**改法：** 面板加 `GET /api/admin/agent-asset?v=&os=&arch=`（校验后缓存），agent 的 `downloadAsset` 允许面板 base URL；**需 agent 发版**，与下一批 agent 改动同车。工作量 M。

#### FG-06（P1）维护窗口只压离线告警

```go
// utils/notifier/offline.go:95-110 —— 唯一的维护判断
if decision := For(now, clientID); decision.Suppress { ... deferOfflineAlert(...) ; return }

// utils/notifier/load.go:305-323 —— ping 规则(load.go:75)与负载规则共用，没有任何维护判断
func sendLoadNotification(clientUUIDs []string, task models.LoadNotification) {
    ...
    messageSender.SendNotification(models.EventMessage{Event: messageevent.Alert, ...})
}
```
ROADMAP H3 的验收写的是"Inside a window, no offline/**ping** notification is sent"（`ROADMAP.md:369`、`:377-378`）。
**用户场景。** 凌晨给机房换光模块（目标侧维护窗口）→ 节点侧 ping 任务 100% 丢包 → `Alert` 仍然发出去。
**改法（S）。** `sendLoadNotification` 内按 uuid 调 `For/Decide`：点位型告警只做抑制（不排队延迟，窗口结束后再报旧指标没有意义），
在日志里记一次"suppressed by window id"；补两条测试（窗口内不发、窗口外照发）。
**风险：** 不要延迟——负载/ping 是点位事实，延迟交付会产生"两小时前的告警"；这个决定要写进代码注释。

#### FG-07（P1）维护窗口不进节点状态、不进 SLA

- 节点状态只有 `online bool`：`web/rpc/jsonrpc/latest_status.go:40-41,67`；前端没有"维护中"徽标（`grep -i maintenance frontend/src` 只命中数据库维护卡片与独立页）。
- `internal/maintenance` 被谁引用：`utils/notifier/*` 与 `web/rpc/jsonrpc/admin.maintenance.go`，**`internal/sla` 0 处**；
  SLA 的可用性来自 ping 丢包桶（`internal/sla/availability.go:233-265`），节点级只有覆盖率（`:268-284`）。
- ROADMAP H3 验收："**The status page distinguishes "maintenance" from "down"**"（`:381`）——H3 在 ROADMAP 里标着 closed（`:331`），这条没做到。

**改法。** （a）`latestStatusFromReport` 增加 `maintenance *maintenance.Status`（用 `internal/maintenance.Evaluate` + 窗口缓存），前端在节点卡片/详情/表格显示"维护中"且不计红；
（b）SLA：`sla.ReportRequest` 增加排除窗口（或"维护桶"标记），任务事件里把落在窗口内的连续失败标为 `maintenance` 而非 outage，并在页面上说明定义；
（c）补测试：窗口内合成的丢包不降低可用性，窗口外照旧。**风险：** 这是"口径"变更，必须像 H1 那样把定义写进代码注释并测试钉住，否则会变成"随便排窗口就能刷高 SLA"。

#### FG-08（P1）节点表勾选是死路

```tsx
// frontend/src/pages/admin/index.tsx:42,77,87 —— 状态只往下传
const [selectedNodes, setSelectedNodes] = useState<string[]>([]);
<Header ... selectedNodes={selectedNodes} />
<NodeTable selectedNodes={selectedNodes} setSelectedNodes={setSelectedNodes} />

// frontend/src/pages/admin/autoDiscovery/Header.tsx:59-63 —— 唯一消费者只显示计数
{selectedNodes.length > 0 && <Text ...>({selectedNodes.length} selected)</Text>}
```
`NodeTable.tsx:312-319` 有全选/单选；但 `grep selectedNodes` 在 `pages/admin` 里没有任何批量写入调用；批量动作只在独立页 `pages/bulk/index.tsx`（它自己重新拉列表、自己再勾一次）。
CHANGELOG v0.1.36（`CHANGELOG.md:534`）写的是"Multi-select the node table, switch on the fields…"，这个流程在今天的界面上不存在。
**改法（S）。** 选中后出现操作条："批量编辑（跳 `/standalone/bulk/bulk.html?uuids=…`，页面预选）"、"远程执行（跳 `/admin/exec` 预选）"；或先移除勾选框，避免误导。**验收：** 挂载测试点两条 → 跳转 URL 携带正确 uuid 集合。

#### FG-14（P2）uptime：已上报、已展示，但没存成指标（OPEN-WORK 条目需收窄）

`agent/monitoring/monitoring.go:152-157` 一直上报 `uptime`；面板 `latest_status.go:41,67` 透出，前端 `components/Node.tsx:79`、`NodeTable.tsx:347` 已经在显示。
缺的是**历史**：`internal/metricstore/definitions.go:64-92` 没有 uptime 定义，`report_batcher.go:446` 只在流量连续性里用它。
所以 `OPEN-WORK.md` 的 "No uptime / restart-count metric exists … the panel cannot state 'how long was this node up'" 中**后半句已不成立**。
**改法（S–M，不需要 agent 发版）：** 加 `system.uptime` gauge 定义并在 `WriteReport` 落点；由序列下降算重启次数；节点页/SLA 展示。这是"数据已到、只差落库"的典型。

---

## 3. 与 `docs/OPEN-WORK.md` 的关系

### 3.1 新增（OPEN-WORK 中没有，本文首次提出）

FG-01、FG-02、FG-03、FG-04、FG-05、FG-06、FG-07、FG-08、FG-09、FG-10（筛选/导出部分）、FG-11、FG-12、FG-13、FG-15、FG-16、FG-17、FG-18、FG-19、FG-20。

其中 FG-07 有特殊性：它指向一条**已在 ROADMAP 标为 closed** 的验收项（H3 "status page distinguishes maintenance from down"），
OPEN-WORK 作为"仍待办清单"没有承接它——这正是"已关闭但验收未满足"的遗漏，建议把它当成 H3 的未完成尾巴处理，而不是新功能。

### 3.2 补充证据 / 建议修正措辞

| OPEN-WORK 条目 | 本文结论 |
|---|---|
| "No uptime / restart-count metric exists … 面板无法回答'这台机器在线多久'" | **措辞已过时**：实时 uptime 已上报且已展示（FG-14 证据）。真实缺口收窄为"历史序列 + 重启次数"。建议改写该条并降级为 S–M |
| Security / "No supported way to rotate a client token" | 维持；**建议升级为下一版 P1**，与 FG-01 合批：轮换 API key 与 client token、"导出含密钥需 step-up"三件事同属"凭据生命周期"，一次做比三次做便宜 |
| Security / "API keys are exempt from the sensitive-operation step-up" | 与 FG-01 同一个策略决定：如果继续豁免，则 FG-01 的修复对 API key 无效；建议在同一批里明确"API key 不豁免敏感操作"或写清豁免范围 |
| Security / "admin:getLogs accepts an unbounded limit" | 与 FG-10 同文件、同批：一行 clamp + 筛选/导出 |
| Numbers / "Ping latency is reported in whole milliseconds" | 不重复；与 FG-20（HTTP 状态码/证书）同属"探测表达力"主题，可同车但不必同批 |
| 前端 polish（~33 处 toLocaleString、mutation-envelope 24 处、8 处 dangerouslySetInnerHTML） | 不重复；FG-18（错误信息 i18n）与之相邻但不重叠 |
| ROADMAP H6 验收（`ROADMAP.md:475-488`） | H6 在 CHANGELOG `v0.1.42`（`CHANGELOG.md:409-425`）已交付 RPC/agent 侧；本文 FG-03/04/05 是它**未满足的验收项**（canary、自动回滚、面板代传、operator 可见结果），建议把 H6 从"已发布"回标为"半程" |

### 3.3 明确不重复、本文不再计数的条目

客户端 token 轮换、隐藏节点泄露（已修）、dbQuery step-up（已修）、插件备份白名单（已修）、SSRF 默认值、会话缓存锁、
主题 `/plugin/*` 归属、`DeleteAllSessions()` 全局、`sso_id` 策略、OIDC+2FA pending token、agent 以 root 运行、
上传归档不可复现、前端 mutation-envelope 重复实现、时间戳时区、React Compiler 采纳——均为已有条目，本文只在上表说明是否建议提级。

---

## 4. 建议实施顺序（功能路线图建议）

### 批次 A — 下一版（v1.6.13）：安全 + 可达性，纯面板，不动 fleet

| 项 | 依赖 | 验收 |
|---|---|---|
| FG-01 敏感操作名单 | 无 | 带 2FA 的会话不带 code 调 `exportConfig(include_secrets)`/`rollAgentUpdate` 被拒；名单测试变红即提醒 |
| FG-02 五个页面入口 + FG-19 URL 修正 | 无 | 菜单可达；新增"每个 standalone 页至少一条菜单引用"的测试 |
| FG-03 H6 UI | FG-01（同一批先后） | 页面展示版本分布与逐节点 outcome |
| FG-06 维护窗口压 ping/负载告警 | 无 | 窗口内不发 Alert、窗口外照发；两条测试 |
| FG-08 选中→批量入口 | FG-02（跳转目标） | 挂载测试断言 URL uuid 集合 |
| FG-14 uptime 指标 | 无 | 序列有值；重启后下降→重启次数 +1 |
| （已有）client token / API key 轮换 | 凭据策略决定 | 轮换后旧凭据立即失效、新凭据可用 |

**理由：** 只有一个 P0，其余全是 S；不改数据库 schema（FG-14 加指标定义不改表）、不碰 agent，一次容器重启即可回滚。
上线即把"已交付但不可达"的 5 个功能变成可用，是这份报告里单位工作量收益最高的一批。

### 批次 B — 下两版（v1.6.14 / v1.6.15）：正确性联动 + 运维效率

| 项 | 依赖 | 说明 |
|---|---|---|
| FG-04 H6 目标持久化 + 连接重发 | 需要 schema 或 config 键 | 与 FG-03 同一功能收尾；建议先做可测的"重启后仍钉版本" |
| FG-07 维护窗口 → 节点状态 + SLA | 需要先定义"维护不算停机"的口径 | 与 FG-06 同主题；SLA 是客户可见数字，必须带测试改 |
| FG-05 面板代传 agent 资产 | **需要 agent 发版** | 与下一批 agent 改动同车（ROADMAP 的批次规则：agent 变才动 fleet） |
| FG-10 审计日志筛选/导出 + limit clamp | 无 | 事故复盘的最小可用集 |
| FG-12 远程执行历史 | 无 | 后端已有，只缺列表 |
| FG-09 分组/标签筛选 | 无 | 舰队规模变大后收益线性增长 |
| FG-16 批量"复制设置" + 通知策略 | FG-02 | H2 范围补齐 |
| FG-18 错误码 + i18n | 需要 RPC 错误信封约定 | 先高频 20 条，不追求一次做完 |

### 批次 C — 更远：能力补齐（大多需要 agent 或产品决策）

- **FG-13 CPU 温度 / 磁盘 I/O、FG-20 HTTP 状态码/证书到期**：一个 agent 批次一起发（都是"采集层新增"）；建议先做证书到期（告警价值最高、数据量最小）。
- **FG-11 历史数据导出（CSV）**：与报表需求一起做；先明确"导出=原始样本还是聚合桶"，避免再造一次 24h 流量口径事故。
- **FG-15 成本预测**：需要产品定义计费模型（固定价/按量/超额单价），在确定前不要写代码。
- **FG-17 用户/RBAC**：产品决策项（是否保持单管理员）。若决定不做，应该写进 README 的"设计取舍"，而不是留在审计里。
- 进程列表、移动端表格、通知渠道扩展：体验项，等前两批稳定后再排。

---

## 5. 未能验证 / 假设（请勿当成结论）

1. **未运行面板与浏览器**：所有"界面不可达/无入口"结论来自源码与构建产物，不是渲染验证；FG-02/08/09 建议由 Lead 用 `panel-smoke` 与一次真实点击复核。
2. **移动端表格**：`NodeTable` 容器是 `overflow-hidden`（`NodeTable.tsx:297`）且 9 列，**是否横向裁切未渲染验证**；不作为发现计数。
3. **FG-05 的 PZYC 现状**：PZYC 是否仍无法访问 GitHub release 资产来自 ROADMAP 的历史记录（`ROADMAP.md:462-466`），未在本次核实。
4. **FG-07 的 SLA 影响程度**：取决于该舰队维护窗口内是否真的停止上报；未用生产数据量化。
5. **`admin:exportConfig` 的密钥范围**：`configxport.Export` 的字段清单未逐项审计（只确认"存在 include_secrets 且无 step-up"这一事实）。
6. **FG-01 的可达性**：需要 admin 会话（或 API key，见 OPEN-WORK 条目）；未在运行时验证，但 `transport.go` 的 step-up 只按 `IsSensitive` 判定，静态链完整。

---

## 6. 复核命令（本次实际运行）

```powershell
# 基线
git log -1 --format='%h %ad %s' --date=short          # b48963c 2026-10-17 docs(deploy): record the v1.6.12 rollout
git describe --tags --always                          # v1.6.12-1-gb48963c

# FG-01 敏感名单与两条未登记方法
Select-String -Path web/rpc/jsonrpc/*.go -Pattern 'MarkSensitive'
Select-String -Path web/rpc/jsonrpc/sensitive_stepup_test.go -Pattern 'admin:'
Select-String -Path web/rpc/jsonrpc/admin.configxport_apply.go -Pattern 'include_secrets|RegisterWithGroupAndMeta'
Select-String -Path web/rpc/jsonrpc/admin.agentupdate.go -Pattern 'RegisterWithGroupAndMeta|DispatchV2Event'

# FG-02 入口
Select-String -Path frontend/src/config/menuConfig.json -Pattern 'standalone'          # 0
Get-ChildItem -Recurse frontend/src -Include *.ts,*.tsx | Select-String '/standalone/' # 仅 entries/*.tsx 注释
Get-ChildItem -Recurse theme-luminaplus/src -Include *.ts,*.tsx | Select-String '/standalone/'  # 0
Get-ChildItem frontend/dist/standalone -Recurse -Filter *.html                        # 5 页 + admin

# FG-03/04/05 H6
Select-String -Path web,agent -Recurse -Include *.go -Pattern 'MethodAgentUpdate'     # web 只有 admin.agentupdate.go:156
Select-String -Path agent/update/target.go -Pattern 'on connection|Held in memory'
Select-String -Path agent/update/update.go -Pattern 'githubAPIBaseURL|github.com'

# FG-06/07 维护窗口
Select-String -Path utils/notifier/*.go -Pattern 'For(|Decide(|maintenance'          # load.go 0 命中
Select-String -Path internal/sla/*.go -Pattern 'maintenance'                           # 0
Select-String -Path web/rpc/jsonrpc/latest_status.go -Pattern 'Online|Uptime'

# FG-08 选中状态
Select-String -Path frontend/src/pages/admin -Recurse -Include *.tsx -Pattern 'selectedNodes'

# FG-14 uptime
Select-String -Path agent/monitoring/monitoring.go -Pattern 'Uptime'
Select-String -Path internal/metricstore/definitions.go -Pattern 'uptime'             # 0
```
