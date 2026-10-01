# 修复包清单 — 2026-10-01

配套 [排查报告](./BUG-AUDIT-2026-10-01.md) 与 [修复方案](./FIX-PLAN-2026-10-01.md)。

> **当前计分板是 [FINDINGS-STATUS-2026-10-01.md](./FINDINGS-STATUS-2026-10-01.md)。** 它逐条核对报告里每一个编号项在当前代码上的真实状态（**55 个编号项 / 51 条带严重度的 finding**，不是早期任务描述里写的 49），比本文与 FIX-PLAN 的状态表都新；三者冲突时以它为准。

**这批改动已并入 `main`（PR #58，merge `c87df36`）。** 下面是当时分支上的提交。

## 分支上的提交（19 个）

```
$ git log --oneline main..HEAD
913e94e docs(audit): correct P2-1, and record the regression CI caught
f3839e3 fix(rpc): getMe answered with the agent token instead of the client uuid
3dcb104 fix(smoke): assert the panel's own document where it exists, and the redirect where it does not
ba5be87 fix(notifier): do not forget an alert that has not been delivered
6db6ba7 fix(agent): the last two places the agent put its credential in a URL
2c16c76 fix(metrics): keep the legacy traffic fallback inside its own contract
157bd62 fix(panel): reach the panel's own routes through the existing prefix rule
a2db815 docs(audit): the 2026-10-01 audit, its fix plan and the fix bundle
2e63569 fix(jsruntime): serialise console writes, and synchronise the test sink
d71a74a fix(frontend): billing cycles in days, and say what the memory figure means
88cf5bc fix(panel): stop the installed theme from taking over the panel's own routes
8346bcd fix(geoip): release the mmdb write lock on every error path
935888d fix(notifier): deliver a deferred offline alert, and its recovery
8e04df3 fix(backup): validate the archive's database before restoring it
f542de8 fix(db): make the pre-upgrade archive atomic, and guard the restore path
aa47ae7 fix(metrics): give legacy agents their cycle traffic back
28230ca fix(sla): an unreadable entity must not report the whole fleet
4adf64e fix(agent): keep the credential out of URLs, and the identity file owner-only
```

> **`88cf5bc` 里的 F5 机制在 `157bd62` 被推翻重做。** 前者把 `/install`、`/database-recovery` 注册成显式路由并返回 admin 文档——目标错了，且与 `internal/server/runtime.go` 已注册的 `/database-recovery` 冲突，让服务器在首次安装完成后 `panic: handlers are already registered for path '/database-recovery'`。CI 在合并前拦下了它。两个提交都保留在历史里，以便复核者看到这次修正本身。

## 恢复（若分支丢失）

> 这里原本指向 `docs/audits/2026-10-01/fixes-tracked.patch`（最初 29 个已跟踪文件的补丁），并记录过 `git apply --reverse --check` → **exit 0** 的自洽性校验。**该补丁已于 2026-10-01 删除**：这 19 个提交已全部并入 `main`（PR **#58**，merge `c87df36`），补丁只是同一内容的第二份副本，留着只会随 main 漂移，也会让复核者误以为它是权威来源。

```bash
# 这批修复的权威位置就是 main
git log --oneline 01dccaf..c87df36        # 审计基线 01dccaf → PR #58 合并提交 c87df36
```

## 改了什么（26 个已跟踪文件）

| 文件 | 改动 | 对应条目 |
|---|---|---|
| `web/api/Auth.go` | `extractClientToken` 增加 `Authorization: Bearer` 头分支（查询参数仍优先，向后兼容） | F1b / P1-2 |
| `agent/server/v2auth.go`（新） | 共享助手 `v2RPCEndpoint` / `setV2Auth` / `v2RPCAuthHeaders` | F1b / P1-2 |
| `agent/server/{basicInfo,task,files,websocket}.go` | 5 处把 token 从 URL 查询串移到请求头；WebSocket 用握手头 | F1b / P1-2 |
| `agent/cmd/autodiscovery.go` | 身份文件写入 0644 → **0600**；读取时就地收紧历史宽权限；抽路径 seam | F12 / P9-2 |
| `web/rpc/jsonrpc/public.sla.go` | 实体列表为空时返回空报告（原先空列表=不过滤，会返回全队含隐藏节点） | F2 / P3-1 |
| `web/rpc/jsonrpc/public.metric.go` | `traffic.*` 的 `sum` 在区间序列缺失时回退读周期累计（AggLast）并标注 quality | F6 / P1-1 |
| `web/public/public.go` | **改对了机制**：`panelOwnedPrefixes` 含 `/install`，由 `serveIndex` 固定用内置默认前端；撤掉显式路由（见 F5 订正） | F5 / P2-1 |
| `web/rpc/jsonrpc/common.go` | `getMe` 的判断写反（成功时把 agent 裸 token 当 uuid 返回）→ `if err == nil` | F16 |
| `database/dbcore/dbcore.go` | 打包改"临时名→写完→校验→rename"；恢复分支加护栏（快照失败即中止、解压失败保留归档）；**顺带修掉**"第 5 步删除会把 `komari-backup-markup` 一起删掉" | F3a / F10 |
| `web/backup/restore.go` | `ValidateArchive` 增加**内容**校验：流式读 SQLite 头 + 页数/大小比对 + `PRAGMA quick_check(1)` | F10 续 / T9 |
| `pkg/jsruntime/console/console.go` | 加 `writeMu` 串行化模块自身的写入（它会从事件循环的后台协程写调用方的 writer） | F15 / P1-16 |
| `pkg/jsruntime/{node,runtime}_test.go` | 4 处裸 `bytes.Buffer` 换成并发安全的 `syncBuffer`，并移除随之无用的 `bytes` import | F15 / P1-16 |
| `utils/notifier/offline.go` | 维护窗口抑制分支补 `isConnExist = false`（否则延迟告警永远发不出） | F7 / P3-2 |
| `utils/notifier/maintenance_defer.go` | 补发成功后收敛 pending 状态（否则"掉了"通知有了、"回来了"永远没有）；带 connectionID 守卫防竞态 | T8 |
| `utils/geoip/mmdb.go` | 持锁段抽成独立函数用 `defer Unlock`（避开 `initialize()` 自死锁）；加超时；临时文件+校验+rename | F11 / P7-3 |
| `frontend/src/types/Bulk.ts` | `BILLING_CYCLES` 从"月"改为"天"（-1/30/92/184/365/730/1095） | F8 / P2-3 |
| `frontend/src/pages/admin/dashboard.tsx` | 管理页内存卡片加口径提示（`hint`） | F9 / P5-1 |
| `frontend/src/i18n/locales/*.json` ×5 | 新增 `dashboard.memoryCaliberTip` | F9 |
| `frontend/script/bulk-fields.test.mjs` | 修正固化错误单位的断言（yearly 12→365）+ 新增钉死 7 个值的测试 | F8 |
| `frontend/script/panel-smoke.spec.py` | **订正目标**：`/install` 断言"不是已安装主题、也不是 admin 包"（排除式，读响应体）；`/database-recovery` 改为断言 **307 → `/`** | F5 续 / T10 / T15 |

## 第二轮：独立复核 + CI 拦下的问题（均已修复）

第一轮 11 个提交推上去后，我让一名独立复核者对抗性审这份 diff，同时 CI 跑真实例 smoke。**两边都抓到了东西**——这部分如实记录，包括我自己的错误。

| # | 问题 | 严重度 | 修法 |
|---|---|---|---|
| 1 | **CI：服务器启动 panic**。`88cf5bc` 把 `/database-recovery` 注册成显式路由，与 `internal/server/runtime.go:117` 已有的注册冲突 → `panic: handlers are already registered for path '/database-recovery'`，发生在首次安装完成后 | P0（阻断合并） | `157bd62`：撤掉 4 条显式路由，回到 `panelOwnedPrefixes` 机制。同时发现**目标也错了**：那两条是**前台** App 的路由，不是 admin 包 |
| 2 | 回退路径绕过 `max_points` 下采样：24h 窗口 1 分钟桶最多 1440 点/实体，响应却仍宣称 500 | P2→P1（在修复里） | `2c16c76`：新增 `lastPublicTrafficBins`，按 bin 保留**最后一个**观测（周期累计不能相加，否则乘出 k 倍） |
| 3 | 回退表按 `storageKey` 归位，同一请求里显式写的 `traffic.interval.*` 会被回退污染 | P3（潜在） | 同上，改为按**请求的 metricKey**。注意：今天从 RPC 到不了（`pkg/metric` 会先以 `-32602 duplicate series` 拒绝），属消除隐患 |
| 4 | **终端握手**仍是 `/api/clients/terminal?token=…` | P2 | `6db6ba7`：URL 只留转义后的 `id`，凭据走 `v2RPCAuthHeaders()` |
| 5 | **文件传输数据面**仍是 `/transfer/:id?token=…`（download/upload 两条路径都没带头）；`file_stream_test.go` 还把 `?token=` 断言成正确行为 | P2 | 同上：删 query token + 两条路径 `setV2Auth`；`transfer_token`（一次性令牌）语义不动 |
| 6 | 工具脚本 `agent/fake_agent.py` 仍拼 `?token=`，跑一次就复刻那 47 个 token 的日志现场 | P4 | 同上：改走 `Authorization` 头 |
| 7 | `getMe` 把 agent 裸 token 当 uuid 返回（判断写反的既有 bug），而本次让 `meta.ClientToken` 在头认证路径上也被填满，等于扩大回显面 | P3 | `f3839e3`：`if err == nil` |
| 8 | **重排队的延迟告警被立刻遗忘**：窗口 A 结束时若节点已身处窗口 B，`sendDeferredOfflineAlert` 重新排队后清扫器又把它删掉 → 通知永久丢失（main 上同样存在） | P2 | `ba5be87`：发送返回三态，只有"已交付/已确认失效"才清理；重排队改用 CAS 且不覆盖更新的条目 |
| 9 | 发送途中的重连会把 pending marker 消费掉 → 只发"掉了"、永远没有"回来了" | P3 | 同上：把判定与清 marker 合并成**发送前**的同一临界区 |
| 10 | 延迟告警测试不可重复（共享内存库 + 固定 token），`-count=2` 直接唯一约束失败 | P4 | 同上：唯一 UUID/token |
| 11 | smoke 断言 `/install`、`/database-recovery` 返回 admin 包——两条都不是 admin 包，且 `/database-recovery` 在真实应用里 307 跳 `/`，该断言不可能成立 | P3（测试） | `3dcb104`：排除式断言 + 307 契约 |
| 12 | `v2auth.go` 注释暗示新旧 agent 可双向混跑，实际**面板必须先升**（新 agent + 旧面板 = 永久 401） | P3（文档） | `6db6ba7`：注释改为单向兼容并指向 `DEPLOY-OC424.md` 的 "panel first" 一节 |
| 13 | 审计里 P2-1 把 `/database-recovery` 也算作被主题接管——**我读错了**：线上它返回 307 → `/`（`runtime.go:117`，`01dccaf` 本就有）。我只读了正文、没看状态行 | 订正 | `913e94e`：报告里写明订正与教训 |

第 2、3、4、5、8、9 项都做了"撤销修复 → 新测试 FAIL → 恢复"的反向验证；第 8、9 项还验证了对**真正的 main 版本**同样 FAIL。第 11 项用 5 个受控 fixture 对照验证，其中 `admin-takeover` 正是"旧断言会放过错误响应"的证明。
| 测试文件（已跟踪 2 个 + 新增 8 个） | 见下 | — |

**新增测试文件（10 个）**：`agent/cmd/autodiscovery_test.go`、`agent/server/v2auth_test.go`、`database/dbcore/restore_guard_test.go`、`frontend/script/panel-smoke-routes.test.mjs`、`utils/geoip/mmdb_update_test.go`、`utils/notifier/maintenance_defer_positive_test.go`、`web/api/Auth_client_token_test.go`、`web/backup/validate_content_test.go`、`web/public/panel_routes_test.go`（另 `web/rpc/jsonrpc/public.sla_test.go`、`public_metric_test.go` 为已存在文件的追加）。

## 验证程度（全部由 Lead 在所有写入者停止后复跑）

```
root  module: go test ./... -count=1 -skip '^TestIpInfo$|^TestIpApi$|^TestGeojs$'  → 53 ok，0 FAIL，exit 0
agent module: go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing'  →  7 ok，0 FAIL，exit 0
两个模块    : go vet ./...                                                          → 无输出
frontend    : npx tsc -b exit 0；npm test 112/112
-race       : 本轮及此前改动过的 12 个包（jsruntime / web/api/... / utils/notifier / utils/geoip /
              database/dbcore / web/rpc/jsonrpc / web/public / web/backup）全部 ok
              —— 其中 pkg/jsruntime 在修 P1-16 之前是 **FAIL**
```

**每个修复都带"修复前会失败"的反向证据**（由对应执行者提供、Lead 复核）：F2（关掉判空 → `an empty entity list leaked the fleet: count=1`）、F5（注释掉路由 → `GET /install does not load the panel's own bundle`）、F6（短路回退 → `legacy node still answers buckets=0`）、F7（去掉置位 → 新用例 FAIL）、F10/T9（短路三段校验 → 6 条拒绝用例全 FAIL）、F11（只成功路径解锁 → 500 用例 FAIL；`Lock` 跨 `initialize()` → 20 秒看门狗 FAIL）、F12（改回 0644 → `实际为 0644` FAIL）。

## 这批改动**不包含**的东西（都在生产侧，已完成但不在补丁里）

| 生产侧改动 | 证据/回滚 |
|---|---|
| nginx 查询串脱敏 | `nginx -t` 通过 → reload → 日志实测 `?[redacted]`；原文件备份 `nekomari.bak-token-redact-<ts>` |
| **9/9 agent token 轮换** | 日志中 47 个历史 token，仍有效的 = **0**（轮换前 9）；抽样 3 个实测 401 |
| ShunDun 单元改 `--token-file` | `ps` 里不再有 auto-discovery key；备份 `nekomari-agent.service.bak-autodiscovery-<ts>` |
| NOSLA 身份文件 0644 → 0600 | 内容为**失效**旧令牌（指纹与当前不符） |
| `auto_discovery_key` 轮换 | 旧 key 注册 → **403**；全队 10 台已确认无消费方 |
| 面板 DB `REINDEX`（修复索引不一致） | 事故记录见报告 P8；修复前库备份 `before-reindex-20261001T071031Z` |

**待你决定、尚未执行**：F4 路由器代理直连规则、F3c 备份出盘目标、以及本批代码的提交/发版方式。
