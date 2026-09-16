#!/bin/bash
# Nekomari —— 一键构建（monorepo）
#
# 目录约定：
#   ./                 服务端 + 面板（Go，内嵌默认主题）
#   ./frontend/        前端 SPA（TypeScript + Vite）
#   ./agent/           探针（Go，独立 module）
#   ./tools/zstdpack/  主题打包器（Go）
#
# 产物：
#   nekomari[.exe]                        服务端 + 面板
#   agent/nekomari-agent[.exe]            探针
#   web/public/defaultTheme/dist.tar.zst  内嵌主题（中间产物，必须先于服务端构建）
#
# 依赖：Go >= 1.25、Node >= 20 + npm；Linux 原生构建还需 gcc（sqlite 驱动走 CGO）
#
# 关于交叉编译：GOOS=linux CGO_ENABLED=0 会失败（sqlite 需要 CGO）。
# 请在目标平台原生构建，或用 zig cc 作为交叉编译器。

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

GO_BIN="${GO_BIN:-go}"
SKIP_FRONTEND="${SKIP_FRONTEND:-0}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"
export GOFLAGS="${GOFLAGS:--mod=mod}"

EXT=""
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) EXT=".exe" ;;
esac

echo "==> [1/5] 构建主题打包器"
(cd tools/zstdpack && $GO_BIN build -o zstdpack$EXT .)

if [ "$SKIP_FRONTEND" != "1" ]; then
  echo "==> [2/5] 构建前端（frontend/）"
  (cd frontend && npm install --no-audit --no-fund && npm run build)
else
  echo "==> [2/5] 跳过前端构建（SKIP_FRONTEND=1）"
fi

if [ ! -f frontend/dist/index.html ]; then
  echo "!! frontend/dist/index.html 不存在，无法打包主题" >&2
  exit 1
fi

echo "==> [3/5] 打包内嵌主题 -> web/public/defaultTheme/dist.tar.zst"
mkdir -p web/public/defaultTheme
./tools/zstdpack/zstdpack$EXT -src frontend/dist -out web/public/defaultTheme/dist.tar.zst
cp -f frontend/komari-theme.json web/public/defaultTheme/
for f in preview.png perview.png preview.webp; do
  [ -f "frontend/$f" ] && cp -f "frontend/$f" web/public/defaultTheme/ || true
done

echo "==> [4/5] 构建服务端"
$GO_BIN build -o nekomari$EXT .

echo "==> [5/5] 构建探针（agent/）"
(cd agent && $GO_BIN build -o nekomari-agent$EXT .)

echo
echo "构建完成："
ls -la nekomari$EXT 2>/dev/null || true
ls -la agent/nekomari-agent$EXT 2>/dev/null || true
echo
echo "启动：  ./nekomari$EXT server"
echo "自检：  ./nekomari$EXT netcheck 1.1.1.1"
