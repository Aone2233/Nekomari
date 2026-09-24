#!/usr/bin/env bash
# 生成 nginx 用的 Cloudflare 网段清单（geo 指令 include 的那个文件），并可选地把
# UFW 的 80/443 放行名单同步到同一份来源。
#
# 为什么需要它：vhost 只有在直连来源确实属于 Cloudflare 时才该采信 CF-Connecting-IP。
# 源站 443 对公网开放，无条件采信等于让任何人自选一个客户端 IP —— 而那个 IP 同时是
# 面板登录限流的 key，于是按 IP 的限流形同不存在。
#
# 网段来自 Cloudflare 官方公开列表，不是手抄的。IP 会变，所以用脚本生成、可以重复执行，
# 并在文件头写明抓取时间与来源，方便以后核对是不是过期了。
#
# 用法（源站上）：
#   sudo ./update-cloudflare-ips.sh              # 写入 /etc/nginx/cloudflare-ips.conf
#   sudo ./update-cloudflare-ips.sh --check      # 只比对，不写；有差异时退出码 1
#   sudo ./update-cloudflare-ips.sh --ufw        # 同时把 UFW 的 80/443 放行名单补齐
#   sudo ./update-cloudflare-ips.sh --ufw --check
#
# 关于 --ufw：它【只增不删】。缺的网段会被补上，多出来的（Cloudflare 已经不再公布的）
# 只打印出来并给出删除命令，由人决定 —— 静默删防火墙规则比留一条多余规则更危险。
set -euo pipefail

OUT="${OUT:-/etc/nginx/cloudflare-ips.conf}"
SOURCES=(
  "https://www.cloudflare.com/ips-v4"
  "https://www.cloudflare.com/ips-v6"
)
CHECK_ONLY=0
UFW_SYNC=0
for arg in "$@"; do
  case "$arg" in
    --check) CHECK_ONLY=1 ;;
    --ufw)   UFW_SYNC=1 ;;
    *) echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

{
  echo "# Cloudflare 网段 —— 由 deploy/update-cloudflare-ips.sh 生成，请勿手改。"
  echo "# 抓取时间: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  for url in "${SOURCES[@]}"; do
    echo "# 来源: $url"
    curl -fsS --max-time 30 "$url"
    # curl 的输出不保证以换行结尾（Cloudflare 的 ips-v4 就没有），少了这个 echo 会把
    # 下一段的注释拼到最后一个网段上，生成出 "131.0.72.0/22# 来源: … 1;" 这种行，
    # nginx 会以 "invalid number of the geo parameters" 拒绝加载。
    echo
  done
} > "$tmp"

# 逐行转成 geo 语法："<cidr> 1;"
# 空行与注释原样保留，便于人工核对。
{
  while IFS= read -r line; do
    case "$line" in
      ''|'#'*) echo "$line" ;;
      *)       printf '%s 1;\n' "$line" ;;
    esac
  done < "$tmp"
} > "${tmp}.conf"

count=$(grep -cE '^[0-9a-fA-F:.]+/[0-9]+ 1;$' "${tmp}.conf" || true)
if [[ "$count" -lt 10 ]]; then
  echo "只解析出 $count 条网段，明显不对（Cloudflare 公开列表有几十条），拒绝写入" >&2
  exit 1
fi

# 只取网段本身，供 UFW 同步使用。
grep -oE '^[0-9a-fA-F:.]+/[0-9]+' "${tmp}.conf" | sort -u > "${tmp}.cidrs"

# ufw_ports 与 vhost 里放行的端口保持一致。
ufw_ports="80,443"

# 把 UFW 的放行名单补齐到 "${tmp}.cidrs"。
#
# 为什么需要它：v4 与 v6 是两份独立规则。只补了一半的后果不是「少放行几个段」这么简单 ——
# Cloudflare 会用源站记录里【存在的那一族】回源，所以当源站只有 A 记录时缺 v6 规则不会
# 立刻出事，但一旦加上 AAAA，那些回源就会静默超时，表现成间歇 522，而源站日志里什么都
# 看不到（包在到达 nginx 之前就被默认拒绝策略丢了）。补齐比事后排查便宜。
sync_ufw() {
  if ! command -v ufw >/dev/null 2>&1; then
    echo "未安装 ufw，跳过防火墙同步" >&2
    return 0
  fi

  local current added=0 stale=0
  # ufw status 的行是 "80,443/tcp   ALLOW IN   <cidr>   # cloudflare"，
  # 所以不能锚定行首 —— 那样只会匹配到 "80,443/tcp"（且它本来就不匹配 CIDR 形态），
  # 结果是每个已配置的网段都被误报成缺失。不带锚点地取出网段即可。
  current="$(ufw status | grep -oE '[0-9a-fA-F:.]+/[0-9]+' | sort -u || true)"

  while IFS= read -r cidr; do
    [[ -z "$cidr" ]] && continue
    if grep -qx "$cidr" <<<"$current"; then
      continue
    fi
    if [[ "$CHECK_ONLY" == "1" ]]; then
      echo "  UFW 缺少: $cidr"
      added=$((added + 1))
      continue
    fi
    ufw allow proto tcp from "$cidr" to any port "$ufw_ports" comment 'cloudflare' >/dev/null
    added=$((added + 1))
  done < "${tmp}.cidrs"

  while IFS= read -r cidr; do
    [[ -z "$cidr" ]] && continue
    if ! grep -qx "$cidr" "${tmp}.cidrs"; then
      echo "  UFW 多出（Cloudflare 已不再公布，未自动删除）: $cidr"
      echo "    删除：sudo ufw delete allow proto tcp from $cidr to any port $ufw_ports"
      stale=$((stale + 1))
    fi
  done <<<"$current"

  if [[ "$CHECK_ONLY" == "1" ]]; then
    if [[ "$added" -gt 0 ]]; then
      echo "UFW 有 $added 条待补，请重新运行不带 --check 的版本" >&2
      return 1
    fi
    echo "UFW 放行名单已与 Cloudflare 公开列表一致"
    return 0
  fi

  echo "UFW 补齐 $added 条，多余 $stale 条"
  return 0
}

if [[ "$CHECK_ONLY" == "1" ]]; then
  rc=0
  if [[ -f "$OUT" ]] && diff -q <(grep -vE '^# 抓取时间' "$OUT") <(grep -vE '^# 抓取时间' "${tmp}.conf") >/dev/null; then
    echo "网段清单已是最新（$count 条）"
  else
    echo "网段清单有变化，请重新运行不带 --check 的版本（$count 条）" >&2
    rc=1
  fi
  if [[ "$UFW_SYNC" == "1" ]]; then
    sync_ufw || rc=1
  fi
  exit "$rc"
fi

install -m 0644 "${tmp}.conf" "$OUT"
echo "已写入 $OUT（$count 条网段）"

if command -v nginx >/dev/null 2>&1; then
  if nginx -t >/dev/null 2>&1; then
    echo "nginx -t 通过"
  else
    echo "nginx -t 失败，请检查后再 reload：" >&2
    nginx -t >&2 || true
    exit 1
  fi
fi

if [[ "$UFW_SYNC" == "1" ]]; then
  sync_ufw
fi
