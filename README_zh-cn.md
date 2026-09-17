# Nekomari

[English](./README.md) | [简体中文](./README_zh-cn.md)

> ### 🐱 Nekomari 是 [Komari](https://github.com/komari-monitor/komari) 的 fork
>
> **基线版本：`1.5.0-fix1`（commit `0ca87aa`，2026-09-14）** —— 上游归档前发布的最后一个版本。
> 上游仓库已 **归档**、不再维护；Nekomari 在其基础上继续开发新功能。
> 完整的来源说明与改动清单见 **[FORK.md](./FORK.md)**。
>
> 许可证与上游一致（MIT）。原始版权声明 `Copyright (c) 2025 Komari Moniter` 已在 `LICENSE` 与 `NOTICE` 中**原样保留**。

Nekomari 是一款轻量级的自托管服务器监控工具 —— Komari 的持续维护版本。它支持通过 Web 界面查看服务器状态，并通过轻量级 Agent 收集数据。

> [!WARNING]
> Nekomari 是一款自托管的监控/控制程序，仅应部署在你拥有或已获得授权管理的系统上。在未获授权的情况下部署、访问、持久化、执行命令及从事其他滥用行为，用户需要自行承担部署和使用的责任。开发者不对未经授权或滥用行为及其后果承担责任。

## Nekomari 相对上游新增了什么

| 改动 | 状态 |
|---|---|
| **`netcheck`** —— 多协议可达性自检（DNS + ICMP + TCP），直接告诉你目标该用哪种探测类型 | ✅ 已可用 |
| 单仓结构 —— 服务端 + `frontend/` + `agent/` 合并到一个仓库 | ✅ |
| 自带主题打包器（`tools/zstdpack`）—— 无需 `zstd` CLI 即可构建（Windows 上尤其实用） | ✅ |
| 一键构建脚本（`build.sh`） | ✅ |
| 更多功能开发中 | 🚧 见 [FORK.md](./FORK.md) |

### 20 秒看懂 `netcheck`

监控任务对目标只用**一种**协议探测（ICMP / TCP / HTTP）。**选错协议就会得到恒定的 100% 丢包** —— 看起来像宕机，实际是协议不匹配。`netcheck` 让你提前知道该选哪种：

```
$ ./nekomari netcheck 1.51.3.134

ICMP 探测 (3 次, 单次超时 3s)
  ✗ 收 0/3  丢包 100%   —— 目标不响应 ICMP
TCP 探测 (单次超时 3s)
  ✗ 22     不可达
  ✓ 80     开放   (握手 2 ms)
  ✓ 443    开放   (握手 2 ms)
----------------------------------------------------------
判定: 仅 TCP 可达（不响应 ICMP）
建议: 该目标【不响应 ICMP】，但 TCP:80,443 可用。监测任务必须选 tcp，
      目标写成 host:port（例如 1.51.3.134:80），否则会 100% 超时。
```

它还会刻意区分 **「权限不足」与「目标不可达」** —— 否则非特权环境下的 ICMP 失败会得出**完全相反**的结论。

## 特性

以下是 Nekomari 从 Komari 继承的基础能力：

- **实时监控** —— 秒级数据展示。
- **轻量** —— 占用极低，小机器也能跑。
- **自托管** —— 数据留在自己的机器上。
- **Web 界面** —— 提供前台展示与后台管理两套面板。
- **可扩展** —— 支持自定义主题与插件。

## 快速开始

### 直接下载产物

从 [Releases](https://github.com/Aone2233/Nekomari/releases) 下载。每个版本都提供
服务端与探针，覆盖 `linux/amd64`、`linux/arm64`、`windows/amd64`，并附
`SHA256SUMS.txt`。

```bash
./nekomari-linux-amd64 server          # 打开 http://localhost:25774 完成安装
./komari-agent-linux-amd64 -e https://你的面板 -t <token>
```

探针产物的文件名保留 `komari-agent-` 前缀 —— 那是探针自更新逻辑要查找的名字，
理由见 [docs/RELEASING.md](./docs/RELEASING.md)。

### 从源码构建

需要 Go（版本见 `go.mod`）、Node 22+，以及 C 工具链 —— 服务端通过 CGO 链接
SQLite，所以 `CGO_ENABLED=0` 构建不出来。

```bash
./build.sh          # 前端 -> 内嵌主题 -> 服务端 -> 探针
```

`build.sh` 自带主题打包器，不依赖外部的 `zstd` 命令。

### 安装说明

安装向导会要求填写 **指标库 DSN**（默认 SQLite）。基础软件的 Docker 与部署方式
可参考[安装文档](https://www.komari.wiki/en/install/quick-start) —— 那份文档描述的是
Komari，也就是 Nekomari 的基座；两者不一致之处，以本仓库的 `docs/` 为准。

## 文档

| 文档 | 内容 |
|---|---|
| [FORK.md](./FORK.md) | 来源、基座 commit、完整改动清单 |
| [docs/RELEASING.md](./docs/RELEASING.md) | 如何发版，以及两个容易踩的坑 |
| [docs/TESTING.md](./docs/TESTING.md) | 哪些测试是密闭的，哪些需要 root 或 IPv6 |

## 来源与致谢

Nekomari 是 **[Komari](https://github.com/komari-monitor/komari)** 的 fork，
基座为 `1.5.0-fix1`（commit `0ca87aa`）。上游已归档，本项目在其基础上继续维护。

感谢所有为 Komari 做出贡献的人 —— 见
[上游贡献者列表](https://github.com/komari-monitor/komari/graphs/contributors)。
他们不对本项目新增的任何内容负责。

**赞助与捐赠：本 fork 没有。** 上游 README 里原有的赞助商与收款码属于上游作者，
这里已刻意移除 —— 目的是不让本仓库暗示任何并不存在的背书关系或收款渠道。
如果你想支持原作者，请通过
[上游仓库](https://github.com/komari-monitor/komari)进行。

## 许可证

MIT，未作改动。原始版权声明逐字保留在 [LICENSE](./LICENSE) 与 [NOTICE](./NOTICE) 中。

