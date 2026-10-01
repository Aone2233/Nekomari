# Nekomari 监控数据准确性审查

审查日期：2026-09-30。本文全部现场时间采用 Asia/Shanghai（UTC+08:00）。

## 结论

基础主机资源可以作为运维趋势参考；**当前流量消耗统计与 Ping 历史汇总不能作为精确对账、配额核算或网络质量比较的依据**。本次确认 4 项问题：连续流量的桶边界漏计、短窗口/主题补点对累计流量重复求和、失败 Ping 污染延迟统计、跨桶百分位和标准差算法错误。

这些结论不意味着所有节点、所有指标都错误。流量漏计和 Ping 统计失真已有线上存储样本；SAO 补点的放大问题已用线上实际代码复现，但没有观察已登录用户页面触发该条件。没有将主机内存口径差异、时间不对齐的瞬时差异或版本不一致直接判为缺陷。

本次只读审查。没有修改生产代码、配置、数据库或服务，没有部署，也没有使用或保存用户提供的 sudo 密码。唯一新增产物为本报告和复现文件。

## 范围与部署事实

| 项目 | 已核实情况 | 证据边界 |
| --- | --- | --- |
| 本地代码 | main / v1.6.3，提交 a7a8fe7a295e34a04b604d892ecbc0e450293c5d | 本次源码与复现基线 |
| OC424 面板 | 容器 ghcr.io/aone2233/nekomari:v1.6.3；版本接口返回 v1.6.3 / a7a8fe7；健康检查通过，重启计数 0 | 不代表统计口径正确 |
| 容器启动 | 2026-09-30 09:36:50.755 +08:00 | 与源码/发布记录分开核实 |
| 实际界面 | SAO / Komari-Theme-SAO v1.0.10；不是仓库默认前端 | 默认前端测试不能证明 SAO 显示正确 |
| 指标存储 | SQLite，/opt/nekomari/data/metrics.db；启用降采样；raw 保留 10 分钟 | 现场查询使用 mode=ro、query_only 和只读事务 |
| 存储保留 | minute 600 分钟；5min 3000 分钟；hour 600 小时；record 720 小时；ping 24 小时 | 各配置含义不同，不能只看总保留天数 |
| 当前 Agent | 10 个当前注册节点的 Agent 自报 v1.6.2；检查时 CPU 最新点约 5 秒内 | 不将面板/Agent 版本差异单独判为错误；已删除节点历史不计入当前在线节点 |
| MAC-WAN | 实为 Ubuntu 24.04.5 / Linux 6.8.0-139-generic；用户级 nekomari-agent.service 运行 | 不能根据主机名称假定 macOS |
| MAC-WAN 采集 | -i 5；基本信息每 10 分钟；exclude-nics lo,docker0；month-rotate 1；关闭自动更新 | 基本信息 updated_at 不是 5 秒心跳 |
| 访问边界 | private_site=true；/api/clients 返回 401 | 未绕过登录，未读取会话或 Agent token；没有已登录浏览器端到端验收 |

MAC-WAN Agent 进程启动于 2026-09-29 18:40:47 +08:00。安装文件的 SHA256 为 f83ff97493541fe54b476fd7169d563f0be1eb72242a73af5aa2bec78132ad49；由于 /proc/PID/exe 读取权限限制，**没有核实运行中可执行文件的哈希**，安装文件哈希不替代运行身份。

## 1. [P1] 累计流量按桶作 last-first，正常连续上报也会漏计

定位：`C:\Users\SRTco\Nekomari\pkg\metric\rollup.go:367-386`；`C:\Users\SRTco\Nekomari\pkg\metric\batch_series.go:495-510,550-577`；`C:\Users\SRTco\Nekomari\web\rpc\jsonrpc\public.metric.go:784-801`。

`traffic.up/down` 写入的是计费周期累计值，而非单次消耗。查询把客户端的 sum 映射为 delta，但历史桶只用 last-first；相邻输出桶之间的正常增量未计入。这里不是长时间掉线或数据缺口：两个样本即使按正常 5 秒节奏处于桶边界两侧，该增量仍被遗漏。少于 2 个点的桶还直接返回 0。

### 线上固定窗口

节点 OC424，2026-09-30 10:00-11:30；实际首末样本 10:00:05.753-11:29:55.753。只纳入相邻边界时间差大于 0、小于 15 秒且累计值未回退的增量。该窗口不跨迁移或计数器重置。

| 指标 | 聚合粒度 | 桶内 delta 合计 / B | 漏掉的正常边界增量 / B | 观测累计值增长 / B | 漏计比例 |
| --- | ---: | ---: | ---: | ---: | ---: |
| traffic.up | 1 分钟，90 桶 | 31,350,613 | 6,704,285 | 38,054,898 | 17.6174% |
| traffic.up | 5 分钟，18 桶 | 37,205,628 | 849,270 | 38,054,898 | 2.2317% |
| traffic.down | 1 分钟，90 桶 | 18,696,037 | 3,274,088 | 21,970,125 | 14.9025% |
| traffic.down | 5 分钟，18 桶 | 21,596,660 | 373,465 | 21,970,125 | 1.6999% |

SAO 常规流量查询以 5 分钟目标粒度计算，因此该现场样本更直接对应约 2.23% 上行、1.70% 下行漏计；1 分钟结果说明误差随粒度变化。这里的参照是上报累计值增长，**不是运营商账单或全站全天的误差率**。

最小复现：累计值 100、150、200、250 分别处于两个 1 分钟桶。真实观测增长 150；1 分钟查询合计 100，2 分钟查询合计 150。同一数据改变图表分辨率就改变消耗总量。

另一个已复现但未声称当前窗口触发的边界：900、1200、100、200，raw 按已定义重置规则累计为 500，rollup 只保留首末关系得到 200；单靠 first/last 无法恢复桶内多个增长段。

建议：将可验证的相邻采样增量在采集/入库阶段转为可加和的 interval delta，rollup 保存 delta 的和与覆盖度；引入 counter epoch，区分月度轮换、重启、网卡变化和历史基线恢复。正常采样边界不能当作未知缺口，真正缺口必须标记未知，不得任意补 0 或制造增长。

## 2. [P1] raw 与 rollup 返回值语义不一致，SAO 补点仍会累加周期累计值

定位：`C:\Users\SRTco\Nekomari\web\rpc\jsonrpc\public.metric.go:302-314,375-401,431-433`；线上 `SAO/dist/assets/themeSettings-CYV_2lCd.js` 的 `Ot`、`je`、`Me`、`Re`。

最近短窗口走 raw 分支时，即使已解析算法为 delta，返回值仍是每个样本的周期累计值，Count=1；长窗口则返回聚合后的消耗。消费方不能可靠地把这两种序列当成同一种量。

线上 SAO 的流量算法表仍指定 sum。常规历史查询走服务端 delta，但边界补点函数 Ot 对缺失/null 桶发起短窗口查询，返回 raw；Me/je 再按 sum 合并这些原始累计值。只有满足缺失/补点条件时触发，**不是所有 SAO 查询都会产生这个放大结果**。

验证分两部分：

1. 本地服务端 helper 在 mapped_algorithm=delta、downsampled=false 时返回 1000、1010、1020。消费者求和为 3030，观测增长只有 20。
2. 从 OC424 读取正在使用的 SAO 资产，在独立 VM 中执行实际 Me/je 纯函数：一个缺失 5 分钟桶用上述 3 点修补，结果同样为 3030 / Count=3。

资产 SHA256：6950e5729b960fd90465fca94e6fd27cb554e7dd34ed3ad7ec23c3f7ef8a3e66。纯函数复现不替代已登录浏览器现场复现，因此本报告不声称已经看到某位用户的账单或页面被放大。

建议：API 明确区分 counter、interval_delta、rate，raw/rollup 保持业务量语义一致；不要将同一 metricKey 在不同查询路径中切换为累计值和消耗值。SAO 的补点需遵守同一契约，不能依赖服务端只在历史分支偷偷将 sum 重解释为 delta。

## 3. [P2] 失败 Ping 的 -1 进入延迟聚合，使平均值偏低、最小值丢失

定位：`C:\Users\SRTco\Nekomari\internal\metricstore\ping_records.go:61-78`；`C:\Users\SRTco\Nekomari\pkg\metric\rollup.go:244-268`；`C:\Users\SRTco\Nekomari\web\rpc\jsonrpc\public.metric.go:977-984,1115-1144`。

失败记录同时写入 latency=-1 和 loss=1；latency 的 count、sum、min、digest 均未排除失败。查询只在聚合后过滤负数，无法排除混在正平均值里的 -1。

最小复现：[100,-1,100]，有效 Ping 2 个，总探测 3 个。应为成功样本均值 100 ms、最小值 100 ms；实际均值 66.3333 ms、最小值 nil。独立 loss=33.3333% 是正确的，问题是延迟统计的样本集合错误。

现场证据：MegaBox，task_id=17 / ICMP IPv4，2026-09-30 10:40-10:45 的 5 分钟桶：

```text
ping.latency_ms: count=5, sum=616, min=-1, max=158
ping.loss:       count=5, sum=1
成功数=4；成功延迟和=617；正确成功均值=154.25 ms
当前桶均值=616/5=123.2 ms，比成功均值低约 20.13%
```

建议：latency 的统计仅包含成功样本；探测总数、失败数和覆盖度单独保留。不要因为排除 -1 而把失败从丢包分母中删除。历史混合桶的最小值和百分位无法仅凭当前 first/last/sum 精确还原，应注明旧统计语义或使用可恢复的数据重建。

## 4. [P2] 全窗口 P50/P99/标准差不能由桶统计加权平均得到

定位：`C:\Users\SRTco\Nekomari\web\rpc\jsonrpc\public.metric.go:978-981`。

接口对各桶的 P50、P99、StdDev 做 count 加权平均。平均延迟在样本有效时可以这样合并；百分位不是线性量，标准差还必须包含桶间均值变化。这会导致长期抖动被隐藏，且统计值随查询粒度变化。

反例：100 个 10 ms 样本，加 1 个 1000 ms 样本。当前 P50=19.80198 ms，正确 P50=10 ms；两个桶各自标准差为 0，接口得到 0，整体总体标准差实际为 98.019802 ms。

现场 MAC-WAN，2026-09-30 10:00-11:30，90 个一分钟桶、每桶 1 个成功 Ping：

| ICMP IPv4 任务 | 最小 / 最大 ms | 当前算法的窗口标准差 ms | 从 count/sum/sum_sq 合并得到的总体标准差 ms |
| --- | --- | ---: | ---: |
| task_id=18 | 32 / 308 | 0 | 28.837082 |
| task_id=19 | 50 / 326 | 0 | 28.924884 |

这是对真实存储桶应用当前公开接口算法得到的结果，**不是已登录页面或已认证 RPC 的截图/响应**。

建议：全窗口标准差合并 count、sum、sum_sq 后计算，明确总体/样本标准差口径；P50/P99 合并可合并的 digest，或从有效原始样本重算，披露近似误差；不能平均各桶百分位。

## 基础资源核对：没有将口径差异误判为缺陷

MAC-WAN 的核对来自 Linux 内核原始计数器、挂载点和服务端落库值。采样没有完全同步，不能据此报出 CPU 或瞬时网络速率的精确相对误差。

- 内存总量 8,235,036,672 B。Agent 采用 htop 风格 used 公式，某次现场算得 1,234,243,584 B；`total-MemAvailable` 为 1,551,843,328 B，差约 320 MB 属于定义差异。应在 UI/文档明确 used 的定义，不能直接据此宣布采集错误。
- Swap 的 Agent 定义扣除 SwapCached；现场值 185,049,088 B 与落库值吻合。常见 total-free 口径为 185,335,808 B，差 286,720 B；应说明 cached 的处理。
- 磁盘是多个纳入挂载点合计，而非仅 `/`。总容量 251,380,572,160 B 与注册信息一致；现场使用量 30,265,122,816 B，近期存储约 30,265,118,720 B，非同步窗口差 4 KiB。不能把 root-only df 与全挂载点合计直接比较。
- TCP 含 TIME_WAIT 的计数在同一次主机核对中为 21，UDP 为 4，与相应内核条目计数相符；不同时间落库的 TCP=19 不足以证明错误。
- CPU、瞬时速率已检查采集路径，但未完成同步受控负载校准；不声明精度已通过验收。近期指标新鲜度不等于采样时间、传输时延已完整验证。

其他代码层风险，尚未声称现场触发：采集失败可能生成 0 或 CPU 最小值而非缺失；服务端采用接收时间，缺少独立 sampled_at/quality；计数器恢复缺少明确 epoch。建议缺失值、最后有效值年龄、采样覆盖度与异常原因一并传递。

另见 2026-09-29 的大幅累计值基线变化：OC424 上行在 16:39:59.203 至 16:53:39.758 之间变化 30,582,945,739 B，下行变化 35,185,002,151 B。代码近期加入历史基线恢复，但现场不足以证明这些跳变全部由该修复造成，也不能把全部跳变认定为真实即时流量。升级边界需要标记，历史图暂不宜用于精确对账。

## 验证结果与复现

已通过的原有基线：

```text
根模块：go test ./... -count=1 -skip '^TestIpInfo$'
Agent：go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing'
默认前端：npm test，105 passed / 0 failed
审查复现：go test ./pkg/metric ./web/rpc/jsonrpc -run '^TestAccuracyAudit' -count=1 -v
```

跳过的是依赖外部 IP 信息或在线探测的测试；测试通过不等于测量口径正确。**审查复现测试的 PASS 表示成功复现旧缺陷，不表示已经修复**。临时测试已移出生产测试目录，以免以后把旧错误当成正确行为维护。

复现材料：`C:\Users\SRTco\Nekomari\docs\audits\2026-09-30`。

- `metric_repro_test.go.txt`：桶边界、粒度依赖、桶内 reset。
- `rpc_repro_test.go.txt`：raw 流量、失败 Ping、全窗口统计。
- `sao-repair-repro.cjs`：通过已有 SSH 只读加载固定哈希的线上主题资产，在本地 VM 回放纯函数；资产变化时拒绝继续。
- `live-evidence.py`：通过已有 SSH 在 OC424 用只读 SQLite 重查上述固定窗口，不读取会话/凭据。旧桶可能被保留策略清理，不应将清理后的空结果当成未曾发生。

在 PowerShell 重放源码夹具（只在未占用的指定临时路径写入，结束后清理）：

```powershell
Set-Location 'C:\Users\SRTco\Nekomari'
$source = 'C:\Users\SRTco\Nekomari\docs\audits\2026-09-30'
$metric = 'C:\Users\SRTco\Nekomari\pkg\metric\accuracy_audit_tmp_test.go'
$rpc = 'C:\Users\SRTco\Nekomari\web\rpc\jsonrpc\accuracy_audit_tmp_test.go'
if ((Test-Path -LiteralPath $metric) -or (Test-Path -LiteralPath $rpc)) {
    throw 'Temporary path already exists; do not overwrite it.'
}
$created = @()
try {
    Copy-Item -LiteralPath "$source\metric_repro_test.go.txt" -Destination $metric
    $created += $metric
    Copy-Item -LiteralPath "$source\rpc_repro_test.go.txt" -Destination $rpc
    $created += $rpc
    go test ./pkg/metric ./web/rpc/jsonrpc -run '^TestAccuracyAudit' -count=1 -v
} finally {
    foreach ($path in $created) { Remove-Item -LiteralPath $path }
}
node "$source\sao-repair-repro.cjs"
Get-Content -LiteralPath "$source\live-evidence.py" -Raw | ssh -o BatchMode=yes -o ConnectTimeout=12 OC424 'python3 -'
```

## 修复顺序与验收门槛

1. 优先统一流量的 counter/delta 契约，连同 SAO 补点一起验证；不能只改默认前端或只修历史 sum。
2. 将失败探测从 latency 样本排除，保留独立 loss/total/coverage；修复跨桶方差和 digest 合并。
3. 引入 sampled_at、质量标志、counter epoch、未知缺口及历史语义版本；明确内存/Swap/磁盘/TCP 口径。
4. 修复后在 OC424 和 MAC-WAN 重新做已登录真实主题验收，不仅依赖本地测试或构建。

必须验证的性质：同一有效窗口改变 maxPoints、raw/rollup 切换、1min/5min/1h 粒度及补点路径，流量总量应保持一致；重启/月度 reset/升级恢复/网卡变化不得制造流量；Ping 成功样本统计不能被失败样本稀释；标准差和百分位与原始基准一致或在明确近似误差内；缺失不等同于 0 或“在线”。

历史混合聚合数据未必能无损修复；上线前应备份并制定可重建范围与旧数据标记策略。本次未执行上述修复或重建。
