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

# 标签必须带内容指纹。
#
# 这个文件名是固定的（不像主题自己的资源带哈希），浏览器会一直用缓存里那份，改完脚本
# 刷新页面看到的还是旧的 —— 第一次改渲染逻辑时就撞上了这个，页面照旧显示旧文案。
# 所以把脚本内容的前 8 位 sha256 拼进查询串：内容一变 URL 就变，缓存自然失效。
HASH="$(sha256sum "$SRC" | cut -c1-8)"
TAG="<script defer src=\"/assets/$ASSET_NAME?v=$HASH\"></script>"
echo "指纹     : v=$HASH"

if [[ ! -f "$THEME_DIR/index.html.bak-pre-unlock" ]]; then
  cp -p "$THEME_DIR/index.html" "$THEME_DIR/index.html.bak-pre-unlock"
  echo "已备份   : index.html.bak-pre-unlock"
fi

# 用 python 而不是 sed：要替换的是「同一个脚本、任意旧指纹」的整行，斜杠和引号在
# 替换串里容易被解释。幂等：同一份内容重复执行不会改动文件。
python3 - "$THEME_DIR/index.html" "$TAG" "$ASSET_NAME" <<'PY'
import re
import sys

path, tag, asset = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, encoding='utf-8') as handle:
    html = handle.read()

pattern = re.compile(r'[ \t]*<script defer src="/assets/' + re.escape(asset) + r'(\?v=[0-9a-f]+)?"></script>\n?')
if pattern.search(html):
    updated = pattern.sub('    ' + tag + '\n', html, count=1)
    action = '已更新'
else:
    marker = '</body>'
    if marker not in html:
        sys.exit('index.html 里没有 </body>，拒绝盲插')
    updated = html.replace(marker, '    ' + tag + '\n  ' + marker, 1)
    action = '已插入'

if updated != html:
    with open(path, 'w', encoding='utf-8') as handle:
        handle.write(updated)
print(f'{action}   : {tag}')
PY

echo
echo "验证："
grep -o "<script defer src=\"/assets/$ASSET_NAME?v=[0-9a-f]*\"></script>" "$THEME_DIR/index.html" || {
  echo "  ✗ index.html 里没找到带指纹的标签" >&2
  exit 1
}
ls -l "$THEME_DIR/assets/$ASSET_NAME"
echo
echo "index.html 不带哈希，浏览器可能缓存它本身；强制刷新一次即可。脚本 URL 带指纹，之后改脚本不需要再强刷。"
