#!/usr/bin/env bash
# 给 LuminaPlus 装上「流媒体 / AI 解锁」区块。
#
# 主题本身不渲染这个区块（1.3.3 与上游 1.3.4 都没有相关代码），所以这里挂一个附加
# 脚本，而不是去改主题的压缩产物 —— 压缩过的 React bundle 改起来极易出错，而且主题
# 一升级就失效。附加脚本只依赖主题已公开的 DOM 结构与它自己用的那个接口。
#
# 幂等：重复执行只会覆盖脚本本身，不会重复插入标签。
#
# 用法：
#   sudo ./install-theme-unlock-panel.sh                 # 默认 /opt/nekomari/data/theme/LuminaPlus/dist
#   THEME_DIR=/path/to/dist sudo -E ./install-theme-unlock-panel.sh
set -euo pipefail

THEME_DIR="${THEME_DIR:-/opt/nekomari/data/theme/LuminaPlus/dist}"
SRC="$(cd "$(dirname "$0")" && pwd)/unlock-panel.js"
ASSET_NAME="unlock-panel.js"
TAG='<script defer src="/assets/unlock-panel.js"></script>'

if [[ ! -f "$SRC" ]]; then
  echo "找不到 $SRC" >&2
  exit 1
fi
if [[ ! -f "$THEME_DIR/index.html" ]]; then
  echo "找不到 $THEME_DIR/index.html —— 主题装好了吗？" >&2
  exit 1
fi

echo "主题目录 : $THEME_DIR"

install -m 0644 "$SRC" "$THEME_DIR/assets/$ASSET_NAME"
echo "已写入   : assets/$ASSET_NAME"

if grep -qF "$ASSET_NAME" "$THEME_DIR/index.html"; then
  echo "index.html 已经引用过它，未改动"
else
  # 备份一次就够：第二次执行不会走到这里。
  if [[ ! -f "$THEME_DIR/index.html.bak-pre-unlock" ]]; then
    cp -p "$THEME_DIR/index.html" "$THEME_DIR/index.html.bak-pre-unlock"
    echo "已备份   : index.html.bak-pre-unlock"
  fi
  # 插到 </body> 之前。用 python 而不是 sed，避免斜杠和引号在替换串里被解释。
  python3 - "$THEME_DIR/index.html" "$TAG" <<'PY'
import sys
path, tag = sys.argv[1], sys.argv[2]
with open(path, encoding='utf-8') as handle:
    html = handle.read()
if tag in html:
    sys.exit(0)
marker = '</body>'
if marker not in html:
    sys.exit('index.html 里没有 </body>，拒绝盲插')
html = html.replace(marker, '    ' + tag + '\n  ' + marker, 1)
with open(path, 'w', encoding='utf-8') as handle:
    handle.write(html)
PY
  echo "已插入   : <script defer src=\"/assets/$ASSET_NAME\">"
fi

echo
echo "验证："
grep -o "<script defer src=\"/assets/$ASSET_NAME\"></script>" "$THEME_DIR/index.html" || {
  echo "  ✗ index.html 里没找到标签" >&2
  exit 1
}
ls -l "$THEME_DIR/assets/$ASSET_NAME"
echo
echo "浏览器强制刷新一次即可看到「流媒体 / AI 解锁」区块（主题资源带哈希，index.html 不带）。"
