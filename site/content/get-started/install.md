# 安装面板

## Docker（推荐）

```bash
docker run -d --name nekomari \
  -p 25774:25774 \
  -v nekomari-data:/app/data \
  ghcr.io/aone2233/nekomari:latest
```

多架构（`linux/amd64`、`linux/arm64`），可匿名拉取。镜像里的二进制与 Release 附件是同一个。

!!! danger "务必加 `-v`"
    镜像声明了 `VOLUME /app/data`。不加 `-v` 时 Docker 会创建一个**匿名卷** —— 升级就是重建容器，而新容器会挂到**另一个**匿名卷上，于是面板带着空数据目录启动、又出现安装向导。

    数据没被删除，只是被孤立了，但从外部看**和"升级把配置重置了"一模一样**。

    命名卷（`-v nekomari-data:/app/data`）或绑定挂载（`-v /opt/nekomari/data:/app/data`）都可以。检测到匿名卷时服务端会在启动日志里告警。

### 用 Compose

```yaml title="docker-compose.yml"
services:
  nekomari:
    image: ghcr.io/aone2233/nekomari:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:25774:25774"   # 只监听回环，前面放反向代理
    volumes:
      - ./data:/app/data
    environment:
      TZ: Asia/Shanghai
```

!!! warning "反向代理必须透传 Upgrade 头"
    探针持有长连接 WebSocket。代理不透传 `Upgrade`/`Connection`，或读超时太短（默认 60s），连接会被掐断，表现为**面板上节点反复上下线**。

    可用的配置见仓库的 `deploy/nginx-nekomari.conf`，以及本站的[部署指南](../guides/deployment.md)。

## 预编译二进制

从 [Releases](https://github.com/Aone2233/Nekomari/releases) 下载。每个版本附带服务端与探针，覆盖 `linux/amd64`、`linux/arm64`、`windows/amd64`，以及 `SHA256SUMS.txt`。

```bash
./nekomari-linux-amd64 server     # 然后打开 http://localhost:25774 完成安装
```

```bash
./komari-agent-linux-amd64 -e https://your-panel -t <token>
```

!!! note "探针的文件名为什么保留 `komari-agent-` 前缀"
    那是它自更新时查找的资产名，属于发布契约。改名会让升级链路失效。

## 从源码构建

需要 Go（版本见 `go.mod`）、Node 22+，以及 C 工具链 —— 服务端通过 CGO 链接 SQLite，所以 `CGO_ENABLED=0` 构建不出来。

```bash
./build.sh          # 前端 -> 内嵌主题 -> 服务端 -> 探针
```

`build.sh` 会跑完全部步骤（含主题打包），不需要外部 `zstd` 命令。

## 装完之后

第一次访问会进入安装向导，需要设置管理员账号。装完继续看[首次配置](quick-start.md)。
