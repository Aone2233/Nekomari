FROM alpine:3.21

WORKDIR /app

# Docker buildx 会在构建时自动填充这两个变量（TARGETOS=linux, TARGETARCH=amd64/arm64）。
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache ca-certificates curl tzdata

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
