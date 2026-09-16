#!/bin/bash
# Linux 开发机辅助脚本 —— 把构建 / 测试 / 可行性验证类任务放到 Linux 机器上跑
#
# 为什么需要它：
#   - 本项目的服务端依赖 CGO（sqlite 驱动），在 Windows 上交叉编译 Linux 版会失败；
#   - 很多验证（真实 ICMP 探测、Linux 专有行为）必须在 Linux 上做；
#   - 目标机通常没有 GitHub 凭据（仓库可能是私有的），所以用 tar+scp 同步工作树，
#     而不是在远端 git clone。
#
# 默认目标机可用环境变量覆盖：
#   NEKOMARI_LINUX_HOST   默认 MAC-WAN
#   NEKOMARI_LINUX_DIR    默认 ~/nekomari
#
# 用法：
#   scripts/linux-dev.sh sync                      # 同步当前工作树到目标机
#   scripts/linux-dev.sh build                     # 在目标机执行 build.sh
#   scripts/linux-dev.sh run  go test ./...        # 在目标机仓库目录执行任意命令
#   scripts/linux-dev.sh netcheck 1.1.1.1          # 用目标机的二进制做可达性自检(需 root 才有 ICMP)
#   scripts/linux-dev.sh log                        # 查看最近一次后台构建日志

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="${NEKOMARI_LINUX_HOST:-MAC-WAN}"
RDIR="${NEKOMARI_LINUX_DIR:-\$HOME/nekomari}"       # 远端路径（允许 $HOME 由远端展开）
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=15)

GO_ENV='export PATH=$HOME/go-toolchain/go/bin:$PATH; export GOPROXY=${GOPROXY:-https://goproxy.cn,direct}; export GOSUMDB=${GOSUMDB:-sum.golang.google.cn};'

die() { echo "错误: $*" >&2; exit 1; }

cmd_sync() {
  local tmp
  tmp="$(mktemp -t nekomari-sync-XXXXXX.tar.gz)"
  echo "==> 打包工作树（排除 .git / node_modules / dist / 二进制）"
  tar -czf "$tmp" -C "$ROOT" \
    --exclude='.git' --exclude='node_modules' --exclude='dist' \
    --exclude='*.exe' --exclude='*.tar.zst' --exclude='.runtest' \
    --exclude='data' . 2>/dev/null
  echo "    包大小: $(du -h "$tmp" | cut -f1)"
  echo "==> 同步到 $HOST"
  ssh "${SSH_OPTS[@]}" "$HOST" "mkdir -p $RDIR"
  scp "${SSH_OPTS[@]}" "$tmp" "$HOST:/tmp/nekomari-sync.tar.gz" >/dev/null
  ssh "${SSH_OPTS[@]}" "$HOST" "tar -C $RDIR -xzf /tmp/nekomari-sync.tar.gz && rm -f /tmp/nekomari-sync.tar.gz && echo '    远端文件数:' \$(find $RDIR -type f | wc -l)"
  rm -f "$tmp"
}

cmd_build() {
  echo "==> 在 $HOST 执行 build.sh（后台，日志 /tmp/nekomari-build.log）"
  ssh "${SSH_OPTS[@]}" "$HOST" "cd $RDIR && $GO_ENV nohup bash build.sh > /tmp/nekomari-build.log 2>&1 & echo '    已启动'"
  echo "    查看进度: $0 log"
}

cmd_run() {
  [ $# -gt 0 ] || die "run 需要命令，例如: $0 run go test ./..."
  echo "==> $HOST: $*"
  ssh "${SSH_OPTS[@]}" "$HOST" "cd $RDIR && $GO_ENV $*"
}

cmd_netcheck() {
  local target="${1:-1.1.1.1}"
  echo "==> 在 $HOST 上对 $target 做可达性自检"
  # ICMP 需要 root；SUDO_PASSWORD 从 ~/.hermes/.env 取（目标机的既有约定）
  ssh "${SSH_OPTS[@]}" "$HOST" '
    cd '"$RDIR"'
    PW=$(grep -m1 "^SUDO_PASSWORD=" ~/.hermes/.env 2>/dev/null | cut -d= -f2-)
    if [ -x ./nekomari ] && [ -n "$PW" ]; then
      printf "%s\n" "$PW" | sudo -S -p "" ./nekomari netcheck '"$target"' 2>&1 | grep -vE "^2026/|INFO/SERVER"
    elif [ -x ./nekomari ]; then
      ./nekomari netcheck '"$target"' 2>&1 | grep -vE "^2026/|INFO/SERVER"
    else
      echo "  远端还没有 ./nekomari，先执行: $0 build"
    fi'
}

cmd_log() {
  ssh "${SSH_OPTS[@]}" "$HOST" "tail -40 /tmp/nekomari-build.log"
}

case "${1:-}" in
  sync)     cmd_sync ;;
  build)    shift; cmd_build "$@" ;;
  run)      shift; cmd_run "$@" ;;
  netcheck) shift; cmd_netcheck "$@" ;;
  log)      cmd_log ;;
  *) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//' ;;
esac
