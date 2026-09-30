# 2026-09-30 生产验收脚本

这些脚本记录 OC424 v1.6.5 与 MAC-WAN v1.6.4 的一次限定范围验收，不是通用部署工具，也不是全网探针升级器。最终结果和身份见 `../../MONITORING-ACCURACY-FIXES-2026-09-30.md` 第 8 节。

## 范围与安全边界

| 文件 | 执行位置 | 行为 |
|---|---|---|
| `canary-kernel.py` | MAC-WAN，普通用户 | 只读采集内核网络、内存、Swap、磁盘、TCP/UDP |
| `canary-panel.py` | OC424，具有面板数据库读取权限 | 只读 API/SQLite；record、analyze、boundary、boundary-check、ping-check、fleet |
| `canary-runtime.py` | OC424，具有 Docker/数据库读取权限 | 只读检查版本、digest、挂载、认证、数据库与日志 |
| `sao-interval-contract.cjs` | 本地 Node.js + SSH OC424 | 校验固定已部署 SAO 资产 SHA，再回放纯函数；不是在线图表数值验收 |
| `deploy-oc424-v165.sh` | OC424，root | **变更生产**：停服务、完整备份、切换固定镜像；仅用于已执行的 v1.6.4 -> v1.6.5 升级审计 |

- `canary-panel.py` 固定当前 MAC-WAN UUID，fleet/runtime 固定 10 个注册节点。`canary-kernel.py` 的三个物理挂载点和网卡排除前缀来自当时的 MAC-WAN 配置。环境变化应先复核脚本，不应放宽断言来制造通过。
- API key 由现有 `deploy/nekomari_auth.py` 在 OC424 进程内读取，不打印、不复制到本地或浏览器。运行时把这个 helper 放到 canary 脚本同目录。不要输出数据库、token-file、完整服务配置或带认证头的请求。
- SSH 复用现有 OC424/MAC-WAN 配置；不把 sudo 密码写入命令、文件或报告。OC424 使用已经可用的 `sudo -n`；权限不满足时停止，不更改权限策略。
- 采样与结果目录应为 0700，原始采样只在本地私有证据目录归档，不能整目录加入 Git。脚本内断言是门槛，使用正常 `python3`，不要使用 `-O` 或设置 `PYTHONOPTIMIZE`。
- “boundary” 模式本身只读；实际重启另行执行，仅限已获授权的 MAC-WAN 用户服务。不要重启其他探针或面板来扩大验收范围。

## 准备与并行采样

当次 OC424 staging 为 `/tmp/nekomari-v1.6.5-20260930`，MAC-WAN staging 为 `/tmp/nekomari-v1.6.4-20260930`。它们均为 0700 用户目录。下面示例沿用这些路径；重新执行时应准备新的私有目录和匹配脚本，不覆盖原始证据。

必须在两个终端**同时开始**以下采集，不能先采完一侧再启动另一侧：

MAC-WAN 终端（普通用户）：

```sh
umask 077
python3 /tmp/nekomari-v1.6.4-20260930/canary-kernel.py --seconds 120 > /tmp/nekomari-v1.6.4-20260930/mac-kernel.json
```

OC424 终端（SSH 登录用户，在私有目录重定向）：

```sh
umask 077
sudo -n python3 /tmp/nekomari-v1.6.5-20260930/canary-panel.py record --seconds 120 > /tmp/nekomari-v1.6.5-20260930/mac-reports.json
```

等待两侧退出码为 0，把 MAC-WAN 的 `mac-kernel.json` 通过本地中转复制到 OC424 staging；不要采用串行远程采集。复制的是采样结果，不是认证信息。传输后立即在 OC424 运行：

```sh
sudo -n python3 /tmp/nekomari-v1.6.5-20260930/canary-panel.py analyze \
  --panel-file /tmp/nekomari-v1.6.5-20260930/mac-reports.json \
  --kernel-file /tmp/nekomari-v1.6.5-20260930/mac-kernel.json
```

该版本 raw 短期保留窗口为 10 分钟：分析会再次查询服务端，旧采样过期后不能重放成同样结论，必须重新并行采集。出现空结果、未对齐、epoch 改变或断言失败都属于未通过；生成了 JSON 文件不代表退出码成功。

正常验收包括：至少 15 份对齐报告、内核累计计数区间、htoplike 内存、Swap、磁盘、连接数量、实际时间间隔网络速率；并以 max_points=1/2/500 分别对账约 5 秒、60 秒和完整窗口流量。

## 重启边界复测

仅在重新确认可重启当前 MAC-WAN 探针后进行。在 OC424 开始 60 秒捕获：

```sh
umask 077
sudo -n python3 /tmp/nekomari-v1.6.5-20260930/canary-panel.py boundary --seconds 60 > /tmp/nekomari-v1.6.5-20260930/restart-reports.json
```

约 10 秒后，在 MAC-WAN 普通用户会话执行一次：

```sh
systemctl --user restart nekomari-agent.service
systemctl --user is-active nekomari-agent.service
```

捕获结束后立即在 OC424 检查，不要超过 raw 保留窗口：

```sh
sudo -n python3 /tmp/nekomari-v1.6.5-20260930/canary-panel.py boundary-check \
  --panel-file /tmp/nekomari-v1.6.5-20260930/restart-reports.json
```

门槛：两个 epoch 均至少两份报告；新 epoch 首份网络速率未知/null，下一份恢复；跨重启 interval.valid=0；流量仅统计各 epoch 内的有效累计差值。

## 只读状态、Ping 与主机检查

在 OC424 执行：

```sh
sudo -n python3 /tmp/nekomari-v1.6.5-20260930/canary-panel.py fleet
sudo -n python3 /tmp/nekomari-v1.6.5-20260930/canary-panel.py ping-check
sudo -n python3 /tmp/nekomari-v1.6.5-20260930/canary-runtime.py \
  --version v1.6.5 \
  --commit 4847bf84ba91b7b933e314431fe6d9ec0824a3d7 \
  --image ghcr.io/aone2233/nekomari:v1.6.5@sha256:b780f99498765f25f86353258d4a9d04016e1229fbd6d7e929f24210992c3b56
sudo -n nginx -t
timedatectl show -p NTPSynchronized
```

Ping 使用延后一整分钟的两个闭合分钟桶，以免内存中的新写入和 SQLite 快照混比；同时比较两个点数预算、次数、丢包率、成功样本均值/标准差及 min/max。分位数只检查有序和上下界，仍是 approximate，不是精确分位数证明。

实际探测服务为 `panel-probe.service`，不要使用不存在的猜测服务名。经授权的探测执行方式：

```sh
sudo -n systemctl start panel-probe.service
sudo -n systemctl show panel-probe.service -p ActiveState -p Result -p ExecMainStatus -p ExecMainExitTimestamp
```

这是 oneshot：执行后 inactive 是正常结果，要求 Result=success、ExecMainStatus=0。命令失败时停止检查，不用后一条成功输出掩盖失败。

在本地运行固定 SAO 资产纯函数回放：

```sh
node docs/audits/2026-09-30/sao-interval-contract.cjs
```

浏览器另行实际点击管理页服务器名称、工作台侧栏并刷新 `/terminal`；公开页负载/Ping 图表的可用性与 API/内核数值对账分开记录。截图或零控制台错误不能替代数值验收。

## 已执行部署与回滚证据

`deploy-oc424-v165.sh` 要求旧 Compose 正好指向记录的 v1.6.4 digest，以及固定 v1.6.5 ARM64 image revision、Release 二进制和镜像内二进制 SHA 一致；当前已经升级，不应再次运行。脚本采用 stopped-write 完整备份，失败后恢复旧 Compose 并尝试重新启动，但回滚成功仍须重新验收。

- 面板停写备份：`/opt/nekomari/backups/v1.6.4-before-v1.6.5-20260930T122413Z`。
- MAC-WAN 升级前备份：`/home/macos/nekomari-agent/backups/agent-before-v1.6.4-20260930T111631Z`。
- 面板回滚优先恢复旧固定 digest 的 Compose 并重新验证；不要自动恢复数据库而丢弃升级后的写入。只有确认数据兼容性问题且批准丢弃新写入时，才考虑完整数据恢复。
- 探针回滚须同时保留原 unit、token-file 所有权/0600、二进制 root:root/0755 和 cap_net_raw；不要只更换二进制后省略权限与运行验证。
- 数值 CPU 独立对账、全网探针升级、所有生产历史压缩层数值验收、面板启动恢复性能不在本次通过范围内。

原始证据保存在本地 `.accuracy-smoke/release-v1.6.5`；只提交脚本、此说明和脱敏后的最终报告，不提交原始机器报告、数据库或发布二进制。
