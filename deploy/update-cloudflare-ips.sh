#!/usr/bin/env bash
# 生成 nginx 用的 Cloudflare 网段清单（geo 指令 include 的那个文件）。
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
set -euo pipefail

OUT="${OUT:-/etc/nginx/cloudflare-ips.conf}"
SOURCES=(
  "https://www.cloudflare.com/ips-v4"
  "https://www.cloudflare.com/ips-v6"
)
CHECK_ONLY=0
[[ "${1:-}" == "--check" ]] && CHECK_ONLY=1

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

if [[ "$CHECK_ONLY" == "1" ]]; then
  if [[ -f "$OUT" ]] && diff -q <(grep -vE '^# 抓取时间' "$OUT") <(grep -vE '^# 抓取时间' "${tmp}.conf") >/dev/null; then
    echo "网段清单已是最新（$count 条）"
    exit 0
  fi
  echo "网段清单有变化，请重新运行不带 --check 的版本（$count 条）" >&2
  exit 1
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
