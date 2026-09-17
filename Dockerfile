# 基础镜像必须是 glibc 系（Debian），不能用 alpine。
#
# 原因：服务端依赖 CGO（mattn/go-sqlite3），而 release.yml 是在
# ubuntu-latest 上原生构建的（CGO_ENABLED=1，系统 gcc），产出的二进制
# 动态链接 glibc，程序解释器是 /lib64/ld-linux-x86-64.so.2。
# 把它放进 alpine（musl）会以 `exec /app/nekomari: no such file or directory`
# 启动失败 —— 这个报错说的是【找不到程序解释器】，不是文件不存在。
#
# 上游之所以能用 alpine，是因为它用 zig cc 交叉编译出的是 musl 二进制；
# 本仓库改用「各平台原生构建」后这个前提就不成立了。
FROM debian:bookworm-slim

WORKDIR /app

# Docker buildx 会在构建时自动填充这两个变量（TARGETOS=linux, TARGETARCH=amd64/arm64）。
ARG TARGETOS
ARG TARGETARCH

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
 && rm -rf /var/lib/apt/lists/*

# 依赖发布产物已放在构建上下文根目录，命名形如 nekomari-linux-amd64。
# 注意：改名后这里必须用 nekomari- 前缀 —— 发布流水线产出的是 nekomari-<os>-<arch>。
COPY --chmod=755 nekomari-${TARGETOS}-${TARGETARCH} /app/nekomari

ENV GIN_MODE=release
# KOMARI_LISTEN 特意保留旧名：它是启动配置的契约，改名会让既有的
# compose / systemd / 脚本全部失效，而它并不会显示给最终用户。
ENV KOMARI_LISTEN=0.0.0.0:25774

# 主数据库与指标库默认都落在 /app/data，挂卷即可持久化。
VOLUME ["/app/data"]

EXPOSE 25774

CMD ["/app/nekomari", "server"]