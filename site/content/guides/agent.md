# 探针

探针是一个单文件 Go 程序，无运行时依赖。它做两件事：**上报指标**（HTTP）、**接收任务**（WebSocket 长连接）。

## 常用参数

| 参数 | 说明 |
|---|---|
| `-e, --endpoint` | 面板地址，例如 `https://panel.example.com` |
| `-t, --token` | 节点 token。生产环境建议用 `--token-file` |
| `--token-file` | 从文件读取 token（权限 `0600`），避免出现在 `ps` 和 `systemctl show` 里 |
| `-i, --interval` | 指标采集间隔（秒） |
| `--info-report-interval` | 上报间隔（秒），默认 10 |
| `--exclude-nics` | 排除的网卡，通常是 `lo,docker0` |
| `--month-rotate` | 流量计数器按月的重置日，与服务商账单日一致 |
| `--disable-auto-update` | 关闭探针自行升级 |
| `--disable-web-ssh` | 关闭 Web 终端与远程执行 |
| `--ignore-unsafe-cert` | 跳过 TLS 校验（仅用于自签证书） |
| `--auto-discovery` | 用自动发现密钥注册，而不是预置 token |

!!! danger "token 不要写在命令行上"
    `-t` 会出现在 `ps` 和 `systemctl show` 的输出里，任何能读进程列表的用户都能看到。用 `--token-file`。

    安装脚本默认就是写文件的。

## 两种接入方式

### 预置 token（推荐）

在面板里添加节点 → 复制安装命令 → 在目标机执行。token 是**该节点专属**的。

### 自动发现

适合批量铺开：配置一个**自动发现密钥**，探针首次启动时用它向面板注册，换取自己的 token 并写入本地文件。

```bash
komari-agent --auto-discovery <key> -e https://panel.example.com
```

!!! warning "自动发现的两个副作用"
    - 注册成功后会**新建一个节点条目**。如果这台机器之前以别的身份存在过，会出现重复条目 —— 删掉不需要的那个。
    - 重装或删除本地注册文件会让它**重新注册成新节点**，而不是回到原来的条目。原有的分组、账单、历史曲线留在旧条目上。

    所以：**能预置 token 就预置 token**，自动发现留给"确实要批量铺开"的场景。

## 探针的升级

探针有两条升级路径，**面板下发的是推荐路径**。

### 面板下发（推荐）

面板可以指定某个节点运行哪个版本，**包括更早的版本**（用于回滚）。细节见[运维能力](../features/operations.md#探针升级由面板下发)。

### 自行跟踪最新发布

探针默认每 6 小时检查一次最新 Release 并自我替换。这是 `--disable-auto-update` 关掉的行为。

它适合"没人在看的小机群"，但有两个问题：

- **无法回滚** —— 只能往前
- **无法分批** —— 一次发布同时推向所有节点

机群需要按批次推进时，用面板下发。

## 以非 root 运行

探针不需要 root 也能上报指标，但有两处会受影响：

| 功能 | 影响 |
|---|---|
| **ICMP 探测** | 需要 `cap_net_raw`，见[监控与告警](../features/monitoring.md#一个必须知道的限制icmp-需要权限) |
| **部分硬件信息** | 某些 GPU、磁盘 SMART、温度需要更高权限才能读到 |

降权的收益是安全边界，代价是部分指标缺失。**缺失的指标会明确标注**，不会静默显示为零。

!!! warning "文件能力会被二进制替换丢掉"
    `cap_net_raw` 是**文件属性**。用 `cp` 或 `install` 替换二进制会丢掉它 —— 两者只复制内容和权限位。替换后必须重新 `setcap`，回滚到旧二进制同样如此。

    而且两个 systemd 设置会**静默废掉**文件能力，即使 `/proc/<pid>/status` 里显示 `CAP_NET_RAW` 仍在有效集中：
    `NoNewPrivileges=yes` 与 `PrivateTmp=yes`。非 root 跑 ICMP 时**两者都要留空**。

## 健康检查

```bash
journalctl -u komari-agent -f          # systemd
journalctl --user -u komari-agent -f   # systemd 用户级单元
rc-service nekomari-agent status       # Alpine / OpenRC
```

启动日志会明确报告几件事，值得对上：

- 找到的网卡与挂载点
- **ICMP 是否可用**（不可用时写明原因）
- 与面板的认证是否成功
- WebSocket 是否建立

## 常见问题

??? question "面板上一直离线，但进程在跑"
    看日志里有没有认证失败（`401`）。最常见的原因是 token 与面板记录不一致 —— 例如重装过探针、或删掉节点后又用同一个 token 启动。

    注意日志里的错误是**探针自己报的**，而面板上的"离线"是**面板的判断**，两者可能不同步。

??? question "数据在，但远程执行和终端用不了"
    上报走 HTTP、下发走 WebSocket，**两条是独立的**。这种组合说明 WebSocket 没建立起来，通常是反向代理没透传 `Upgrade` 头或读超时太短。见[部署](deployment.md)。

??? question "流量数字明显偏大"
    检查 `--month-rotate` 是否设成了服务商账单日。不设的话探针上报的是内核自开机以来的累计值，面板除以月额度就会得到一个偏大的百分比。
