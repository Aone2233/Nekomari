#!/usr/bin/env python3
"""把主题背景图从局域网 WebDAV 上取下来。

LuminaPlus 的 backgroundImage 指向 dav.orderly2233.org，而那个域名解析到 192.168.100.168
—— 一个【局域网地址】。所以面板的背景图只有用户家里的网能取到，公网访客（以及服务器自己）
永远取不到，背景就一直不显示。

这个脚本在能访问该主机的机器上运行，把图片写到指定路径，供安装到主题资源目录。
用法：fetch-theme-background.py <输出路径>
"""
import json
import sqlite3
import sys
import urllib.error
import urllib.request

OUT = sys.argv[1]
KDB = "/opt/nekomari/data/komari.db"

conn = sqlite3.connect(f"file:{KDB}?mode=ro", uri=True)
row = conn.execute("select data from theme_configurations where short='LuminaPlus'").fetchone()
config = json.loads(row[0])

url = config.get("backgroundImage") or ""
print(f"backgroundImage = {url[:120]}{'...' if len(url) > 120 else ''}")

request = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
try:
    with urllib.request.urlopen(request, timeout=30) as response:
        data = response.read()
        content_type = response.headers.get("Content-Type", "")
except urllib.error.HTTPError as exc:
    raise SystemExit(f"HTTP {exc.code}: {exc.read()[:200]!r}")
except Exception as exc:
    raise SystemExit(f"{type(exc).__name__}: {exc}")

print(f"fetched {len(data)} bytes, content-type={content_type!r}")
if len(data) < 1024:
    raise SystemExit("响应太小，不像一张图")

with open(OUT, "wb") as handle:
    handle.write(data)
print(f"wrote {OUT}")
