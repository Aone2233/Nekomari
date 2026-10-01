# 55 条编号项（51 条 finding）的逐条现状对照 — 2026-10-01 审计 × `codex/v1.6.9-hardening`

本文把 [BUG-AUDIT-2026-10-01.md](./BUG-AUDIT-2026-10-01.md) 的每一条 finding 拿到**当前代码**上重判：
已修 / 部分修 / 未修 / 过时 / 非缺陷。**不看文档里的 ✅**，只看代码与 commit。

> **口径更正。** 标题原写"49 条"，那是任务描述里的数字，与报告自身不符：文件里实有
> **55 个 `## P` 编号标题**，其中带严重度的 **51 条**（差 4 条分别是 `P0-1`、`P8-0`、
> `P9-3`、`P9-4`——非项目缺陷或收尾记录）。本文以 **55/51** 为口径，逐条判定见下表。

## 判定基准（重要）

| 项 | 值 |
|---|---|
| 分支 / HEAD | `codex/v1.6.9-hardening` @ `f616a77`（Merge PR #61） |
| 审计报告的基线 | `main@01dccaf`（报告第 3 行自述）；本文用 `git diff --stat 01dccaf..HEAD` 判断"自审计以来哪些文件真的动过" |
| 判定时点 | 2026-10-01 23:43（本地，CST） |
| 工作区状态 | **非静止**：本批次另有写者正在改 `agent/`、`web/api/`、`web/router/`、`.github/`。见下面「在途修复」一节 |
| 只读约束 | 本文只新增本文件；未改任何其它文件；未 commit / push / 切分支 |

**在途修复（工作区已改、尚未提交，判定时点的 blob hash）：**

| finding | 工作区文件（blob） | 状态 |
|---|---|---|
| P1-6 | `agent/server/task.go` `2d9312c3…`、`agent/server/ping_retry_test.go` | 已改为"慢不等于丢"（`task.go:605` 起报实测延迟）；HEAD 上仍是 `-1` |
| P1-7 | `web/api/public/oauth.go` `79881f3e…`（判定时点仍在变）、`web/router/router.go` `296e5ec6…` + `web/api/public/oauth_secondfactor_test.go`、`web/router/oauth2_bind_sensitive_test.go` | 已加 `verifySSOSecondFactor`（`oauth.go:180/221`）与 `/oauth2/bind`、`/oauth2/unbind` 的 `RequireSensitive2FA()`（`router.go:146-147`）；HEAD 上没有 |
| P1-5 | `database/unlock/unlock.go` + `web/api/ipinfo/handler.go`（新 `LoadVisible`/`ClientHistoryReadable`/`ErrNotVisible`）、`web/api/ipinfo/zz_temp_repro_test.go` | 判定时点仍在改；HEAD 上无 `LoadVisible`，`handler.go` 仍直接取 uuid |
| 其它在途 | `agent/cmd/root.go`（`-t` 弃用告警，**不是** P1-15 的优先级修复）、`agent/cmd/token_deprecation_test.go`、`.github/workflows/docker.yml`（latest 门禁）、`.gitignore`、若干 docs | 与 finding 无关 |

> 结论：**P1-5、P1-6、P1-7 已在修，不要重复派活**；在途集合是快照（写于判定时点），提交前请以 `git status` 为准。`git hash-object` 值可用于确认我核对的那一版是否已被继续改动。

---

## 总表

判定取值：`已修`（已提交，可由 commit/代码复现）｜`已修（运维）`（生产侧动作，仓库不可验证，仅有报告证据）｜`部分修`｜`在途`（工作区未提交）｜`未修`｜`过时`（报告描述与代码不符）｜`非缺陷/记录`

| # | 严重度 | 一句话 | 判定 | 判定依据（当前 file:line 或 commit） |
|---|---|---|---|---|
| P0-1 | 运维 | NOSLA 宕机=服务商计划维护，非缺陷；真结论是"代理单点"耦合 | 非缺陷/记录 | 报告自述已定性；耦合由 F4 运维改动处理（FIX-PLAN F4 验收段有实测证据），仓库无对应代码 |
| P1-1 | 严重 | 9/10 节点（遗留 agent）流量读数整体为空 | 已修 | `aa47ae7`（回退读周期累计）+ `2c16c76`（回退守自己的契约：按 `spec.metricKey` 查表 `public.metric.go:442`、`lastPublicTrafficBins` 下采样 `:470-475`）；测试 `web/rpc/jsonrpc/public_metric_test.go:616+` |
| P1-2 | 严重·安全 | agent token 明文进 nginx 访问日志 | 已修（代码） | `4adf64e`（服务端头分支 `web/api/Auth.go:181-183` + agent 3 处）、`6db6ba7`（终端 + file_stream：`agent/server/websocket.go:509`、`file_stream.go:135/191`）、`7f259a9`（WS Origin 认 Bearer，`web/security/origin.go`）。运维项（nginx 脱敏、9 台轮换、清日志）只有报告证据 |
| P1-3 | 高 | 管理页"最近 24 小时流量"放大约 286 倍 | 未修 | `frontend/src/pages/admin/dashboard.tsx:487-488` 请求 `last`，`:281`/`:289`/`:302-303` 仍把 `last` 累加，`:1060-1061` 渲染在 `:1020` 的 "Last 24h traffic" 下；`git diff 01dccaf..HEAD -- dashboard.tsx` **只有 13 行 P5-1 提示** |
| P1-4 | 高 | 配额告警与预测把"开机累计"当"本周期已用" | 未修 | `utils/notifier/traffic.go:66` 仍传 `r.Network.TotalUp/TotalDown`；语义见 `protocol/v2/jsonrpc.go:243-251` 与 `internal/metricstore/report_mapping.go:36-39`（`traffic.*` 才取 `CycleUp/Down`） |
| P1-5 | 高·安全 | `/api/public/ip-info/v1/lookup` 可匿名按 uuid 取出口 IP | **在途** | HEAD：路由无鉴权 `web/router/router.go:55`；`web/api/ipinfo/handler.go:143` 直接 `loadUnlock(uuid)`（`:166`），无 hidden/存在性校验（对比 `web/rpc/jsonrpc/public.go:57`）。工作区已加 `database/unlock/unlock.go` 的 `LoadVisible`/`ErrNotVisible` 并改 `handler.go` |
| P1-6 | 高 | RTT 恒 >1 s 被上报为 100% 丢包 | **在途** | HEAD：`agent/server/task.go:590-595` 仍 `return -1, false`（`git show 01dccaf:agent/server/task.go` 同样）；工作区已改为上报最后一次实测延迟（`task.go:605`），`ping_retry_test.go` 同步改 |
| P1-7 | 中·安全 | OAuth 登录/SSO 绑定绕过 2FA | **在途** | HEAD：`web/api/public/oauth.go:177` 直接 `CreateSession`、`web/router/router.go:144` bind 无 `RequireSensitive2FA`；工作区已加 `verifySSOSecondFactor`（`oauth.go:180/221`）与 `router.go:146-147` |
| P1-8 | 中 | 市场下载 SSRF 保护 fail-open | 未修 | `web/api/admin/market_download.go:196-207`：记录不存在 → `config.Set(..., false)` → `return false`；`:62` 用 `http.DefaultTransport` |
| P1-9 | 中 | 公开 ping 任务接口泄露隐藏节点 UUID | 未修 | `web/rpc/jsonrpc/public.go:243-270` 原样返回 `task.Clients`，无 hidden 过滤（同文件 `:57` 有正确规则） |
| P1-10 | 中 | netstatic：>~3 Gbps 静默丢流量；注释承诺的分支不存在 | 未修 | `agent/monitoring/netstatic/static.go:432-437` `ceilingBytes` 只有假定 1 Gbps；`:422` 注释仍宣称"真实速率优先分支"（全仓库无 `/sys/class/net/*/speed` 读取）；`:527-528` `invalidDeltaLogged` 只置位不复位 |
| P1-11 | 中 | netstatic 网卡白名单是启动快照 | 未修 | `agent/cmd/root.go:130-135` 仅启动时 `InterfaceList()`→`SetNewConfig`；`static.go:897-901` 存下；`:505` `isNicAllowed` 精确匹配 |
| P1-12 | 中 | `--include-nics` 无法包含 br*/vmbr*/veth*/tap*/lo* | 未修 | `agent/monitoring/unit/net.go:508-531`：`loopbackNames` 前缀过滤（`:510-514`）**先于** `includeNics`（`:517-521`） |
| P1-13 | 中 | agent 镜像缺容器标记 → 容器内自我更新 | 未修 | `Dockerfile.agent`（发布镜像，`.github/workflows/docker.yml:80` 指定）无 `touch /.komari-agent-container`；标记只在无人使用的 `agent/Dockerfile:11`；`agent/update/update.go:40,155` 仍据它判断 |
| P1-14 | 中 | "Used > Total" 整份上报硬拒 | 未修 | `database/clients/report.go:120-121` 仍 `return fmt.Errorf("resource used value exceeds total")` |
| P1-15 | 低-中 | 配置文件里的 token 盖过 `-t`/`--token-file` | 未修 | `agent/cmd/root.go:87` `loadFromEnv` → `:88-97` 配置 JSON 反序列化进 `flags` → `:100` `resolveToken`；`agent/cmd/token.go:127` `if flags.Token != ""` 提前返回；`root.go:98-99` 注释仍写相反顺序。在途的 root.go 改动是 `-t` 弃用告警，**没碰优先级** |
| P1-16 | 低 | `pkg/jsruntime` 的 `-race` 失败（数据竞争） | 已修 | `2e63569`：`pkg/jsruntime/console/console.go:39` `writeMu` + `:111-113`；`syncbuffer_test.go` |
| P1-17 | 低 | `common:getRecords` 无窗口上界；已删除节点历史仍对访客可见 | 未修 | `web/rpc/jsonrpc/common.record.go:56-61`（`hours` 无上界）、`:77`（只查 hidden，不校验存在性） |
| P1-18 | 低 | `common:getMe` 的 agent 分支把 token 当 uuid 返回 | 已修 | `f3839e3`：`web/rpc/jsonrpc/common.go:459-465` 现为 `if err == nil { resp.UUID = client }`，失败留空 |
| P1-19 | 低 | 密钥比较未用常量时间 | 未修 | `web/api/Auth.go:162`、`:243`；`web/api/client/autoDiscovery.go:24` |
| P1-20 | 低 | 已删除节点的指标序列未清理 | **过时** | 级联删除一直存在：`web/rpc/jsonrpc/admin.client.go:133` `metricstore.DeleteEntityAsync(params.UUID)`，`git show 01dccaf:web/rpc/jsonrpc/admin.client.go` 第 133 行相同，引入自 `0ca87aa`。线上 18 个 entity 是历史残留，**不是缺代码** |
| P1-21 | 低 | 跨升级窗口的遗留 ping 分位数消失（报告自述属预期） | 未修 | `web/rpc/jsonrpc/public.metric.go:1511` 仍置 `legacy_quantiles_unknown`；遗留迁移不产出分位数。报告自己标"已知且预期"→**不建议排期** |
| P1-22 | 中·能力缺口 | 面板存不下"在线时长/重启次数" | 未修 | `internal/metricstore/definitions.go` 无 uptime 定义（grep 无命中） |
| P1-23 | 低-中 | 单个指标的参数问题打掉整批查询 | 未修 | `web/rpc/jsonrpc/public.metric.go:241-243` 仍整批 `return nil, rpc.MakeError(InvalidParams, …)` |
| P2-1 | 严重 | `/install`、`/plugin/*` 被主题文档接管 | **部分修** | `/install` 已修：`web/public/public.go:301` `panelOwnedPrefixes` + `:616` `isPanelOwned`；测试 `panel_routes_test.go`、`embedded_theme_test.go`。**`/plugin/*` 仍未**：`public.go:299-300` 注释、`panel_routes_test.go:153` 断言 `/plugin/demo` = false |
| P2-2 | 中 | 面板主构建（含 SW）不在发布归档中 | 已修 | `8540da3`（归档改为从 `frontend/dist` 组装，含 sw.js/registerSW.js/workbox/manifest、`admin/`；`dist.tar.zst` 8.57 MB→5.38 MB）+ `abb294c`（`public.go:347` `assetOnlyExtensions`、`:365` `isAssetRequest` → 缺资源 404 而不是 SPA 外壳）。测试 `pwa_assets_test.go`、`embedded_theme_test.go` |
| P2-3 | 高 | 批量编辑的 `billing_cycle` 单位是"月" | 已修 | `d71a74a`：`frontend/src/types/Bulk.ts:83-95` 改为 -1/30/92/184/365/730/1095；`frontend/script/bulk-fields.test.mjs` 同步修断言 |
| P2-4 | 高 | "未采集"（null / quality≠ok）在主题里被渲染成 0 | 未修（主题侧） | 生产装的是第三方 SAO 主题（本仓库外），报告实测其 bundle 无 `quality`、百分比兜底 0。仓库内默认文档已换成面板自己的前台（`8540da3`），其渲染走 `frontend/src/utils/liveData.ts:29-31` / `usagePercent`（null-aware）→ 剩余风险只在第三方主题 |
| P2-5 | 中 | `PriceTags` 没有"无到期时间"状态 | 未修 | `frontend/src/components/PriceTags.tsx:47-48` 仍只判 `undefined` |
| P2-6 | 中 | 筛选状态下拖拽只重排可见子集 | 未修 | `frontend/src/pages/admin/index.tsx:43-48` 先 filter 再 sort、`:78` 传 `filteredNodes`；`nodeTable/NodeTable.tsx` 提交可见子集权重 |
| P3-1 | 严重·安全 | `public:getSlaReport` 空实体列表返回全队 | 已修 | `28230ca`：`web/rpc/jsonrpc/public.sla.go:105-120` 判空返回空报告；测试 `public.sla_test.go:169+` |
| P3-2 | 严重 | 维护窗口内离线：延迟告警发不出、恢复通知被吞 | 已修 | `935888d`（`utils/notifier/offline.go:95-110` 抑制分支置 `isConnExist=false`；`maintenance_defer.go:175-181` 补发后收敛 pending）+ `ba5be87`（补发未成功前不 forget）；测试 `maintenance_defer_positive_test.go` |
| P3-3 | 高 | 同一 task 多个 tag 世代被 append 拼接 | 未修 | `internal/sla/query.go:305` 仍 `tasks[taskIDOf(tags)] = append(...)`；下游 `internal/sla/availability.go:358-396` 按输入顺序合并 |
| P3-4 | 中 | `Presence.Coverage` 不是"窗口覆盖率" | 未修 | 字段文档 `internal/sla/availability.go:44-49` vs 实现 `:193-194`（`span := LastData.Sub(FirstData)`）；`batch_test.go:144-160` 已把该行为钉住 |
| P3-5 | 中 | 预测只识别"净下降"的重置 | 未修 | `internal/forecast/forecast.go:250` 仅 `bytesPerSecond < 0` |
| P3-6 | 中 | `CrossesAt` 的 `time.Duration` 溢出 | 未修 | `internal/forecast/forecast.go:289-290` 无上界 |
| P4-1 | 中·运维 | 备份与数据库同盘、无异地副本 | 已修（运维） | FIX-PLAN F3c 记录：已复制到 MAC-WAN 并做 `sha256sum -c` + 两个库 `quick_check` 验证；仓库无代码改动。附带"改 daily prune"的结论已作废 |
| P5-1 | 低 | `ram_mode: htoplike` 名字与公式不符 | 已修 | `d71a74a`：`frontend/src/pages/admin/dashboard.tsx:1347-1350` `hint` + 5 个 locale 的 `dashboard.memoryCaliberTip`；线缆值 `ram_mode` 未动 |
| P6-1 | 中 | 失败备份留下截断 zip；坏档让保留策略永久停摆 | **部分修** | 代码半已修：`f542de8`（临时名→校验→rename，`database/dbcore/dbcore.go:38-127` 含 `verifyZipArchive`）。**脚本半未修**：`deploy/prune-upgrade-backups.sh:44-52` 的 python3 校验仍裸跑在 `set -e` 下（应放进 `if !`），一个坏档仍会中止整个 prune；该文件自 `bd6580c` 起未改（线上是否单独修过无法从仓库验证） |
| P6-2 | 低-中 | 自动升级备份按设计排除 `metrics.db` | 未修（设计取舍） | `database/dbcore/dbcore.go:295` 仍排除 `metrics.db`/`-wal`/`-shm`；`dbcore.go:279` 注释说明了取舍 |
| P6-3 | 低-中·未证实 | 遗留迁移无断点标记，崩溃从头重跑 | 未修 | `internal/migrations/legacy_monitoring.go:171/182/225` 仍无落库进度；报告已论证重跑幂等（不污染数据） |
| P7-1 | 中·潜伏 | 保留期短于层级跨度时整层 rollup 被无条件删除 | 未修 | `pkg/metric/store.go:1289`（`RetentionDays == 0`）、`:1301`（`all: true`）、`:1333`（按 `all` 删） |
| P7-2 | 高 | 启动恢复：快照失败不阻止删除、解压失败仍删档 | 已修 | `f542de8`（`database/dbcore/dbcore.go:602+`：`:631`/`:637` 拒绝恢复并保留 `backup.zip`，`:649-651` 解压失败保留归档与标记）+ `8e04df3`（`web/backup/restore.go:232` `ValidateArchive` 增 SQLite 魔数/页数/`quick_check`，`:350`）；测试 `restore_guard_test.go`、`validate_content_test.go` |
| P7-3 | 高 | mmdb 更新在每条错误路径泄漏写锁 | 已修 | `8346bcd`：`utils/geoip/mmdb.go:139-145`（抽 `downloadToTempFile`）、`:153-155` `defer Unlock`、60 s 超时、`:190` `validateMmdb`、临时文件+rename；测试 `mmdb_update_test.go` |
| P7-4 | 中 | 恢复页覆盖 `MigrationTargetKey` → 迁移自认"源=目标" | 未修 | `internal/metricstore/store.go:159`/`:175` 仍写 `MigrationTargetKey = 新 DSN`；`store_migration.go:82` 以它为源、`:101-103` 源==目标即拒绝 |
| P7-5 | 中 | ipinfo 把空国家当成功并缓存 48 h；空 provider 无日志 | 未修 | `utils/geoip/ipinfo.go:75-78` 无空值检查；`utils/geoip/geoip.go:24` 48 h 缓存 + `:128-136` 只按 `err == nil` 缓存 |
| P7-6 | 低 | 自定义 docker data-root 下"持久"警告结论相反 | 未修 | `database/dbcore/dbcore.go:476` 正则 `^/var/lib/docker/volumes/[0-9a-f]{64}/_data$` + `:496` default 分支打印 "persists" |
| P8-0 | — | 轮换事故经过（MAC-WAN 13 分钟） | 非缺陷/记录 | 报告自述已完全恢复，属过程记录，无待办 |
| P8-1 | 中·能力缺口 | 没有"轮换 client token"的管理接口 | 未修 | `web/rpc/jsonrpc/` 内无 rotate/regenerate（grep 无命中）：只有创建时生成与 `admin:getClientToken`（`admin.client.go:164`） |
| P8-2 | 中 | 认证依赖索引，索引不一致时静默 401 | 未修 | 仓库内无 `integrity_check`（grep 无命中）；报告实测 `quick_check` 抓不到索引缺失 |
| P8-3 | 低·运维 | 轮换的正确顺序与两个易踩点 | 非缺陷/记录 | 属流程建议，报告已给顺序并在生产按此执行；无代码可改 |
| P9-1 | 中·安全 | auto-discovery key 经命令替换进进程命令行 | 已修（运维） | FIX-PLAN F13：SDE9929 单元改 `--token-file`，`ps` 无 key、无重复节点。注意 `deploy/install-node-agent.sh:102-103` 仍支持把 key 作为命令行参数（flag 本身如此），`deploy/README.md:208-210` 已推荐用节点 token |
| P9-2 | 中·安全 | agent 把身份文件写成 0644 | 已修 | `4adf64e`：`agent/cmd/autodiscovery.go:120` 写 0600、`:64-74` `tightenIdentityFilePermissions` 读取时就地收紧；测试 `agent/cmd/autodiscovery_test.go` |
| P9-3 | — | 全队凭据卫生普查与收尾 | 非缺陷/记录 | 报告记录 10 台逐一核查结果，无代码待办 |
| P9-4 | — | `auto_discovery_key` 已轮换 | 非缺陷/记录 | 报告记录旧 key 注册 → 403 的负面验证 |

---

## 仍未修 → 建议排期

共 **30 条**（另有 2 条部分修、**3 条在途**（P1-5/P1-6/P1-7）、1 条过时，见文末计数）。

### 安全（5 条）

**P1-9**、**P1-8**、**P1-19**、**P8-1**、**P8-2**

- P1-9 与 P1-5 是同一条链：P1-9 给出隐藏节点的 UUID，P1-5 用这个 UUID 取它的出口 IP。在 `private_site=false`（默认值）的公开面板上，两步都不需要任何会话。**P1-5 已在途修复**，但 P1-9 这一半没人动——只修一半，UUID 仍然可枚举。
- P1-8 的 SSRF 保护默认 fail-open，且注释（`market_download.go:41-43`）声称 fail-closed —— 代码与注释相反。
- P8-1 是本报告里"唯一因为执行修复才发现"的缺口：没有受支持的轮换路径，运维只能直接写运行中的 SQLite，而这正是 P8-0 那次 13 分钟事故的根因。P8-2 让同类故障"静默变成 401"（`quick_check` 抓不到）。两条不做，下一次凭据事件会以同样方式复现。

### 数据安全 / 可恢复性（5 条）

**P7-1**、**P7-4**、**P6-2**、**P6-3**、**P7-6**（P6-1 的脚本半为"部分修"，也建议一起收）

- P7-1（整层 rollup 被无条件删除）与 P7-4（恢复页改写 `MigrationTargetKey` 后迁移自认"源=目标"）都是**只在真出事时才暴露**的类型，且当前靠"迁移自己设了长保留期"这类巧合成立。
- P6-2 是明示的设计取舍，建议至少把"指标库不在这条自动回滚路径里"写进升级日志/文档。
- P6-1 的代码半已修，**残余风险因此下降**（不再产生截断 zip）；但脚本仍会被历史坏档或位翻转卡死，改动只有 5 行。

### 数字正确（14 条）

**P1-3**、**P1-4**、**P1-10**、**P1-11**、**P1-12**、**P1-21**、**P1-22**、**P2-4**、**P2-5**、**P3-3**、**P3-4**、**P3-5**、**P3-6**、**P7-5**

- P1-3（首页流量卡片 286×）与 P1-4（配额告警/预测拿开机累计当月用量）是**同一根因的两次出现**：把"累计量"当"区间量"。建议一次改完并补数值验收（管理页流量卡片从未被数值验收过）。
- P1-10/11/12 是 netstatic 的三处漏计/错计，直接让"本周期流量"与 `net.total.*` 分叉。
- P1-21 报告自己标注"已知且预期"，**不建议排期**（列在此处仅为计数完整）。
- P3-3/P3-4 是 SLA 曲线的可比性与字段语义问题；P3-5/P3-6 是预测的两个边界错误。

### 其余（可用性 / 健壮性 / 体验）（6 条）

**P1-13**、**P1-14**、**P1-15**、**P1-17**、**P1-23**、**P2-6**

- P1-13：**下次发布会触发**——手工 `docker run`（未加 `--disable-auto-update`）的 agent 容器会自更新并 `exit(42)`，`restart: no` 时节点永久离线；修复是一行 `RUN touch`。
- P1-15：用 `-t NEW` 轮换时，配置文件里的 `token` 会静默获胜（注释与代码相反）——凭据事件期间这条最坑人。
- P1-14：整份上报硬拒会让节点"保持连接但冻结"，甚至被判离线。

### 最该优先排期的 5 条（我的排序，理由）

1. **P1-13**（容器标记）——唯一一条"**下次发布就会变坏**"的项，且修复成本一行；不修就是拿可用性赌没人手工 `docker run`。
2. **P1-9（配对 P1-5）**（安全连招）——公开部署形态下无需凭据即可枚举隐藏节点并取其出口 IP；**P1-5 已在途修复，P1-9 必须跟上**，否则隐藏节点的 UUID 仍然可枚举（只修一半等于没修）。
3. **P1-3 + P1-4**（累计量当区间量）——首页最显眼的数字错 ~286 倍，配额/预测口径错；同一根因，一次改完 + 补数值验收。
4. **P8-1 + P8-2**（轮换路径 + 索引告警）——P8-0 事故的根因是"没有受支持的轮换方式"，而认证又依赖索引且静默失败；不修则下次凭据事件原样重演。
5. **P7-1 + P7-4**（数据安全潜伏项）——只在真出事时暴露，且都不在本批次任何人的修复范围内。

> 次优先（值得在同一个发版批次里捎带）：**P1-15**（轮换静默无效）、**P1-14**（节点冻结）、**P6-1 脚本半**（5 行）、**P2-4**（主题把未知画成 0，需主题侧配合）。

---

## 报告本身写错 / 已过时 / 状态表不实

1. **P1-20 判定过时（报告写错）**：报告称"删除客户端时未级联清理指标序列"，但 `web/rpc/jsonrpc/admin.client.go:133` 的 `metricstore.DeleteEntityAsync(params.UUID)` **在审计基线 `01dccaf` 上就已存在**（`git show 01dccaf:web/rpc/jsonrpc/admin.client.go` 第 133 行；引入自 `0ca87aa`）。报告的证据只有线上"10 个 client / 18 个 entity"，没有读删除路径。→ 线上那批孤立序列是**存量**（含 P8 事故期间直接写库、迁移前的删除），正确动作是清一次存量，不需要新代码。
2. **"共 49 条编号项"与报告自身不符**：文件里实有 **55 个 `## P` 编号标题**；其中带严重度标记的 **51 条**，另 4 条是 `P0-1`（非项目缺陷）、`P8-0`（事故经过）、`P9-3`/`P9-4`（收尾记录）。49 既不等于 55 也不等于 51，最可能是漏计了后补的 `P1-22`/`P1-23`（或把 P9-1/P9-2 当记录）。排期时请以本文的编号清单为准。
3. **`FIX-PLAN` 状态表把"部分完成"写成"完成"**：`F5` 行写 "✅ 完成（`/install`、`/database-recovery`；`/plugin/*` 按计划未动）"——但审计 P2-1 的标题就把 `/plugin/*` 算在缺陷里，而这个前缀至今仍归主题（`public.go:299-300`、`panel_smoke`/`panel_routes_test.go:153` 都把它排除在外）。**这是"不要相信 ✅"的具体实例。**
4. **`FIX-PLAN` 状态表漏项**：`P1-5`/`P1-6`/`P1-7`（判定时点均在途）、`P1-18`、`P2-2` 都不在 F1–F15 表里（P1-18 由 `f3839e3` 修、P2-2 由 `8540da3`+`abb294c` 修）。表里也没有 `P8-1`/`P8-2`/`P1-3`/`P1-4` 等 30 条未修项。
5. **P1-16 的正文写了"✅ 已修复"，而 P1-16 的标题也带 ✅**——这一条的 ✅ 是对的（`2e63569`），但同一份报告里 P9-1/P9-2 的 ✅ 是**运维**完成（代码修复另有 commit），三种 ✅ 语义混用，容易被读成"代码都修完了"。
6. **P0-1 的"建议"与最终处置不一致**：报告建议"预先建维护窗口覆盖 NOSLA"，而 FIX-PLAN F4 的最终处置是改路由器直连规则、并明确"维护窗口（可选，低价值）——现在建窗口没有对象"。同一份材料里两条建议互相取消，容易让后来的执行者重复劳动。

### 行号漂移清单（对报告引用位置逐条核对）

**已漂移**（报告写的是 `01dccaf` 的位置，代码已被修复搬动）：

| finding | 报告引用 | 当前位置 |
|---|---|---|
| P1-1 | `public.metric.go:1034-1039`、`:1046-1056` | `trafficCumulativeMetrics` `:1258`、`publicMetricStorageKey` `:1284` |
| P1-2 | 五处 agent 拼接（`basicInfo.go:77` 等） | `basicInfo.go:76`、`websocket.go:138/241/509`、`task.go:686`、`files.go:829`，并新增 `file_stream.go:135/191` |
| P1-23 | `public.metric.go:231-233` | `:241-243` |
| P2-1 | `public.go:607-610` | `serveAdminDocument` `:741`、`/admin` `:749`、`/terminal` `:751-752`、`panelOwnedPrefixes` `:301` |
| P2-2 | `public.go:695-701`（被注释掉的分支） | 已实现：`assetOnlyExtensions` `:347`、`isAssetRequest` `:365` |
| P3-1 | `public.sla.go:88-92` | 判空在 `:105-120` |
| P6-1 | `dbcore.go:26-92`、`:294-297` | `zipDirectoryExcluding` `:38-127`、`backupOnVersionUpgrade` `:327+` |
| P7-2 | `dbcore.go:539-581`、`restore.go:187-213` | `doInitialize` `:602+`、`ValidateArchive` `:232`、`quickCheckDatabase` `:350` |
| P7-3 | `mmdb.go:134-165` | `:139-198`（整段重写） |
| P7-6 | `dbcore.go:409-430` | 正则 `:476`、告警 `:496` |
| 无问题清单 | `public.go:607-610`、`:585-598`、`:617-621` | `:741-752`、`:278-315` |

**未漂移**（逐条核对，行号仍精确；这也是"报告的证据可信"的一面）：P1-3（`dashboard.tsx:487-488`/`:281`/`:302-303`/`:1020`/`:1060-1061`）、P1-4（`traffic.go:66`）、P1-5（`router.go:54-56`）、P1-8（`market_download.go:196-208`）、P1-9（`public.go:243-269`）、P1-10（`static.go:416-423`/`:432-437`）、P1-11（`static.go:180-190`/`:897-902`）、P1-12（`net.go:508-531`）、P1-13（`docker.yml:80`）、P1-14（`report.go:117-122`）、P1-15（`root.go:87-100`、`token.go:126-129`）、P1-16（`console.go:112`）、P1-17（`common.record.go:55-62,64-80`）、P1-18（`common.go:454-464`）、P1-19（`Auth.go:162/236`）、P1-21、P1-22、P2-3（`Bulk.ts:84-92`）、P2-5（`PriceTags.tsx:47-48`）、P2-6（`index.tsx:43-49,78`）、P3-2（`offline.go:95-100`、`maintenance_defer.go:113-118`）、P3-3（`query.go:295-306`）、P3-4（`availability.go:193-199`）、P3-5（`forecast.go:240-256`）、P3-6（`forecast.go:288-291`）、P7-1（`pkg/metric/store.go:1288-1308`）、P7-4（`store.go:157-159,173-175`）、P7-5（`ipinfo.go:68-78`）、P8-1（无行号）。

---

## 计数

| 判定 | 条数 |
|---|---|
| 已修（代码已提交） | **12** — P1-1, P1-2, P1-16, P1-18, P2-2, P2-3, P3-1, P3-2, P5-1, P7-2, P7-3, P9-2 |
| 已修（运维完成，仓库不可验证） | **2** — P4-1, P9-1 |
| 部分修 | **2** — P2-1（`/plugin/*` 仍在）、P6-1（prune 脚本仍在） |
| 在途（工作区已改，未提交） | **3** — P1-5, P1-6, P1-7 |
| 未修 | **30** |
| 过时（描述与代码不符） | **1** — P1-20 |
| 非缺陷 / 记录 | **5** — P0-1, P8-0, P8-3, P9-3, P9-4 |
| **编号项合计** | **55** |

- 带严重度标记的 finding：**51 条** = 已修 14 + 部分修 2 + 在途 3 + 未修 30 + 过时 1 + 非代码缺陷 1（P8-3）。
- 任务描述里的 **"49"** 与 55（编号项）、51（带严重度）都不吻合，差 2 —— 见上文「报告本身写错」第 2 条。本文以 **55 个编号项 / 51 条 finding** 为口径。
- 未修 30 条的排期分组：安全 5 / 数据安全 5 / 数字正确 14 / 其余 6（合计 30）。
- **在途 3 条（P1-5/P1-6/P1-7）在提交前随时可能继续变化**；表里给的是判定时点的状态与 blob hash。

---

## 抽查：3 条"已修"判定如何复现

```powershell
# 1) P1-18（getMe 不再回显 token）——commit f3839e3
git log --oneline -1 f3839e3
git show f3839e3 -- web/rpc/jsonrpc/common.go | Select-String "err == nil"
# 当前代码：web/rpc/jsonrpc/common.go:459-465（if err == nil { resp.UUID = client }）

# 2) P7-2（恢复路径护栏 + 归档内容校验）——commit f542de8 + 8e04df3
git log --oneline -1 f542de8; git log --oneline -1 8e04df3
go test ./database/dbcore/ ./web/backup/ -count=1 -run "Restore|ValidateArchive|ZipDirectory"
# 当前代码：database/dbcore/dbcore.go:631/637（拒绝恢复）、:649-651（解压失败保留）；
#           web/backup/restore.go:232（ValidateArchive）、:350（quickCheckDatabase）

# 3) P1-1（遗留 agent 流量回退）——commit aa47ae7 + 2c16c76
git log --oneline -1 aa47ae7; git log --oneline -1 2c16c76
go test ./web/rpc/jsonrpc/ -count=1 -run TestPublicQueryMetricsFallsBackToTheCycleCounterForLegacyAgents -v
# 当前代码：web/rpc/jsonrpc/public.metric.go:442（按 metricKey 查表）、:470-475（回退也下采样）
```

反向抽查（"未修"也不是猜的）：

```powershell
# P1-3：自审计基线以来 dashboard.tsx 只多了 P5-1 的提示
git diff --stat 01dccaf..HEAD -- frontend/src/pages/admin/dashboard.tsx   # → 1 file changed, 13 insertions(+)
# P1-20：级联删除早就存在
git show 01dccaf:web/rpc/jsonrpc/admin.client.go | Select-String DeleteEntityAsync   # → 133: metricstore.DeleteEntityAsync(params.UUID)
# P1-13：发布镜像没有容器标记
Select-String -Path Dockerfile.agent -Pattern "komari-agent-container"   # → 无输出
```

---

## 本批次（v1.6.9）结束后的状态更新 — 2026-10-01

上面那张表是**判定时点**的快照（那时 P1-5/P1-6/P1-7 还在工作区、P1-13 还没修）。这一节记录该批次实际交付了什么，以及什么被**有意**留到下一批。

### 本批次修掉的

| 条目 | 结果 |
|---|---|
| **P1-5** | 已修。`/api/public/ip-info/v1/lookup` 的非空 `uuid` 现在会先被解析，并按"节点存在且非 hidden，或调用者是管理员"校验；hidden 与不存在回答**完全相同**（404 + 回显 uuid），空 `uuid` 仍 200 且不解析任何节点。`unlock.Load` 改为非导出，公开路径不再有第二条裸读入口。 |
| **P1-6** | 已修。`measureWithRetries` 只在某次 `measure()` 真的返回 error 时给出 `(-1,false)`；4 次探测全部成功（即使都 >1000ms）改为上报最后一次实测延迟。真丢包仍记为丢包。 |
| **P1-7** | 已修，fail-closed。SSO 回调在 `CreateSession` **之前**要求同一第二因子（同规则、同文案、同一个限流器）；`/oauth2/bind`、`/unbind` 挂 `RequireSensitive2FA`，管理页配套把码带上。 |
| **P1-13** | 已修。发布镜像实际使用的 `Dockerfile.agent` 补上 `RUN touch /.komari-agent-container`（原先只有无人使用的 `agent/Dockerfile` 有）。 |
| P2-1 的 `/plugin/*` 那一半 | 仍未修（维持原判定）。 |
| P6-1 的 prune 脚本那一半 | 仍未修（维持原判定）。 |

每一处的独立反向复核（复核者自己撤销修复、确认对应测试真的失败，再恢复）见 [VERIFICATION-2026-10-01.md](./VERIFICATION-2026-10-01.md)。

### 下一批的首位（**带已执行的复现证据**）

1. **P1-9 + P1-17 + 同一类的三个匿名面。** P1-5 修掉的是 ip-info 那一处；复核者用真匿名 principal 驱动真 handler 实测，同一类缺陷在另外三处仍然成立：
   - `common:getNodeRecentStatus`：hidden → `ERROR(-32602)`，unknown → `{"count":0,"records":[]}` —— **可区分，等于隐藏节点判定器**；
   - `common:getRecords`：hidden → `UUID not found`，unknown → `Failed to fetch records` —— 同上；
   - `public:getPublicPingTasks`：匿名**直接回显隐藏节点的 UUID**，它就是前两处的输入。
   最小修复：这四处共用同一条可见性规则（新导出的 `unlock.ClientHistoryReadable` 正好可以共享），让 hidden 与 unknown 答案一致，并从 `clients` 回显里滤掉 hidden。**必须同时核对主题契约**——本仓库已经吃过"客户端 strict schema 是服务端契约的一部分"的亏（`docs/OPEN-WORK.md` 条目 2），而这几处都是公开 RPC 载荷。
2. **P1-3 + P1-4**：首页"最近 24 小时流量"仍把累计量当区间量（`frontend/src/pages/admin/dashboard.tsx` 的 `:281`/`:289`/`:302-303`），配额告警与预测仍用开机累计。同一根因，一次改完并补数值验收。
3. **P8-1 + P8-2**、**P7-1 + P7-4**、**P1-8**、**P1-10…P1-12**、**P1-14**、**P1-15**：见上表，按"安全 > 数据安全 > 数字正确"排。

### 已知的不完美（不拦发布，但别读成"已解决"）

- `-ut v` 这种"布尔 shorthand 之后跟 `t`"的写法，`commandLineToken` 仍**漏报**（相反，`-e --token x` 会误报）。影响仅限那条警告本身，flag 的行为不变。
- `/bind` 的成功路径会被请求**两次**（前端 `redirect:"manual"` 预检 + 真正的跳转）。服务端无副作用（只重设同一个 cookie，`Verify2Fa` 无状态），但 TOTP 跨 30 秒窗口边界时第二次校验可能 401，用户会落到一个裸 JSON 页。
- `VerifySensitive2FACore` 的 API Key 豁免**没有任何测试覆盖**（既有设计，本次只做了确认）。
- **OIDC 启用 + 账号开了 2FA 时，SSO 登录现在是 fail-closed（会被拒），因为浏览器无法回答那个挑战。** 密码登录不受影响；本部署 OIDC 关闭，所以今日生产影响为零。可用化需要一个"第二次确认"步骤，见 `docs/ROADMAP.md` F 节第 12 条。

