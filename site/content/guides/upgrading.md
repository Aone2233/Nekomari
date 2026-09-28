# 升级

**面板和探针各有各的升级路径。** 分开管理，互不阻塞。

## 面板

```bash
# 1. 备份数据目录（每次都做）
cp -a /opt/nekomari/data /opt/nekomari-backups/pre-$(date +%Y%m%d-%H%M%S)

# 2. 改 compose 里的 tag
sed -i 's|nekomari:v[0-9.]*|nekomari:v1.6.1|' docker-compose.yml

# 3. 拉取并重建
docker compose pull && docker compose up -d

# 4. 确认
curl -s http://127.0.0.1:25774/api/version
```

`/api/version` 同时返回版本号与提交哈希，可以和 Release 对照确认拉到的确实是那一版。

!!! danger "不要省略 `-v`"
    见[安装](../get-started/install.md#docker)里那段说明：匿名卷会让升级看起来像"配置被重置了"。

## 探针

### 面板下发（推荐）

面板 → 运维 → 探针版本，指定目标版本。可以按节点选择，**也可以指向更早的版本**（回滚）。

每个节点的结果会分别报告：

| 结果 | 含义 | 该做什么 |
|---|---|---|
| `dispatched` | 已下发 | 等它重启，然后确认版本变了 |
| `at_target` | 本来就在该版本 | 无需处理 |
| `offline` | 未连接，请求没送到 | 节点不在线，**请求不会补发** |
| `failed` | 下发失败 | 看该节点探针日志 |

### 直接替换二进制

面板升级不了"还不理解指定版本指令"的旧探针。那种情况必须直接替换：

```bash
# 1. 备份
cp -a /opt/nekomari-agent/komari-agent-linux-amd64{,.pre-$(date +%Y%m%d-%H%M%S)}

# 2. 下载并核对
curl -fsSLO https://github.com/Aone2233/Nekomari/releases/download/v1.6.1/komari-agent-linux-amd64
curl -fsSLO https://github.com/Aone2233/Nekomari/releases/download/v1.6.1/SHA256SUMS.txt
sha256sum -c --ignore-missing SHA256SUMS.txt

# 3. 替换并重启
install -m 0755 komari-agent-linux-amd64 /opt/nekomari-agent/komari-agent-linux-amd64
systemctl restart komari-agent
```

!!! warning "两条容易踩的坑"
    **一、文件能力。** 如果探针以非 root 运行且需要 ICMP，`install` 会丢掉 `cap_net_raw`。替换后重新设置，否则 ICMP 静默失效：

    ```bash
    sudo setcap cap_net_raw+ep /opt/nekomari-agent/komari-agent-linux-amd64
    ```

    回滚到旧二进制同样需要重新 `setcap` —— 能力是文件属性，不在二进制内容里。

    **二、先验证能跑再替换。** 架构或 libc 不匹配的二进制替换上去会直接起不来：

    ```bash
    ./komari-agent-linux-amd64 --help    # 能打印用法再往下做
    ```

## 升级顺序

机群 > 1 台时，**先升级面板，再按批升级探针**。

原因是探针需要理解面板下发的指令。反过来做（先升探针）通常也能工作，但面板侧的新能力要等面板升级后才可用。

按批推进、每批确认：

1. 升级面板，确认 `/api/version` 与面板可访问
2. 升级**一台**探针，确认它仍在线、指标在更新、ICMP 状态正常
3. 升级其余探针，分批
4. 全量确认：所有节点在线、版本一致

## 回滚

| 组件 | 回滚方式 |
|---|---|
| 面板 | compose 改回旧 tag，`up -d`。数据目录向下兼容，但**新版本写过的数据旧版本未必认识** —— 所以升级前的备份要留着 |
| 探针 | 面板指定旧版本；或直接替换回备份的二进制（记得重设文件能力） |

!!! warning "回滚面板前想清楚数据"
    面板升级可能改变数据结构。回滚**二进制**很容易，但如果新版本已经写入过新格式的数据，旧版本可能无法读取。所以升级前的数据目录备份是回滚的真正依托。

## 自动化

仓库提供整队升级脚本，按节点形态自动识别探针位置与服务管理方式：

```bash
# 在能 ssh 到各节点的机器上执行
sh deploy/fleet/fleet-upgrade.sh v1.6.1
```

它会：逐节点升级、保留旧二进制、**保留文件能力**、重启后报告服务状态与 ICMP 可用性。

清单文件 `deploy/fleet/inventory.txt` 记录每个节点的形态（systemd / 用户级 systemd / OpenRC），以及**批量脚本不应触碰的节点**。
