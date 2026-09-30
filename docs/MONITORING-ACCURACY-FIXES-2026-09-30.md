# 监控准确性修复与验收记录

日期：2026-09-30（Asia/Shanghai）
工作区：`C:\Users\SRTco\Nekomari`
基线提交：`a7a8fe7a295e34a04b604d892ecbc0e450293c5d`（v1.6.3）

## 结论与状态边界

**已发布并部署 OC424 面板 v1.6.5；MAC-WAN 的 v1.6.4 新探针完成内核数据、查询结果与重启边界实测。本批次的限定范围验收通过。**

- v1.6.4 首轮生产 canary 发现窄窗口误降级为整分钟桶；没有跳过失败门槛，而是独立修复、发布并复测 v1.6.5。
- OC424 固定镜像 digest，保留 loopback 监听、数据挂载和现用 SAO 主题；切换前停写完整备份，两库 quick_check 均通过。
- MAC-WAN 运行二进制已核验 SHA256、cap_net_raw 和实际服务进程。v1.6.5 未改变 Agent 源码，因此探针保留 v1.6.4。
- 其余 9 台探针未升级，仍报告 v1.6.2/legacy；10 个注册节点均在线。不能据此宣称全网达到新探针准确性门槛。
- 生产浏览器实点服务器名称和工作台入口、刷新工作台均通过。第 1～5 节保留本地测试范围；生产证据及其限制见第 8 节。
- 原始审查报告及其故障复现样例保留，不覆盖历史结论。

## 1. 流量 counter/delta 契约与补点

### 修复

- 内核累计计数与区间增量分离：新增经过连续性校验的区间流量指标，区间查询采用 SUM；计费周期累计值保持独立语义。
- 同一非空 counter epoch、递增时间、正常网络/uptime 质量、计数单调及不超过三倍采样周期的间隔，才产生流量增量。
- 首次样本、探针/服务器重启、epoch 变化、计数 reset、采集失败及长缺口不跨越计算。不把不可观测区间写成“测得 0 字节”。
- 批量入库串行化，重复和乱序报告不推进状态；事务写入失败不提交新的计数基线。
- 服务端重启后若无可验证的 epoch 基线，保守丢弃首段未知区间，下一对连续有效样本恢复计算。
- maxPoints 只改变展示分箱，不改变同一有效覆盖窗口的可加总值，也不混合不同实体或 tags。
- raw 内存缓存与持久化桶的覆盖不一致时回退到持久化数据，避免重启后仅凭缓存缺失声称历史流量不存在。

### 已通过的本地门槛

| 场景 | 验收结果 |
|---|---|
| counter 100/150/200/250，采样时刻 5/55/65/115 秒 | 1/2/5 分钟及 1 小时聚合总量均为 150 |
| epoch 变化、reset、uptime 回退、采集失败、长缺口 | 不产生虚构增量 |
| 重复、乱序、失败后重试、计费月度 rollover、服务端重启 | 已验证区间总量 40，4 个有效增量 |
| maxPoints=1/2/500，raw/重启/压缩，20 分钟/12 小时/60 小时历史层 | 相同覆盖窗口总量均为 100 |
| 历史请求 65～125 秒，邻桶各有 1000 | maxPoints=1/2/500 均为 30，不因展示分箱扩大到邻桶 |
| 已部署 SAO 的补点函数，两个增量 10+10 | 补点后 20，count=2；空数据保持 null/count=0 |

SAO 验证脚本：`C:\Users\SRTco\Nekomari\docs\audits\2026-09-30\sao-interval-contract.cjs`。

只读取得的已部署 SAO 资源为 `/opt/nekomari/data/theme/SAO/dist/assets/themeSettings-CYV_2lCd.js`，SHA256：

```text
6950e5729b960fd90465fca94e6fd27cb554e7dd34ed3ad7ec23c3f7ef8a3e66
```

此检查执行的是该确定版本的纯函数契约，不是修复后服务端与真实主题的在线端到端验收。

### 覆盖边界

历史 raw 已淘汰时，无法从粗桶恢复任意秒级子窗口。本次返回 backing resolution 和 coverage_start/coverage_end（右端不包含），并显式标记历史桶覆盖。

例如 65～125 秒请求在 1 分钟历史桶上的实际覆盖为 `[60,180)` 秒。此时“总量不变”针对**同一实际覆盖窗口**，不是承诺它等于已不可恢复的 `[65,125]` 原始样本总量。不得把新旧不同覆盖窗口混在一起验收。

## 2. Ping 成功样本、丢包与全窗口统计

### 修复

- 非负成功延迟写入独立 success-latency 指标；失败值不进入 latency 分布。
- 丢包保留全部尝试次数，成功延迟 count 与探测 total 分离。
- 按节点、任务、地址族、协议及角色分组，先合并全窗口 moments/digest，再计算均值、标准差和分位数。
- 管理仪表板 P95 使用合并分布，不再将分桶均值作为分位样本。
- digest 编解码后合并相同均值的质量点，保留大量相同延迟构成的点质量。
- 旧混合 latency 若缺少可重建分布，只提供可验证的均值，分位数为 unknown；缺少 loss 不默认补成 0% 丢包。

### 已通过的本地门槛

| 原始样本/场景 | 验收结果 |
|---|---|
| 100、-1、100 | total=3，成功=2，均值=100，丢包=33.3333% |
| 全部失败 | 100% 丢包；均值、分位数、标准差均为 null |
| 100 个 10 加 1 个 1000 | 全窗口标准差 98.01980198019803；P50/P95=10，1/5/60 分钟展示一致 |
| 常量桶 digest 经编码、解码和不同分组合并 | compression=30/100、桶大小=1/20/60/100，P50/P95/P99=10 |
| 缺少 loss 的旧数据 | loss=null，valid_known=false，不声称全部成功 |

分位数仍是近似算法，接口显式暴露近似属性。固定分布回归的误差检查通过，不是对所有实际分布的统一误差保证。成功样本与失败样本的计数及未知状态不是近似值。

## 3. 采样质量、资源定义与历史边界

### 修复

- 报告携带 sampled_at、服务端 received_at、采样间隔、counter epoch 和分项 quality；接收时间不能伪装为真实采样时间。
- 无采样时间的旧报告使用服务端接收时间并标记 legacy；明显未来/过旧时钟按入口规则归一化，不伪造旧探针的质量信息。
- 网络采集失败清空速率基线；网卡集合和会话变化形成新 epoch；速率采用实际有效时间间隔。
- 最新状态与默认前端区分 null 和测得的 0：未知值显示 `-`，不参加数值排名；缺 GPU/温度不补 0。
- 正常内存口径为 Total-Available；include-cache 为 Total-Free；htoplike 为独立定义，检查所需字段及整数范围。不同口径不能用一次截图的数值差直接判错。
- Swap 和挂载磁盘数据检查完整性与 used<=total；缺字段/采集失败是 unknown。
- TCP、UDP 与总连接数分别表达，总连接数为 TCP+UDP，不将总数伪装成 TCP。
- 内核累计流量质量取 network，不能因为未配置计费周期而被 traffic_cycle=unknown 隐藏。

本地质量回归、入口数据校验、旧报告兼容及 CPU=0/未知内存/未知速率回归通过。MAC-WAN 新二进制的网络、内存、Swap、磁盘及连接数同窗口对账已完成，结果见第 8 节。CPU 数值未做独立内核时间计数对账，不把 quality=ok 当作该项实测通过。

### 历史处理策略

- 不批量覆写旧流量或混合 Ping 聚合；无法无损重建的字段保留 legacy/unknown。
- coarse parent 恢复仅使用仍保留的细粒度子桶，补缺失、已封口父桶；已有父桶不覆盖。
- 恢复回归验证：首次补 1 个父桶、再次补 0 个；后来的细桶变更不重写已经封口的父桶。
- 已删除的原始数据不能重建；启动恢复开销尚未在生产数据库上做性能基准。
- 同名网卡重建且计数未下降的情况目前不能可靠识别；不能宣称所有网卡生命周期均可追踪。
- 老探针没有 epoch/quality，不能自动达到新的完整流量准确性门槛；上线面板不等于全网探针完成升级。

## 4. 用户报告的两处面板故障

### 服务器名称点击崩溃

根因：DrawerContent 的 Radix Theme slot 收到了额外的 JSX 空白 child，不满足单元素要求。去除该空白，保留原 Drawer 内容和行为。

修复文件：`C:\Users\SRTco\Nekomari\frontend\src\components\ui\drawer.tsx`。

验收：桌面及 390x844 移动端实际点击服务器名称、显示详情、按 Escape 关闭均通过；无 slot 异常。组件回归 8 项通过，最终嵌入产物的真实 API/登录浏览器回归也通过。

### 工作台进入 404

根因：侧栏目标是 `/terminal`，服务端将其交给当前公开主题路由，主题不认识该管理页面。

修复：`/terminal` 与 `/terminal/` 由服务端固定交给管理 SPA，不依赖当前公开主题。

修复文件：`C:\Users\SRTco\Nekomari\web\public\public.go`。

验收：管理侧栏点击打开新页、刷新、直接访问、带尾斜杠访问均进入终端工作台，没有主题 404。验证的是工作台页面和路由，不包括对生产节点实际执行命令或文件操作。

### 最终嵌入资源

已重建 admin/standalone 资源并更新嵌入归档。原有主题的 331 个文件逐字节保持不变；最终归档共 847 个文件。

归档：`C:\Users\SRTco\Nekomari\web\public\defaultTheme\dist.tar.zst`
SHA256：

```text
eea2017c98a9f9c785d7fc05710ddc7da67028ea7207d41bf49fc89b9f51c015
```

这是本地资源归档身份，不能替代已发布 Release、ARM64 服务端或生产镜像身份；后者分别记录在第 8 节。

## 5. 测试结果与重放命令

根目录 `C:\Users\SRTco\Nekomari`：

```powershell
go test ./... -count=1 -skip '^TestIpInfo$|^TestIpApi$|^TestGeojs$'
go vet ./...
go test -race ./internal/metricstore ./pkg/metric ./web/rpc/jsonrpc ./web/api/client ./web/agent -count=1
go test ./web/public ./web/rpc/jsonrpc -count=1
node script/embed-theme.mjs
node docs/audits/2026-09-30/sao-interval-contract.cjs
```

Agent 目录 `C:\Users\SRTco\Nekomari\agent`：

```powershell
go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing'
go vet ./...
```

Frontend 目录 `C:\Users\SRTco\Nekomari\frontend`：

```powershell
npm test
npm run lint
npm run build
python script/admin-node-table.browser.spec.py
python script/admin-navigation.spec.py http://127.0.0.1:25884
python script/panel-smoke.spec.py http://127.0.0.1:25884
```

上述检查均通过。外部网络/特权用例按明确 denylist 跳过，不能声称它们通过。最终导航用例需已安装的可丢弃 loopback 实例；脚本会登录并添加测试节点，主动拒绝非 loopback 主机，不能用于生产变更测试。

- frontend 单元测试：108 项；mounted NodeTable：8 项；最终打包产物导航：4 项。
- 最终资源的 panel smoke：13 个发现的路由，无同源资源错误和请求失败；不是全部管理操作或全部主题覆盖。
- race 检查：5 个指定包通过，不等于整个项目完成 race 检查。
- Linux AMD64/ARM64 Agent 本地交叉编译通过；本地测试阶段未安装，后续只在 MAC-WAN 安装了经校验的正式 v1.6.4 AMD64 Release 二进制。
- Linux ARM64 服务端的 CGO-disabled 本地编译尝试失败于已有 SQLite CGO 依赖。本机没有可用 Linux Docker daemon/ARM64 GCC，没有为此安装工具链；后续正式 CI/Release 的原生 ARM64 构建、镜像 smoke 和 OC424 运行验收均通过。不要把这两种构建环境混淆。
- 本地可丢弃测试服务已停止，25884 端口无监听。临时目录删除被执行策略拦截，未绕过：`C:\Users\SRTco\Nekomari\.accuracy-smoke` 与 `C:\Users\SRTco\Nekomari\frontend\.build-tmp` 仍保留，均为本次测试产物，不是生产发布产物。

## 6. 生产验收门槛与历史基线

2026-09-30 的只读生产取证仍显示旧问题：OC424 指定 10:00～11:30 窗口，上行 1 分钟桶少计约 17.62%，5 分钟桶少计约 2.23%；下行对应约 14.90% 和 1.70%。这些是修复前证据，不是修复后的误差结果。

上线顺序与停止条件：

1. 明确本批次发布范围；生成确定提交、版本及 ARM64 服务端/镜像。最终 CI、Release、Docker smoke、匿名拉取和 checksum/架构/二进制身份均通过，不能用当前 Windows smoke binary 替代。
2. OC424 停止写入后完整备份 Compose 和实际 bound data，包含两份 SQLite 数据库、主题和相关配置；核对归档内容与 SHA256，再开始切换。
3. 仅更新固定镜像版本，保持已有 loopback 监听及数据挂载；保存旧镜像和旧 Compose。出现数据库校验失败、异常重启、认证/路由异常时停止，按备份和旧配置回滚。
4. 核验容器版本/hash、架构、健康/重启计数、两库 quick_check、日志、nginx、未登录管理接口 401 及探测服务。
5. 已登录真实 SAO 主题复测两处面板点击，并验证新流量查询、raw/rollup/补点路径、coverage/unknown 和 Ping 统计。不能只检查 HTTP 200。
6. 先对 MAC-WAN 做新探针 canary，保留旧二进制及配置用于回滚。按实际 sampled_at 对齐内核网络计数、内存/Swap/磁盘和连接数据，至少做 5 秒/60 秒有效窗口对账；再检验 reset/重启/缺口不制造流量。不要把计费周期累计值当作内核 counter。
7. 按注册 UUID 关联新报告与运行二进制，明确哪些节点已升级、哪些仍为 legacy；首轮门槛通过后再决定是否扩展到其余节点。

完整已验证备份、确定发布产物或上述验收任一缺失，都不能把本批次标记为“生产修复完成”。本次限定门槛的实际证据见第 8 节；历史原始数据丢失造成的不可重建部分不在上线后追认成已修复。

## 7. 发布与 canary 发现的补丁（2026-09-30）

- v1.6.4 已由提交 `c83ace28cacccd806cd437ddce2c962ae6daa34d` 发布；OC424 已升级固定 digest 镜像，MAC-WAN 单节点新探针已安装，其余 9 个探针未升级。第 5 节的未安装/ARM64 构建限制是本地阶段记录，随后由正式 Release 和镜像构建完成，不代表当前部署状态。
- OC424 停写完整备份位于 `/opt/nekomari/backups/v1.6.3-before-v1.6.4-20260930T111549Z`；MAC-WAN 旧探针及配置位于 `/home/macos/nekomari-agent/backups/agent-before-v1.6.4-20260930T111631Z`。
- v1.6.4 真实浏览器两处入口通过：服务器名称打开详情，无 Radix slot 错误；工作台进入 `/terminal`，直接刷新也不再 404。闭合分钟 Ping 窗口与只读 SQLite 聚合对账通过，点数预算 1/500 不改变全窗口统计。
- 120 秒新报告与内核快照对账揭示未达门槛项：近期窄窗口被错误降级为整分钟桶，上行返回 360211 字节，实际窗口应为 131033 字节。未把该次上线标记为数值验收完成。
- 独立 v1.6.5 服务端补丁按相同桶覆盖检验 raw 完整性，仍只返回请求窗口内样本；补充重启后迟到插入测试，并修复分钟持久化及粗粒度父桶传播。新增回归、本地全套测试、独立版本发布及生产数值复测均通过；没有覆盖 v1.6.4 tag，没有改写既有历史数据。

## 8. 最终发布、部署与生产验收（2026-09-30）

### 发布身份

| 对象 | 已验证身份/结果 |
|---|---|
| 面板版本 | v1.6.5 / `4847bf8` |
| v1.6.5 tag / 合并提交 | `4847bf84ba91b7b933e314431fe6d9ec0824a3d7`，PR #56 |
| PR CI / 最终 main CI | `36711676091` / `36712628179`，均成功 |
| Release / Docker 工作流 | `36713276555` / `36713988725`，均成功 |
| Release 发布时间 | 2026-09-30 20:18:34 Asia/Shanghai（12:18:34 UTC） |
| OC424 镜像 | `ghcr.io/aone2233/nekomari:v1.6.5@sha256:b780f99498765f25f86353258d4a9d04016e1229fbd6d7e929f24210992c3b56` |
| ARM64 服务端 SHA256 | `f91698aa7285a9ed9ae470645e91815f8e1ff6f8c3b985fc519990664d46c261` |
| MAC-WAN 探针 | v1.6.4，AMD64；v1.6.4～v1.6.5 的 Agent 目录 diff 为空 |
| MAC-WAN 探针 SHA256 | `0fef5189327be752409c2904a3ac1c13081e0adc65e038069cb703ff87873d50` |

ARM64 Release 下载文件、GitHub asset digest、SHA256SUMS 和匿名拉取镜像中 `/app/nekomari` 的校验值一致。镜像架构 ARM64、OCI revision 与 tag 提交一致；不把文档后续提交误当作生产二进制提交。

### 备份与运行状态

- v1.6.5 切换前完整停写备份：`/opt/nekomari/backups/v1.6.4-before-v1.6.5-20260930T122413Z`。
- 备份包含 Compose、完整 bound data、`komari.db`、`metrics.db` 及主题；归档 SHA256 为 `7f5c858e7c8e9910d57afcf0af558796c56e1ee2b076cc83d74320ae6bd9517b`，manifest 与 checksum 已核验。
- v1.6.4 前置面板备份和 MAC-WAN 旧二进制/配置备份保留在第 7 节的路径；旧固定 digest 镜像保留用于回滚。
- 2026-09-30 20:33:46 Asia/Shanghai：面板 healthy、RestartCount=0；`127.0.0.1:25774` 和 `/opt/nekomari/data` 挂载未变化；两库只读 `PRAGMA quick_check` 均为 ok。
- `/api/admin/client/list` 未登录返回 401，API key 认证返回 200；10 个注册节点全部在线。启动后日志未出现 panic/fatal/SQLite 锁定或损坏特征。
- 2026-09-30 20:41:25 Asia/Shanghai：`nginx -t` 通过，实际 `panel-probe.service` 返回 Result=success、ExecMainStatus=0。该服务是 oneshot，执行后 inactive 并非失败。
- OC424 与 MAC-WAN 均为 NTPSynchronized=yes。MAC-WAN 用户服务 active，NRestarts=0；二进制 root:root/0755、cap_net_raw=ep 保留。

### MAC-WAN 数值门槛

120 秒并行采集期间，按 sampled_at 对齐 23 份独立报告与 0.2 秒内核快照。有效窗口为 2026-09-30 20:25:59.566～20:27:51.612 Asia/Shanghai。网卡为 `enx00e04c073958` 与 `wlp1s0`；挂载点为 `/`、`/boot`、`/boot/efi`。

| 检查 | 门槛 | 实际结果 |
|---|---|---|
| 内核累计网络计数 | 每份报告落在相邻快照计数之间 | 23/23 通过，0 次越界 |
| 网络速率 | 使用实际时间间隔，最大相对误差 <2% | 最大 0.118205% |
| 采样连续性 | 正间隔且不超过声明周期的 3 倍 | 实际 2.186984～6.000994 秒，通过 |
| 内存 htoplike | 同口径相邻快照，差值 <总内存 1% | 最小相邻快照差值为 0 |
| Swap | total 一致，used 落在相邻快照之间 | 23/23 通过 |
| 磁盘 | total 一致，used 差值 <=1 MiB | total=251380572160 字节，最大 used 差值 0 字节 |
| TCP/UDP | 同窗口数量区间、总数=TCP+UDP | 23/23 通过 |

流量查询采用 `interval_delta_v2` / `exact_samples`。以下各窗均分别以 max_points=1/2/500 对账，API SUM 与连续内核累计计数差值**完全相等**，18 项检查均通过：

| 有效窗口 | 上行字节 | 下行字节 |
|---|---:|---:|
| 5.000386 秒 | 5666 | 9956 |
| 60.000497 秒 | 73641 | 111429 |
| 112.045147 秒 | 136406 | 223025 |

探针实际重启测试捕获两个 epoch（2 与 9 份报告）：新 epoch 首个速率为 null/net_rate=unknown，下一份正常；跨重启 interval.valid=0。逐 epoch 求差后，上行 74893、下行 297030 字节与 API 完全一致，没有计算未知跨 epoch 区间。

### Ping、浏览器与验收边界

- 闭合 `[20:32,20:34)` Asia/Shanghai 窗口有 5 个任务分组（2 个 TCP、3 个 ICMP），每组 total=2、valid=2、loss=0。API 的 count、sum/moments 推导的均值/标准差与只读 SQLite 分钟聚合一致；max_points=1/500 不改变统计。
- 分位数仍明确为 approximate；只验证本窗口有序及上下界。没有在生产人为制造失败 Ping；混合失败、全部失败及 legacy unknown 的证明来自本地回归。
- v1.6.5 的生产管理页实际点击 NOSLA 名称可打开详情；工作台侧栏进入 `/terminal`，刷新仍显示终端工作台，没有主题 404。恢复后捕获的浏览器 console error 为 0；切换期间旧连接的 WebSocket 断开不混入恢复后结果。
- 2026-09-30 20:57 Asia/Shanghai：公开 SAO 的 MAC-WAN 详情实时负载与 5 条 Ping 线路均完成渲染，三个验收页面捕获的新 console error 为 0。此项证明页面可用，不把图表加载或截图当作数值对账。
- 最新注册状态为 10/10 online；仅 MAC-WAN 的新探针逐项核验，其余 9 台仍为 v1.6.2/legacy，不宣称逐台二进制验证或完成新契约验收。
- CPU 数值独立对账、全网探针升级、生产历史压缩各层的数值对账以及启动恢复性能基准不属于本次已通过结论。重启后迟到插入/父桶传播是持久化回归测试通过，不冒充生产面板重启故障注入。
- 原始证据目录：`C:\Users\SRTco\Nekomari\.accuracy-smoke\release-v1.6.5`，包含 `accuracy-result.json`、`boundary-result.json`、`ping-result.json`、`runtime-result.json`、`fleet-result.json`、`host-check.txt`、`mac-identity.txt`、`browser-result.json` 和浏览器截图。
- 复测脚本及安全使用说明：`C:\Users\SRTco\Nekomari\docs\audits\2026-09-30\README.md`。原始采样只在短期 raw 保留窗口内可直接重放；过期后需重新采集，不可把空结果当成数值通过。
