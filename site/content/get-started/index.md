# 快速开始

装 Nekomari 分两步：**先起面板，再把探针装到被监控的机器上**。顺序不能反 —— 探针需要一个面板地址和一个 token 才能启动。

<div class="nk-grid" markdown>

<div class="nk-card" markdown>
### 第一步
[安装面板](install.md) —— 一个容器，带内置数据库。
</div>

<div class="nk-card" markdown>
### 第二步
[添加节点](quick-start.md) —— 生成 token、在目标机上装探针。
</div>

</div>

## 大概需要多久

一台已经装好 Docker 的机器，**大约五分钟**：拉镜像、写一份 compose、`up -d`，然后在面板里加一台节点、复制一行安装命令到目标机执行。

## 开工前确认

| 项 | 要求 |
|---|---|
| 面板主机 | Linux，装了 Docker（或有 Go 1.26+ 想从源码构建） |
| 被监控主机 | Linux / macOS / Windows，能访问面板地址 |
| 网络 | 探针需要能**主动连出**到面板。面板不需要反向连探针 |
| 端口 | 面板默认监听 `25774`，通常由 nginx 或 Cloudflare 在前面终止 TLS |

!!! tip "探针是主动连出的"
    这很重要：被监控的机器**不需要开放任何入站端口**。探针经 HTTP 上报、经 WebSocket 保持长连接，两个方向都是它主动发起的。
