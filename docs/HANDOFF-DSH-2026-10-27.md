# Nekomari 开发交接给 dsh

交接日期：2026-10-27。交付与验收时间统一按 `Asia/Shanghai` 解释；开发机使用
`Europe/Berlin`，不要把本地日期或日志时间直接当作线上时间。

这是一份**有日期的交接快照，不是第二份实时待办**。后续任务状态只维护在
[`OPEN-WORK.md`](./OPEN-WORK.md)，长期版本目标和验收标准见
[`LONG-TERM-PLAN.md`](./LONG-TERM-PLAN.md)。本文写作时只核对本地仓库及留存证据，
没有重新连接服务器验收，也没有修改服务、安装依赖或发布版本。

## 1. 先读结论

- 最近完成并上线的是 **v1.7.4 / b0ea917**，不是待上线候选；不要重新打包、移动标签或替换 Release 资产。
- 站点是 <https://nekomari.orderly2233.org>，生产面板在 **OC424**；OVH 的 SSH 别名是 **KSLE**，用于隔离测试和密文备份；**MAC-WAN** 用于 Hermes、在线密钥保管及隔离恢复。
- 最后一次留存验收为 **2026-10-27 17:45:29**：面板 healthy/restart0，**14 个注册节点、14 个新鲜 CPU 上报节点**。这是历史证据，不保证接手时仍未变化。
- Pi 后加的节点经过用户明确授权。保留现有节点、UUID、Agent token、API key scope/有效期、备份健康状态；不要用早期 13 节点备份覆盖当前数据。
- 建议下一轮先处理**恢复副本的 metric-store 启动超时**，暂称 v1.7.5 候选；版本号须先核对远端实际进度，不能仅凭本文预留或发布。
- 继续小批次、可回滚的开发与上线；**200 节点优化/压测继续搁置**，不要顺手扩成大规模性能或 Agent 改造。

## 2. 用户已经确定的工作方式

| 事项 | 已确定的边界 |
|---|---|
| 发布 | 用户此前允许合适时机推送和更新，默认分支 + PR；仍须完成精确源码 CI、资产验证、备份、恢复及上线验收，不能理解为跳过门禁 |
| 测试 | 优先改动相关的定向验证，合适的隔离工况放 OVH；避免重复全量本地 Go/前端/容量测试，但不得删减必需 CI 门禁 |
| 真实工况 | OC424 同时承担线上真实验收；它仍是生产环境，不得随意注入故障、测试通知、伪造节点或修改业务数据 |
| 前端导航 | 五个工具入口已放在管理面板内部，保持同一导航与日夜主题；不要退回另开页面造成主题不一致 |
| token | 用户接受现有限制，并明确表示现在不需要轮换；不要替用户轮换 Agent token/API key |
| Hermes | 用于节点元数据管理；不授予 token 读取、远程执行、安装或升级权限，不擅自重新配置凭据 |
| 备份 | OVH 存密文，MAC-WAN 保管在线 identity 并执行恢复；用户报告已下载离线副本，但独立介质恢复尚未验证 |
| 清理 | 先看磁盘和占用，只清理确认属于本批次的可再生临时产物；不清共享缓存，不删历史明文/手动备份、回滚镜像或 identity |

用户无需为普通的定向修复重新做一次全面决策。账户策略、删除保留副本、独立介质演练、
扩大权限、生产轮换或舰队升级等事项，仍按 `OPEN-WORK.md` 对应决策单独确认。

## 3. 仓库与版本基线

仓库：<https://github.com/Aone2233/Nekomari>。

当前交接工作目录：

```text
/root/.codex/worktrees/compiler-patch-1273/Nekomari
```

本次文档修改前工作区干净，分支为 `codex/v1.7.4-delivery`，比本地记录的同名远端分支
领先 1 个合并提交；HEAD 与本地记录的 `origin/main` 相同。本文没有执行 fetch，
dsh 接手时须重新核对远端，不能把这个状态当作永久事实。

| 身份 | 精确值 / 意义 |
|---|---|
| 交接前 HEAD / 本地 `origin/main` | `52c24ce40510572881f981b9b13fa76acc490a49`，合并文档交付 PR99 |
| v1.7.4 不可变源码标签 | `b0ea91756e700e871491a4c4388757f65ead263e`，实际运行源码；不要用文档合并 HEAD 替代它 |
| 源码 PR | [PR98](https://github.com/Aone2233/Nekomari/pull/98)，已合并；运行修复进入主线 |
| 交付记录 PR | [PR99](https://github.com/Aone2233/Nekomari/pull/99)，已合并；不是另一个运行时版本 |
| 已发布版本 | [v1.7.4 Release](https://github.com/Aone2233/Nekomari/releases/tag/v1.7.4) |

**不要切到 `/root/projects/Nekomari` 开发、清理或部署。** 那个旧 checkout 有大量既有
修改及未跟踪文件，不属于本次交接成果；不可 reset、checkout 覆盖、自动合并或清除。
若实际状态与预期冲突，先停下确认，不能把其他操作者的修改当垃圾处理。

接手先做只读核对，之后才 fetch 和从确认后的主线新建开发分支；不要直接 pull 覆盖当前状态：

```sh
cd /root/.codex/worktrees/compiler-patch-1273/Nekomari
git status --short --branch
git log -5 --oneline
git rev-parse HEAD origin/main 'v1.7.4^{commit}'
curl --fail --silent --show-error --connect-timeout 10 --max-time 30 \
  -A 'Nekomari-bounded-release-check/1.7.4' \
  https://nekomari.orderly2233.org/api/version
```

最后留存的响应是 `{"hash":"b0ea917","version":"v1.7.4"}`。默认 Python urllib
User-Agent 曾被 Cloudflare 返回 403，curl 或上述显式 UA 正常；先区分边缘规则与源站故障，
不要为探针方便关闭 WAF 或放松安全规则。

## 4. 已交付内容与证据范围

| 版本 | 已交付内容 | 不应推导出的结论 |
|---|---|---|
| v1.7.0 | scoped API key、输入边界、报告/队列不变量、仅会话可用的 Agent-token 轮换、异机加密备份 | 不代表用户要求立即轮换，或全部调用方/账户策略已验收 |
| v1.7.1 | 流量预测计数器重置、整数溢出与远期日期边界 | 不代表数据不足时应伪造预测 |
| v1.7.2 | GeoIP 配置类型安全、缓存及热重载生命周期 | 不代表本轮可随意更改生产 GeoIP 设置 |
| v1.7.3 | 备份新鲜度/健康心跳告警，重启去重与重试 | 不代表首次夜间定时备份、真实事故通知或独立恢复已验收 |
| v1.7.4 | SLA 同一节点/任务多代标签序列按时间排序去重，空桶不覆盖真实测量 | 不改历史数据、覆盖率定义、API/schema、Agent、前端或依赖 |

v1.7.4 主要代码位于 `internal/sla/query.go`、`internal/sla/availability.go`、
`internal/sla/generations_test.go`；实际发布二进制的隔离验收为
`deploy/sla-generations-smoke.py`。详见 [`SLA-GENERATIONS.md`](./SLA-GENERATIONS.md)。

必须保留以下统计口径：去重取最差的**有测量值**，Count=0 占位不覆盖真实值，
延迟 `-1` 失败哨兵不可丢失；Count 表示存在测量，不给 SLA 加权。
延迟分位数来自已存桶均值，**不是原始探测点 p95/p99**。

最近一轮验收记录：

| 验证 | 留存结果 |
|---|---|
| 精确源码 CI | [40476853150](https://github.com/Aone2233/Nekomari/actions/runs/40476853150)，7/7 |
| Release | [40478743592](https://github.com/Aone2233/Nekomari/actions/runs/40478743592)，attempt 2，10/10 |
| Docker | [40479934401](https://github.com/Aone2233/Nekomari/actions/runs/40479934401)，2/2 |
| 交付文档 CI | [40481065165](https://github.com/Aone2233/Nekomari/actions/runs/40481065165)，7/7；不包含本交接文档 |
| 实际发布物 | 校验和、源码/标签/workflow attestation、双架构镜像来源验证通过；10 个 Release 资产身份、大小、摘要和时间戳在交付后未变 |
| 隔离真实二进制 | OVH 上安装实际发布 Panel，6/6 组通过，不是只跑源代码单元测试 |
| OC424 | 升级检查 9/9，公共检查 23/23，scoped forecast 14 行；SLA 读取覆盖 14 节点/58 任务 |
| SLA 独立对照 | 两节点 11 任务与保留序列的桶均值对照通过；这 11 个任务在该窗口均无多代，不是线上重叠场景的证据 |
| 恢复 | 新鲜 pre/post 密文在 MAC-WAN 用 digest-pinned 旧/新镜像实际恢复成功；启动超时的 scratch-only 绕行及失败记录保留 |

源数据审计发现 44 个多代 metric/entity/task 组（loss/latency 各 22），
**组数不等于时间戳发生重叠**。重叠专项由 synthetic fixture 证明，不能扩大线上结论。
首次发布因 GitHub OIDC 取 token 失败而中断，随后仅重试失败发布/验证步骤；
没有通过重建成功资产或绕过 provenance 来掩盖失败。

## 5. 建议下一轮：恢复启动路径加固

这是建议的下一项有界工作，不是已经完成的修复，也不是授权直接在生产运行维护 SQL。

### 已知现象

v1.7.4 发布前的新鲜加密归档 age 认证、manifest/文件摘要和两个 DB 检查均通过，
但 MAC-WAN 隔离恢复使用 pinned v1.7.3 时，metric-store 初始化触发 context deadline，
最终未在恢复启动期限内就绪。仅在**认证后的一次性 `metrics.db` 恢复副本**执行
`ANALYZE` 后，同一归档启动成功，645172 条 rollup 与业务 schema 保持不变。
pre 旧镜像约 8.73 秒、pre 新镜像约 9.60 秒、post 新镜像约 8.31 秒通过；
post 归档保留 658949 条 rollup。

这些数字是隔离演练耗时，不是生产 RTO。查询计划/统计信息敏感是调查线索，
不是已经锁定的唯一根因；不能把一次 `ANALYZE` 或单纯放大超时称为源码修复。
**没有对生产库运行 ANALYZE，没有改归档或已安装的加密备份工具。**

### 优先阅读的入口

| 文件 | 调查点 |
|---|---|
| `internal/metricstore/store.go` | `InitializeStore` 的 30 秒初始化 context |
| `internal/server/metric_store.go` | 连接重试 3 次、间隔 5 秒；区分连接与初始化代价 |
| `pkg/metric/store.go` | Open -> migrate -> recovery，失败时连接池清理 |
| `pkg/metric/coarse_rollup.go` | `recoverPendingCoarseRollups` 对每个 metric/相邻 tier 的 correlated `NOT EXISTS`，扫描并物化缺父桶的子数据 |
| `pkg/metric/migrations.go` | SQLite `PRAGMA optimize` 与索引/统计信息的关系 |
| `pkg/metric/hot_rollup_test.go` | `TestRestartRecoversUnsealedCoarseParents` 等已存在契约 |
| `pkg/metric/coarse_rollup_memory_test.go`、`pkg/metric/rollup_read_index_test.go` | 内存边界与读取索引回归，避免优化恢复却退化其他路径 |

### 建议完成标准

1. 先建立可复现的 stale-statistics/恢复启动样本，记录查询计划、耗时、取消行为和输入规模；优先合成脱敏数据，真实副本只能在受控隔离环境内使用。
2. 修复后在原始条件下无需人工预处理即可启动；不得丢弃 rollup、跳过必要恢复、重写已封存历史或更改业务 schema/认证来换取通过。
3. 缺父桶恢复保持幂等，节点/任务/tags/labels 身份不串；失败/取消释放 rows、连接与临时资源，避免将全量历史无界加载到内存。
4. 若改通用 SQL，保持 SQLite/MySQL/PostgreSQL 语义兼容并补相应验证；不要只证明当前 SQLite fixture 可用。
5. 定向回归证明修复前失败、修复后通过，相关 race/vet 与必需 CI 通过；先验证实际候选发布二进制，再走 OC424 上线与回滚门禁。
6. 独立记录调查、失败及重试，更新 `OPEN-WORK.md` 和 runbook；不将已有 scratch-only workaround 记成新的根因修复。

下一项可另开 **SLA span-vs-requested-window coverage 契约澄清**；不要与恢复改造混发。
`Presence.Coverage` 目前按观察跨度计算、受请求窗口上限约束，不是整个请求窗口覆盖率，
已有测试固定现状。任何语义变更要先明确兼容性与用户解释。

## 6. 主机、权限与不能动的服务

统一使用已有 SSH 别名，并设置 `BatchMode=yes`、`StrictHostKeyChecking=yes` 和有界超时。
host key 不匹配或缺失时停下核对，不使用 `StrictHostKeyChecking=no`、`accept-new` 或临时
`ssh-keyscan` 代替信任确认。不打印 SSH 配置、完整 `docker inspect`、环境或服务 ExecStart，
它们可能包含凭据。

| 主机 | 角色 / 路径 | 现有服务与权限 |
|---|---|---|
| `OC424` | Linux arm64；`/opt/nekomari/docker-compose.yml`；容器/服务 `nekomari`；源站 loopback `25774`；`data/komari.db` + `data/metrics.db` | `ubuntu`，sudo NOPASSWD；实际上报 Agent 是 **`komari-agent-oc424-original-node.service`**，不是通用名称 |
| `KSLE`（OVH） | Linux amd64；隔离测试及 `/srv/backups/oc424-encrypted` | `ubuntu`，sudo NOPASSWD；`nekomari-agent.service`，不随 Panel-only 发布重启 |
| `MAC-WAN` | Linux amd64，用户 `macos`；在线保管和恢复，不是 macOS 主机 | 可访问 Docker，但无 NOPASSWD sudo；`nekomari-agent.service` 与 `hermes-gateway.service` 是用户服务 |

MAC-WAN 现有私有位置（**只记录位置与权限，不读取/输出其内容**）：

- recovery 根目录：`/home/macos/.local/share/nekomari-recovery`，`0700`。
- identity：`/home/macos/.local/share/nekomari-recovery/identities/oc424-primary.agekey`，`0600`；只在 MAC-WAN 使用，不复制到开发机、OC424 或 OVH，也不挂入恢复 Panel。
- 密文副本：`/home/macos/.local/share/nekomari-recovery/incoming`。
- Hermes CLI：`/home/macos/.local/share/nekomari-hermes/hermes_nodes.py`。
- Hermes credential：`/home/macos/.config/nekomari-hermes/credential.json`，文件 `0600`、目录 `0700`；禁止 cat、回显、复制或从历史/数据库中恢复。

当前 `hermes-node-management` key 仅 `nodes:read` + `nodes:write`，用户选择 365 天，
到期 **2027-10-27 13:38:02 Asia/Shanghai**。实际一次性节点的创建/编辑/读取/删除已验收，
不要重新 configure 或重复创建 key。注意 metadata write 仍可跨节点删除，
不是只读权限；禁止擅自操作真实节点。迁移来的旧 30 天只读 key 尚未退役，
完整消费者清单仍需核对，不以几个 Hermes 引用修改代替全量调查。

MAC-WAN 同 UID 的 Hermes、root 和 Docker 管理员仍属信任边界；在线 identity 没有口令加密，
`0600/0700` 不防这些主体，不可称为离线保险库。已记录的 MAC-WAN ED25519 指纹为
`SHA256:uS7MlNlA1Rowjf7XFkr/8HwT7EaX1t+AhNO8Uoc2scE`，它不能替代其他主机的指纹核验。
历史上代理路由发生过 SSH banner 超时；重试前先核对目的端已有文件/partial 和摘要，
不要重复触发备份写入或复制。

## 7. 备份、健康监控与回滚

权威操作步骤见 [`DEPLOY-OC424.md`](./DEPLOY-OC424.md)、
[`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md)、
[`ENCRYPTED-BACKUPS.md`](./ENCRYPTED-BACKUPS.md) 与
[`BACKUP-HEALTH.md`](./BACKUP-HEALTH.md)。不要直接执行归档里的 compose，
也不要让恢复副本指向生产 metrics 路径。恢复保持 network-none、无 host port、
禁用副本里的通知/GeoIP/OAuth，仅挂一次性数据，结束清理明文和恢复容器。

### 现有备份与监控

- OVH：`nekomari-oc424-encrypted-backup.service/.timer`，每日 **03:30 UTC / 11:30 上海**，顶层保留 14 对密文及校验文件；已观察手动服务成功，最后交接证据尚未观察首次夜间计划触发成功。
- OVH：`nekomari-oc424-backup-health.service/.timer`，每 15 分钟；初始及后续周期检查已观察成功。手动发布备份不等于 nightly 验收。
- OC424：`NEKOMARI_BACKUP_HEALTH_FILE=/app/data/backup-health/ovh.json`；宿主 `/opt/nekomari/data/backup-health/{ovh.json,alert-state.json}`，root 目录 `0700`/文件 `0600`，必须跨 Panel 重启保留。
- 阈值：备份 26 小时、健康心跳 45 分钟、失败通知 15 分钟后重试。最近状态为 `observed=ok`、`notified` 为空；不要通过删除状态强行重置告警。
- 文件时间/密文摘要/健康心跳不等于 age 认证或实际恢复，更不是独立于 Panel 的完整外部监控、生产 RPO/RTO/SLO 证明。

v1.7.4 的独立常规发布副本在 OVH
`/srv/backups/oc424-encrypted/manual-v1.7.4-20261027`，**不在顶层 14 对自动裁剪范围内**；
MAC-WAN 保留同摘要密文。不能为了节省空间改成硬链接或删除旧副本。

| 归档 | 字节数 | SHA-256 |
|---|---|---|
| pre：`oc424-20261027T085608950432Z-8a4761db5628.tar.gz.age` | 183512271 | `94b92ef8c060e23d07e8bc719a7b309ce0b4042538748a28ed52c15d8600e7f6` |
| post：`oc424-20261027T092415525807Z-c3696c018496.tar.gz.age` | 183649817 | `daf3e4c3d211c86eed722387b8d1266f524763df6248571f5ab2240ebdf2d311` |

### 当前镜像与回滚锚点

```text
OC424 当前 arm64：
ghcr.io/aone2233/nekomari:v1.7.4@sha256:e089dabfcba5c3b89365b53d3288f6925d05069e6195adf9832d3c8879e1355e

MAC-WAN 恢复用 amd64：
ghcr.io/aone2233/nekomari:v1.7.4@sha256:b2c6cf4cc986682ace82cf6c0abdc42ab24b3b9bf3830882bc042c64c10453c6

OC424 上一版 arm64，v1.7.3 / 0fe0b37：
ghcr.io/aone2233/nekomari:v1.7.3@sha256:206981e01fc40d7cce7bf6a464973783a0128b58f7111114d9cd33a8c1830a14
```

当前本地回滚包：
`/opt/nekomari/rollbacks/v1.7.4-20261027T092318Z-84a48b94`。
它保留逐 DB 的 WAL-safe 快照、前后 compose、镜像引用和摘要；**不是跨两库的原子事务快照**。

v1.7.4 -> v1.7.3 没有 schema/auth 降级：先验证私有恢复摘要，再把
`docker-compose.before.yml` 恢复为实际 compose，检查 `docker compose config --quiet`，
然后在 `/opt/nekomari` 执行 `docker compose up -d --no-deps nekomari`。
保留**当前数据库、14 节点和备份健康 env/report/state**；不要仅凭本文摘录盲跑。
线上镜像回滚是已准备、未实际执行，和已通过的隔离恢复不是同一证据。

这个规则**不能套到 v1.7 -> v1.6 的认证迁移降级**：后者需要冻结写入并恢复配套旧 DB
与旧二进制，且会丢失之后的数据及权限变化。下一版若改变 schema，也必须重新评审回滚。

当前单容器替换曾出现短暂公共 HTTP 502；后续检查通过不代表零停机，
也没有据此测得中断时长或可用性 SLO。

## 8. 开发环境与最小验证策略

| 项目 | 交接时状态 |
|---|---|
| Go | 本地默认 1.26.9；**CI/Release 固定 1.27.3**，开发定向验证显式 `GOTOOLCHAIN=go1.27.3`；`go.mod` 的 1.26.0 是最低版本，不是发布编译器 |
| 模块 | 根模块是 Panel；`agent/` 是独立模块；先核对改动范围，不要无条件对 Agent 做依赖升级或发布 |
| 前端 | Node 22.23.4 / npm 10.9.9；`frontend/node_modules` 已存在，不能据此保证依赖完整；需要前端时再按 lockfile 安装 |
| CGO | Panel 使用 CGO SQLite，需要 C 编译工具链，不能把 `CGO_ENABLED=0` 当通用构建方式 |
| Python | Python 3；相关备份脚本使用标准库，部分工具要求 3.10+；按各脚本/runbook 要求检查 |
| GitHub CLI | 系统 `gh` 2.46.0 不具备所需现代 attestation 验证能力；上轮校验过的临时 2.103.2 已清理，需要时重新验证下载，不可略过来源门禁 |

仅在新环境或依赖缺失时执行，不为“准备开发”顺便改 lockfile：

```sh
GOTOOLCHAIN=go1.27.3 go mod download
# 仅 Agent 改动需要：
(cd agent && GOTOOLCHAIN=go1.27.3 go mod download)
# 仅前端改动或完整构建需要：
(cd frontend && npm ci --no-audit --no-fund)
```

当前交接不需要重新跑未变更的测试。开始下一轮后按改动选取测试，例如恢复路径可先跑
`pkg/metric` 和 `internal/metricstore` 的相关用例/race/vet，具体范围以实际 diff 为准；
必要集成测试与精确候选 CI 不省略。不要直接把历史通过数抄成新提交的通过数。
详细测试和打包入口见 [`TESTING.md`](./TESTING.md)、[`RELEASING.md`](./RELEASING.md)。

### 下一版发布顺序

1. 核对代码/线上基线及待办；新分支完成一个有界修复，附复现、回归和兼容/回滚说明，走 PR。
2. 等待**精确待打标签 commit** 的必需 CI 成功，再创建未使用的新版本标签；标签/成功发布资产不可变，失败不能通过改写旧版本掩盖。
3. 验证 Release 校验和、SBOM/provenance、源码/workflow 身份、双架构镜像 revision/digest；不能只看 `latest` 或 Docker workflow 事件 SHA。
4. 在 OVH 做一次与改动相符的实际发布二进制隔离验收，不重复所有未变更套件；新鲜 pre 密文及 MAC-WAN pinned 旧/新候选恢复通过后才切生产。
5. OC424 保留本轮 WAL-safe DB 快照、前后 compose/摘要和旧镜像；先验证配置再有界切换，只动 Panel，不动 Agent/Hermes/token/权限。
6. 检查版本/摘要、health/restart、节点/上报连续性、schema/auth/设置及相关功能，保留失败和重试记录；post 密文及新镜像恢复完成后更新交付文档。

出现版本/摘要不符、节点意外变化、权限扩张、DB/恢复失败、无回滚锚点或他人并发修改时，
先停下调查，不能放松断言。新版本如果有不同兼容性要求，必须调整门禁而非机械复用旧脚本。

## 9. 仍未取得的验收与后续事项

以下仅是交接时的未闭环事实；实时状态以 `OPEN-WORK.md` 为准：

- 用户登录后的 history / audit / API-key UI 验收尚无结果。公共 shell、401、synthetic 和 scoped API 读取都不能替代 owner-session UI 验收。
- 已观察 15 分钟健康周期成功，但尚未观察首次夜间计划备份；接手后按实际时刻读取 timer/journal/密文对证据，不重复触发手动备份冒充 nightly。
- 真实事故下的通知投递未验收；不要人为破坏生产备份或发送假事故来关闭待办。
- 用户报告离线副本已下载，但尚未从独立介质完成恢复；MAC-WAN 在线恢复不能替代它。旧明文/手动/本地回滚副本的删除与保留策略仍需明确。
- 完整 API 调用方清单、账户认证策略及其他 owner 决策仍见实时待办；没有宣告 v1.7 全部退出条件、v1.8 或整条路线图完成。
- 200 节点实验及原有 26 份失败报告保留并搁置；不把小范围真实运行或隔离演练当作容量/SLO 证明。

## 10. 留存证据、清理与资料索引

本机 `.runtest/release-v1.7.4-current` 是**保存绝对路径的文本指针**，不是目录或软链接，
指向：

```text
/root/.codex/worktrees/compiler-patch-1273/Nekomari/.runtest/release-v1.7.4-20261027.d27iaQ
```

其中保留 `final-release-acceptance.json`、`final-continuity-verified.json`、
`oc424-finalized-acceptance.json`、CI/资产/attestation 元数据、恢复失败和重试记录，
以及 `macwan-diagnose-scratch-startup.py`、`macwan-scratch-startup-diagnostic.jsonl`、
`macwan-recovery-check.py`。`SHA256SUMS.evidence` 本身的 SHA-256 为
`dcb2fd1b667fb242aa114e2d713fe321f71e4cacff91cf9b4b94858325f232b4`。

这些是本地留存资料，不随 Git clone 分发；含生产元数据的证据不得整目录公开或提交。
其中操作辅助脚本带有 v1.7.4、节点数及进程断言等硬编码，**不是通用可重跑部署工具**。
接手先阅读、审核并适配，不能批量重放来“确认交接”。

上轮已清理约 183.5 MiB 自有下载/临时工具，保留约 0.9 MiB 证据与所有备份/回滚镜像。
当时磁盘使用约为开发机 63%、OC424 43%、OVH 8%、MAC-WAN 25%，不是本次新测量。
需要清理时先重新测量归属与空间；`deploy/prune-agent-backups.sh` 默认 dry-run，
`--apply` 会删除旧 Agent 副本，只能按明确保留政策另行评审，不能作为接手步骤直接执行。

| 文档 | 用途 |
|---|---|
| [`OPEN-WORK.md`](./OPEN-WORK.md) | 唯一实时待办、用户决策和未满足的验收 |
| [`LONG-TERM-PLAN.md`](./LONG-TERM-PLAN.md) | 路线图、SLO 目标和版本退出标准；目标不是已达到的指标 |
| [`DEPLOY-OC424.md`](./DEPLOY-OC424.md) | 当前部署、逐版本证据、镜像/备份/回滚；旧章节按日期阅读 |
| [`SLA-GENERATIONS.md`](./SLA-GENERATIONS.md) | v1.7.4 修复范围及统计口径 |
| [`MAC-WAN-RECOVERY.md`](./MAC-WAN-RECOVERY.md)、[`ENCRYPTED-BACKUPS.md`](./ENCRYPTED-BACKUPS.md) | 保管、隔离恢复、受限备份链路 |
| [`BACKUP-HEALTH.md`](./BACKUP-HEALTH.md) | 健康心跳、告警状态、安装和撤销边界 |
| [`HERMES-NODE-CLIENT.md`](./HERMES-NODE-CLIENT.md)、[`API-KEYS.md`](./API-KEYS.md) | Hermes 私有交接与 scope；历史小节不代表需要重做迁移 |
| [`CLIENT-TOKEN-ROTATION.md`](./CLIENT-TOKEN-ROTATION.md) | 未来按节点确认后的轮换/恢复约束；当前不轮换 |
| [`RELEASING.md`](./RELEASING.md)、[`TESTING.md`](./TESTING.md) | 构建、精确候选 CI、发布资产及测试边界 |
| [`AGENT-FOOTPRINT.md`](./AGENT-FOOTPRINT.md)、[`deploy/README.md`](../deploy/README.md) | Agent 足迹、清理与操作脚本说明 |

**给 dsh 的第一条行动建议：**先核对远端、14 节点基线和 nightly 的实际证据，再以隔离
恢复启动超时为单独开发批次；不要重做已完成的 v1.7 迁移，不要自动启动 200 节点工作。

## Afterword (2026-10-27, same day, dsh)

Two bounded items above were picked up and merged after this handoff was
written. The snapshot itself is left as written.

- **Recovery startup hardening** (PR #100, merged `30d4714`, exact-commit CI 7/7)
  — §5's recommended next batch. The root cause was the recovery statement
  constraining the tier through a join on the resolutions table, so stale
  statistics drove the scan from the resolution index and re-read every series'
  rows of that tier once per metric (27x), with a digest blob page behind every
  row. The fix binds the resolution as an id and forces the series-leading index
  on SQLite. On a production-shaped fixture with stale statistics the scan went
  from 18.2s to 0.12s and no longer depends on statistics at all. Evidence and
  reproduction: [`METRIC-RECOVERY-STARTUP.md`](./METRIC-RECOVERY-STARTUP.md).
- **SLA coverage contract** (PR #101) — §5's follow-up item, clarified without
  changing behaviour: `Presence.Coverage` is a span fraction, and a series that
  reports only the last ten minutes of an hour reads 1. See
  [`SLA-COVERAGE.md`](./SLA-COVERAGE.md).

Neither is a release. No tag, no release asset, no OC424 deployment and no
MAC-WAN recovery was performed, and every §9 gate is unchanged: the owner UI
acceptance is still pending, the first nightly encrypted backup was still
unfired at the time of writing (`LastTriggerUSec` empty on
`nekomari-oc424-encrypted-backup.timer`, next elapse 2026-10-28 11:30 Asia/Shanghai),
independent-media restore is still unverified, and the v1.7.5 candidate cannot
be released until the pinned old/new-image recovery gate can run again.
