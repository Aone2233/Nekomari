#!/usr/bin/env bash
# 把节点上的探针升到指定版本，并确保升级之后服务真的在跑新二进制。
#
# 为什么必须升级探针：v0.1.9 修掉了「已完成的 TCP 握手被报成丢包」，并加入了流媒体/AI
# 解锁探测。两者都只在探针里，服务端升级不解决。
#
# 幂等：优先用探针自己的 --version 判断（新版本有，老版本没有这个 flag），拿不到时回退到
# 与已发布的 SHA256SUMS.txt 比对哈希。已经是目标版本就直接跳过，不下载。
#
# 升级一次做这些事（每一步失败都停在那一步，旧二进制还在）：
#   1. 从正在跑的进程取二进制路径（不猜目录）；BIN= 可直接指定
#   2. --version / 哈希 判断是否已经是目标版本
#   3. 下载 → 校验 SHA256 → 先运行一次新二进制，确认它在这台机器上能跑
#   4. 备份旧二进制（<bin>.bak-<version>-<ts>），替换
#   5. 恢复文件能力（非 root 节点靠 setcap cap_net_raw+ep 发 ICMP；install 会丢掉扩展属性）
#   6. 按 init 系统重启（systemd / OpenRC；OpenRC 会先 zap 掉 crashed 状态再 start）
#   7. 确认跑起来的是新二进制（比对 /proc/<pid>/exe 与文件的 inode），并只保留最新备份
#
# 用法（在目标机上以 root 执行）：
#   VERSION=v1.6.20 ./upgrade-agent.sh
#   ./upgrade-agent.sh --version v1.6.20
#   ./upgrade-agent.sh v1.6.20                  # 位置参数也认
#   ./upgrade-agent.sh --version v1.6.20 --dry-run
#
# 环境变量：
#   VERSION       目标版本（--version/位置参数优先；默认取最新 release）
#   REPO          默认 Aone2233/Nekomari
#   BIN           指定要升级的二进制（默认从正在运行的进程取）
#   RESTART_CMD   自定义重启命令（用户级 systemd 单元、容器里的自定义 supervisor 等）
#   KEEP_BACKUPS  保留几份旧二进制，默认 1
#   NEKOMARI_INIT       强制 systemd / openrc（默认自动判断）
#   NEKOMARI_INIT_DIR   OpenRC 的 init 脚本目录，默认 /etc/init.d
#
# 退出码：0 已升级或本来就在目标版本（旧脚本一直如此），1 失败。
set -euo pipefail

REPO="${REPO:-Aone2233/Nekomari}"
KEEP_BACKUPS="${KEEP_BACKUPS:-1}"
INIT_DIR="${NEKOMARI_INIT_DIR:-/etc/init.d}"
TARGET="${VERSION:-}"
DRY_RUN=0
BIN_OVERRIDE="${BIN:-}"

log()  { printf '  %s\n' "$*"; }
warn() { printf '  !! %s\n' "$*" >&2; }
die()  { printf '  !! %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version)   TARGET="${2:?--version needs a value}"; shift 2 ;;
    --version=*) TARGET="${1#*=}"; shift ;;
    --dry-run)   DRY_RUN=1; shift ;;
    -h|--help)   sed -n '2,34p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*)          die "unknown argument: $1 (see --help)" ;;
    *)           TARGET="$1"; shift ;;
  esac
done

case "$(uname -s)" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "不支持的系统：$(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "不支持的架构：$(uname -m)" ;;
esac
ASSET="komari-agent-${OS}-${ARCH}"

TMP="$(mktemp -d)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

# --- 1. 哪个进程、哪个二进制 -------------------------------------------------
#
# 从正在运行的进程取路径，而不是猜安装目录：各节点的安装位置并不统一。以 root 运行时
# /proc/<pid>/exe 是权威答案；读不到（非 root）时退回 /proc/<pid>/cmdline 与 ps 的参数。
# 注意 /proc/<pid>/exe 对已被替换的文件会显示 "<path> (deleted)"，所以匹配时两种写法
# 都认 —— 升级后旧进程还在跑的那一刻正是这个形状。
exe_of_pid() {
  local p="$1" exe=""
  exe=$(readlink "/proc/$p/exe" 2>/dev/null || true)
  if [ -z "$exe" ] && [ -r "/proc/$p/cmdline" ]; then
    exe=$(tr '\0' '\n' < "/proc/$p/cmdline" 2>/dev/null | head -1 || true)
  fi
  printf '%s' "$exe"
}

discover_bin() {
  local p exe
  for p in $(ps -eo pid= 2>/dev/null); do
    exe=$(exe_of_pid "$p")
    case "$exe" in *komari-agent*) printf '%s' "${exe% (deleted)}"; return 0 ;; esac
  done
  exe=$(ps -eo args= 2>/dev/null | grep '[k]omari-agent' | head -1 | awk '{print $1}' || true)
  case "$exe" in *komari-agent*) printf '%s' "$exe"; return 0 ;; esac
  return 1
}

if [ -n "$BIN_OVERRIDE" ]; then
  BIN="$BIN_OVERRIDE"
  log "二进制：$BIN（BIN= 指定）"
else
  BIN="$(discover_bin || true)"
  [ -n "$BIN" ] || die "找不到正在运行的探针二进制；用 BIN=/path/to/komari-agent 指定"
  log "二进制：$BIN（从运行中的进程取得）"
fi
[ -x "$BIN" ] || die "$BIN 不存在或不可执行"

# 进程 pid 用来确认「重启之后跑的是新文件」，没有也能继续：只是少一条证据。
AGENT_PID=""
for p in $(ps -eo pid= 2>/dev/null); do
  exe=$(exe_of_pid "$p")
  case "$exe" in
    "$BIN"|"$BIN (deleted)") AGENT_PID="$p"; break ;;
  esac
done

# --- 2. 版本与幂等 ----------------------------------------------------------
#
# 探针有 --version（agent/cmd/root.go 的 printVersion）：它打印 update.CurrentVersion，
# 也就是 release 打的 tag。老版本没有这个 flag（实测报 unknown flag: --version），那种
# 节点只能比哈希 —— binary_version 返回空串时就是这条路。
binary_version() {
  local bin="$1" out=""
  [ -x "$bin" ] || return 1
  out=$("$bin" --version 2>/dev/null) || return 1
  case "$out" in
    *"unknown flag"*|"") return 1 ;;
  esac
  printf '%s' "$out" | head -1 | tr -d '[:space:]'
}

CURRENT_VERSION="$(binary_version "$BIN" || true)"
if [ -n "$CURRENT_VERSION" ]; then
  log "当前版本：$CURRENT_VERSION（--version）"
else
  log "当前版本：未知（这个版本的探针没有 --version，改用哈希判断）"
fi

if [ -z "$TARGET" ]; then
  TARGET="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
            | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1 || true)"
  [ -n "$TARGET" ] || die "拿不到最新 release 的 tag；用 --version vX.Y.Z 指定"
fi
log "目标版本：$TARGET"

if [ -n "$CURRENT_VERSION" ] && [ "$CURRENT_VERSION" = "$TARGET" ]; then
  log "已经是 $TARGET，跳过"
  exit 0
fi

BASE="https://github.com/${REPO}/releases/download/${TARGET}"

# --- 3. 文件能力（替换前先记下来） ------------------------------------------
#
# 以非 root 运行的探针靠 setcap cap_net_raw+ep 才能发 ICMP，而这个能力存在【文件】的
# 扩展属性里 —— install 覆盖文件会把它一起丢掉。实测就是这样把 MAC Server 的 ICMP
# 打坏的：升级完三张 ICMP 任务全部报 "socket: operation not permitted"，面板上显示成
# 15.2% 丢包。以 root 运行的节点不需要它，所以只有部分节点会中招。
CAPS=""
if command -v getcap >/dev/null 2>&1; then
  CAPS="$(getcap "$BIN" 2>/dev/null | awk '{ $1=""; print $0 }' | sed 's/^ //' || true)"
fi
if [ -n "$CAPS" ]; then
  log "文件能力：$CAPS（替换后恢复）"
else
  log "文件能力：无"
fi

# --- 4. 重启方式（先判断，dry-run 才能如实报告） ----------------------------
#
# systemd 节点按 MainPID 找到拥有该进程的单元，找到哪个就重启哪个，不猜名字。名字兜底
# 仍然保留：Type=forking 或 supervise 形态下 MainPID 可能是另一个 pid。
#
# OpenRC 节点（Alpine/LXC）用 init 脚本：从 $INIT_DIR 里找 command= 指向这个二进制的那个
# 脚本。NEKOMARI_INIT / NEKOMARI_INIT_DIR 可以强制指定，主要给离线测试用。
SERVICE_SHAPE=""
SERVICE_NAME=""

find_systemd_unit() {
  command -v systemctl >/dev/null 2>&1 || return 1
  local unit pid_of
  for unit in $(systemctl list-units --type=service --all --no-legend 2>/dev/null | awk '{print $1}'); do
    pid_of=$(systemctl show -p MainPID --value "$unit" 2>/dev/null || true)
    if [ -n "$AGENT_PID" ] && [ "$pid_of" = "$AGENT_PID" ]; then printf '%s' "$unit"; return 0; fi
  done
  unit=$(systemctl list-units --type=service --all --no-legend 2>/dev/null \
         | awk '{print $1}' | grep -i komari | head -1 || true)
  [ -n "$unit" ] && { printf '%s' "$unit"; return 0; }
  return 1
}

find_openrc_service() {
  local script name
  [ -d "$INIT_DIR" ] || return 1
  for script in "$INIT_DIR"/*; do
    [ -f "$script" ] || continue
    grep -qF -- "$(basename "$BIN")" "$script" 2>/dev/null || continue
    name=$(basename "$script")
    printf '%s' "$name"
    return 0
  done
  return 1
}

case "${NEKOMARI_INIT:-}" in
  systemd)
    SERVICE_NAME="$(find_systemd_unit || true)"
    [ -n "$SERVICE_NAME" ] || die "NEKOMARI_INIT=systemd，但没有任何单元报告拥有 pid ${AGENT_PID:-?}"
    SERVICE_SHAPE=systemd ;;
  openrc)
    SERVICE_NAME="$(find_openrc_service || true)"
    [ -n "$SERVICE_NAME" ] || die "NEKOMARI_INIT=openrc，但 $INIT_DIR 里没有脚本引用 $(basename "$BIN")"
    SERVICE_SHAPE=openrc ;;
  "")
    if [ "$OS" = "linux" ]; then
      if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
        SERVICE_NAME="$(find_systemd_unit || true)"
        [ -n "$SERVICE_NAME" ] && SERVICE_SHAPE=systemd
      fi
      if [ -z "$SERVICE_SHAPE" ]; then
        SERVICE_NAME="$(find_openrc_service || true)"
        [ -n "$SERVICE_NAME" ] && SERVICE_SHAPE=openrc
      fi
    fi ;;
  *)
    die "NEKOMARI_INIT 只能是 systemd 或 openrc（现在是 '${NEKOMARI_INIT}'）" ;;
esac

if [ -n "$SERVICE_SHAPE" ]; then
  log "服务：${SERVICE_SHAPE} ${SERVICE_NAME}"
elif [ -n "${RESTART_CMD:-}" ]; then
  log "服务：用 RESTART_CMD 重启"
elif [ "$OS" = "linux" ]; then
  warn "找不到负责这个进程的服务（systemd 单元 / OpenRC init 脚本都没有）"
  warn "升级可以继续，但不会自动重启；用 RESTART_CMD='...' 指定重启方式"
else
  log "服务：macOS 上不猜 launchd 标签，升级后需要手动重启"
fi

# --- 5. dry run：把计划说清楚，什么都不改 -----------------------------------
BACKUP_NAME="${BIN}.bak-${TARGET}-$(date -u +%Y%m%dT%H%M%SZ)"
if [ "$DRY_RUN" = "1" ]; then
  log "dry run：计划"
  log "  下载：${BASE}/${ASSET}"
  log "  备份：${BACKUP_NAME}"
  log "  替换：${BIN}"
  [ -n "$CAPS" ] && log "  恢复能力：${CAPS}"
  log "  重启：${SERVICE_SHAPE:-<未知>} ${SERVICE_NAME:-}"
  log "  保留备份：${KEEP_BACKUPS} 份（旧的 *.bak-* / *.pre-v* 会被删掉）"
  log "没有下载、没有替换、没有重启。"
  exit 0
fi

# --- 6. 下载、校验、先在原地跑一次新二进制 ----------------------------------
cd "$TMP"
log "下载 ${ASSET}"
curl -fsSL -o "$ASSET" "${BASE}/${ASSET}" || die "下载失败：${BASE}/${ASSET}"
curl -fsSL -o SHA256SUMS.txt "${BASE}/SHA256SUMS.txt" || die "拿不到 SHA256SUMS.txt"

WANT="$(grep " ${ASSET}\$" SHA256SUMS.txt | awk '{print $1}' || true)"
[ -n "$WANT" ] || die "SHA256SUMS.txt 里没有 ${ASSET}"

# 哈希兜底：没有 --version 的老探针只能这样判断「已经是目标版本」。放在下载之后是因为
# 期望哈希来自已发布的清单；下载一次几十秒，比把一台节点升错版本便宜。
HAVE="$( (sha256sum "$BIN" 2>/dev/null || shasum -a 256 "$BIN") | awk '{print $1}')"
if [ "$HAVE" = "$WANT" ]; then
  log "二进制哈希与 ${TARGET} 一致，已经是目标版本，跳过"
  exit 0
fi

grep " ${ASSET}\$" SHA256SUMS.txt > want.txt
(sha256sum -c want.txt 2>/dev/null || shasum -a 256 -c want.txt) >/dev/null \
  || die "下载到的 ${ASSET} 校验和不匹配"
chmod +x "$ASSET"
log "校验和通过"

# 替换之前先确认新二进制在这台机器上能跑：架构或 libc 不匹配的构建如果直接覆盖上去，
# 服务会进入重启循环（OpenRC 上是 crashed），而旧二进制已经不在了。
PROBE_VERSION="$("$TMP/$ASSET" --version 2>/dev/null | head -1 | tr -d '[:space:]' || true)"
if [ -z "$PROBE_VERSION" ]; then
  "$TMP/$ASSET" --help >/dev/null 2>&1 || die "新二进制在这台机器上跑不起来（架构/libc 不匹配）；旧二进制未改动"
  log "新二进制可以执行（没有 --version，用 --help 验证）"
else
  log "新二进制可以执行：--version ${PROBE_VERSION}"
fi

# --- 7. 备份、替换、恢复能力 ------------------------------------------------
cp -p "$BIN" "$BACKUP_NAME" || die "备份 $BIN 失败"
log "备份：$BACKUP_NAME"
install -m 0755 "$TMP/$ASSET" "$BIN" || die "替换 $BIN 失败"
log "已替换"

# 恢复文件能力。这一步不能省：少了它，非 root 节点会静默失去 ICMP 能力，
# 表现为面板上的「丢包」而不是报错。
if [ -n "$CAPS" ]; then
  if command -v setcap >/dev/null 2>&1; then
    if setcap "$CAPS" "$BIN"; then
      log "已恢复文件能力：$(getcap "$BIN" 2>/dev/null || true)"
    else
      warn "恢复文件能力失败：$CAPS"
      warn "请以 root 执行： setcap $CAPS $BIN"
    fi
  else
    warn "该节点需要文件能力 $CAPS，但没有 setcap，请手动执行："
    warn "  sudo setcap $CAPS $BIN"
  fi
fi

# --- 8. 重启 ----------------------------------------------------------------
#
# OpenRC 会记住上一次运行的结局：agent 以非零码退出后服务处于 "crashed"，这时
# rc-service restart 只会再失败一次、节点仍然离线。zap 清掉那个记录再 start，
# 这就是「升级之后不留 crashed 服务」的那一步。
restart_service() {
  if [ -n "${RESTART_CMD:-}" ]; then
    log "重启：$RESTART_CMD"
    eval "$RESTART_CMD"
    return
  fi
  case "$SERVICE_SHAPE" in
    systemd) systemctl restart "$SERVICE_NAME" ;;
    openrc)
      if rc-service "$SERVICE_NAME" status >/dev/null 2>&1; then
        rc-service "$SERVICE_NAME" restart
      else
        rc-service "$SERVICE_NAME" zap >/dev/null 2>&1 || true
        rc-service "$SERVICE_NAME" start
      fi ;;
    *) return 1 ;;
  esac
}

if restart_service; then
  sleep 6
  case "$SERVICE_SHAPE" in
    systemd) STATE="$(systemctl is-active "$SERVICE_NAME" 2>/dev/null || echo unknown)" ;;
    openrc)  STATE="$(rc-service "$SERVICE_NAME" status 2>/dev/null | head -1 || echo unknown)" ;;
    *)       STATE="(未重启)" ;;
  esac
  log "服务状态：$STATE"
elif [ -n "${RESTART_CMD:-}" ]; then
  die "重启命令失败"
elif [ "$OS" = "linux" ]; then
  die "没能重启服务；旧二进制在 $BACKUP_NAME，恢复后手动重启（或下一次用 RESTART_CMD=...）"
else
  log "macOS：请手动重启探针（launchctl）"
fi

# --- 9. 证据：跑起来的确实是新二进制 ----------------------------------------
#
# 只看 systemctl is-active 不够：旧进程可能还活着（替换文件不会杀掉它），服务显示
# active，而面板上的版本没变。文件的 inode 与 /proc/<pid>/exe 的 inode 相同，才说明这个
# 进程映射的是刚刚替换上去的文件。
if [ "$OS" = "linux" ] && [ -d /proc ]; then
  RUN_PID=""
  for p in $(ps -eo pid= 2>/dev/null); do
    exe=$(exe_of_pid "$p")
    case "$exe" in
      "$BIN") RUN_PID="$p"; break ;;
      "$BIN (deleted)") RUN_PID="$p"; break ;;
    esac
  done
  if [ -z "$RUN_PID" ]; then
    warn "没有找到正在运行的探针进程，升级后服务可能没起来"
  else
    RUN_INODE=$(stat -Lc '%i' "/proc/$RUN_PID/exe" 2>/dev/null || true)
    FILE_INODE=$(stat -c '%i' "$BIN" 2>/dev/null || true)
    if [ -n "$RUN_INODE" ] && [ "$RUN_INODE" = "$FILE_INODE" ]; then
      log "运行中的进程（pid $RUN_PID）跑的就是新二进制（inode $FILE_INODE）"
    else
      warn "运行中的进程（pid $RUN_PID，inode ${RUN_INODE:-?}）仍在跑旧二进制（inode ${FILE_INODE:-?}）"
      warn "重启没有生效：请手动重启服务，或确认 RESTART_CMD 指向的是这个服务"
    fi
  fi
fi

# --- 10. 只保留最新备份 -----------------------------------------------------
#
# 每次升级都留一份旧二进制（几十 MB），几个版本之后就占满小节点的磁盘。三种命名都算
# 备份：本脚本的 .bak-<version>-<ts>、旧脚本的 .bak-pre-<version>、fleet 脚本的
# .pre-<version>-<ts>。失败只警告，不影响这次升级已经成功这件事。
prune_backups() { # <the backup this run just made>
  local keep path extra count=0
  local backups=()
  keep="$KEEP_BACKUPS"
  case "$keep" in ''|*[!0-9]*) warn "KEEP_BACKUPS 不是数字，按 1 处理"; keep=1 ;; esac
  [ "$keep" -ge 1 ] || keep=1
  path="$1"

  # This run's backup is kept first, not ranked by mtime: cp -p preserves the replaced
  # binary's timestamps, so "newest by mtime" can point at an older backup whose
  # timestamps happen to be later. That is exactly the shape that would delete the only
  # backup of the running upgrade.
  log "保留备份：$(basename "$path")"
  count=1

  shopt -s nullglob
  backups=( "$BIN".bak-* "$BIN".pre-v* )
  shopt -u nullglob
  [ "${#backups[@]}" -gt 0 ] || return 0
  # Everything else, newest first, up to the limit.
  while IFS= read -r extra; do
    [ -n "$extra" ] || continue
    [ "$extra" = "$path" ] && continue
    count=$((count + 1))
    if [ "$count" -le "$keep" ]; then
      log "保留备份：$(basename "$extra")"
      continue
    fi
    rm -f "$extra" && log "删除旧备份：$(basename "$extra")"
  done <<PRUNE_EOF
$(ls -1t "${backups[@]}" 2>/dev/null || true)
PRUNE_EOF
}
prune_backups "$BACKUP_NAME" || warn "清理旧备份时出错，旧备份仍在磁盘上"

log "完成：${TARGET}（服务 ${SERVICE_SHAPE:-?} ${SERVICE_NAME:-}）"
