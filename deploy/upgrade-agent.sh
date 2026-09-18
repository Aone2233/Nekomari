#!/usr/bin/env bash
# 把节点上的探针升到指定版本。默认 v0.1.9。
#
# 为什么必须升级探针：v0.1.9 修掉了「已完成的 TCP 握手被报成丢包」，并加入了流媒体/AI
# 解锁探测。两者都只在探针里，服务端升级不解决。
#
# 幂等：已经是目标版本就直接跳过。会先备份旧二进制（*.bak-pre-<version>）。
#
# 用法（在目标机上以 root 执行）：
#   VERSION=v0.1.9 ./upgrade-agent.sh
set -euo pipefail

VERSION="${VERSION:-v0.1.9}"
REPO="${REPO:-Aone2233/Nekomari}"
BASE="https://github.com/${REPO}/releases/download/${VERSION}"

case "$(uname -s)" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) echo "不支持的系统：$(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "不支持的架构：$(uname -m)" >&2; exit 1 ;;
esac

ASSET="komari-agent-${OS}-${ARCH}"
echo "目标：${ASSET}  ${VERSION}"

# 从正在跑的进程取二进制路径，而不是猜目录 —— 各节点的安装位置并不统一。
BIN="$(ps -eo args | grep '[k]omari-agent' | head -1 | awk '{print $1}')"
if [[ -z "$BIN" || ! -x "$BIN" ]]; then
  echo "找不到正在运行的探针二进制" >&2
  exit 1
fi
echo "当前二进制：$BIN"

# 先记下文件能力，替换之后要恢复。
#
# 以非 root 运行的探针靠 `setcap cap_net_raw+ep` 才能发 ICMP，而这个能力是存在【文件】
# 上的扩展属性里的 —— `install` 覆盖文件会把它一起丢掉。实测就是这样把 MAC Server 的
# ICMP 打坏的：升级完三张 ICMP 任务全部报 "socket: operation not permitted"，
# 面板上显示成 15.2% 丢包。以 root 运行的节点不需要它，所以只有部分节点会中招。
CAPS="$(getcap "$BIN" 2>/dev/null | awk '{$1=""; print $0}' | sed 's/^ //')"
if [[ -n "$CAPS" ]]; then
  echo "文件能力：$CAPS（替换后需恢复）"
else
  echo "文件能力：无"
fi

CURRENT="$("$BIN" --version 2>/dev/null | tr -d '\r' | head -1 || true)"
echo "当前版本：${CURRENT:-未知}"
if [[ "$CURRENT" == *"$VERSION"* ]]; then
  echo "已经是 $VERSION，跳过"
  exit 0
fi

cd /tmp
curl -fsSL -o agent-new "$BASE/$ASSET"
curl -fsSL -o sums "$BASE/SHA256SUMS.txt"
grep " ${ASSET}\$" sums | sed "s#${ASSET}#agent-new#" | sha256sum -c -
chmod +x agent-new

cp -p "$BIN" "${BIN}.bak-pre-${VERSION}"
install -m 0755 agent-new "$BIN"
echo "已替换，备份在 ${BIN}.bak-pre-${VERSION}"

# 恢复文件能力。这一步不能省：少了它，非 root 节点会静默失去 ICMP 能力，
# 表现为面板上的「丢包」而不是报错。
if [[ -n "$CAPS" ]]; then
  if command -v setcap >/dev/null 2>&1; then
    setcap "$CAPS" "$BIN" && echo "已恢复文件能力：$(getcap "$BIN")"
  else
    echo "警告：该节点需要文件能力 $CAPS，但没有 setcap，请手动执行：" >&2
    echo "  sudo setcap $CAPS $BIN" >&2
  fi
fi

# 重启方式。
#
# 默认自己找 system 级单元。但有的节点跑的是【用户级】单元（MAC Server 就是），
# `systemctl --user` 以 root 执行会报 "Failed to connect to bus"，必须带上正确的
# XDG_RUNTIME_DIR 和目标用户。这里不猜这些参数，要求显式给出：
#
#   RESTART_CMD='sudo -n -u macos env XDG_RUNTIME_DIR=/run/user/1000 systemctl --user restart nekomari-agent' \
#     VERSION=v0.1.9 ./upgrade-agent.sh
if [[ -n "${RESTART_CMD:-}" ]]; then
  echo "重启：$RESTART_CMD"
  eval "$RESTART_CMD"
  sleep 6
  echo "重启命令已执行"
elif [[ "$OS" == "linux" ]]; then
  UNIT="$(systemctl list-units --type=service --all --no-legend 2>/dev/null | awk '{print $1}' | grep -i komari | head -1)"
  if [[ -z "$UNIT" ]]; then
    echo "找不到 system 级的 komari 单元。若该节点跑的是用户级单元，请用 RESTART_CMD 指定重启方式。" >&2
    exit 1
  fi
  systemctl restart "$UNIT"
  sleep 6
  echo "单元 $UNIT : $(systemctl is-active "$UNIT")"
  journalctl -u "$UNIT" --since "1 min ago" --no-pager 2>/dev/null | grep -iE "unlock probe|Basic info uploaded" | tail -3 || true
else
  echo "macOS：请手动重启探针（launchctl），脚本不猜测它的标签名"
fi
