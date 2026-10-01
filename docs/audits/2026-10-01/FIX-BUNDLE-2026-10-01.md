# 修复包清单 — 2026-10-01

配套 [排查报告](./BUG-AUDIT-2026-10-01.md) 与 [修复方案](./FIX-PLAN-2026-10-01.md)。

**这批改动目前只存在于工作区，一行未提交。** 本文记录它包含什么、验证到什么程度、以及怎么恢复。

## 快速恢复

```bash
# 已跟踪文件的改动（29 个文件，+1162/−131）
git apply docs/audits/2026-10-01/fixes-tracked.patch

# 新增文件（11 个）已经在工作区里，git add 即可
git add agent/cmd/autodiscovery_test.go agent/server/v2auth.go agent/server/v2auth_test.go \
        database/dbcore/restore_guard_test.go frontend/script/panel-smoke-routes.test.mjs \
        pkg/jsruntime/syncbuffer_test.go \
        utils/geoip/mmdb_update_test.go utils/notifier/maintenance_defer_positive_test.go \
        web/api/Auth_client_token_test.go web/backup/validate_content_test.go \
        web/public/panel_routes_test.go
```

补丁做过自洽性校验：`git apply --reverse --check docs/audits/2026-10-01/fixes-tracked.patch` → **exit 0**，即它与当前工作区逐字节一致。

## 改了什么（26 个已跟踪文件）

| 文件 | 改动 | 对应条目 |
|---|---|---|
| `web/api/Auth.go` | `extractClientToken` 增加 `Authorization: Bearer` 头分支（查询参数仍优先，向后兼容） | F1b / P1-2 |
| `agent/server/v2auth.go`（新） | 共享助手 `v2RPCEndpoint` / `setV2Auth` / `v2RPCAuthHeaders` | F1b / P1-2 |
| `agent/server/{basicInfo,task,files,websocket}.go` | 5 处把 token 从 URL 查询串移到请求头；WebSocket 用握手头 | F1b / P1-2 |
| `agent/cmd/autodiscovery.go` | 身份文件写入 0644 → **0600**；读取时就地收紧历史宽权限；抽路径 seam | F12 / P9-2 |
| `web/rpc/jsonrpc/public.sla.go` | 实体列表为空时返回空报告（原先空列表=不过滤，会返回全队含隐藏节点） | F2 / P3-1 |
| `web/rpc/jsonrpc/public.metric.go` | `traffic.*` 的 `sum` 在区间序列缺失时回退读周期累计（AggLast）并标注 quality | F6 / P1-1 |
| `web/public/public.go` | `panelOwnedPrefixes` + `isPanelOwned`，`/install`、`/database-recovery` 归面板自有 | F5 / P2-1 |
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
| `frontend/script/panel-smoke.spec.py` | 默认并入 `/install`、`/database-recovery` + 面板文档归属断言（读**响应体**而非 hydration 后的 DOM） | F5 续 / T10 |
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
