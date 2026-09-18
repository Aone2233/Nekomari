#!/usr/bin/env python3
"""把主题背景图从 OpenList 取下来并装进主题资源目录。

背景一直加载不出来，原因不是「局域网」—— dav.orderly2233.org 是分离解析：局域网内是
192.168.100.168，公网是 59.66.23.138。真正的原因是 OpenList 换了端口（443 -> 49185），
配置里那两条 URL 还指着默认端口，所以一直超时。

这里做的是把图片【落到面板本地】而不是把配置改成新端口：
  * 图片进了主题的 /assets/，由面板自己提供，不再依赖 OpenList 是否在线
  * 也不依赖那个会过期的签名 URL
  * 以后再搬一次 OpenList，背景不会跟着坏

用法（在面板主机上）：sudo ./install-theme-background.py <image-file> [name]
"""
import json
import os
import shutil
import sqlite3
import sys
import time

KDB = "/opt/nekomari/data/komari.db"
THEME_DIR = "/opt/nekomari/data/theme/LuminaPlus/dist"
THEME = "LuminaPlus"

if len(sys.argv) < 2:
    raise SystemExit(__doc__)
source = sys.argv[1]
asset_name = sys.argv[2] if len(sys.argv) > 2 else "background" + os.path.splitext(source)[1]

if not os.path.isfile(source):
    raise SystemExit(f"找不到 {source}")
size = os.path.getsize(source)
if size < 4096:
    raise SystemExit(f"{source} 只有 {size} 字节，不像一张图")

# 1. 装进主题资源目录
destination = os.path.join(THEME_DIR, "assets", asset_name)
os.makedirs(os.path.dirname(destination), exist_ok=True)
shutil.copy2(source, destination)
os.chmod(destination, 0o644)
print(f"已安装   : assets/{asset_name}  ({size} 字节)")

# 2. 指向本地路径
conn = sqlite3.connect(KDB)
row = conn.execute("select data from theme_configurations where short=?", (THEME,)).fetchone()
if not row:
    raise SystemExit(f"主题 {THEME} 没有配置记录")
config = json.loads(row[0])

local = f"/assets/{asset_name}"
print("修改前：")
for key in ("enableBackgroundImage", "backgroundMediaType", "backgroundImage", "backgroundVideo"):
    value = config.get(key, "")
    if isinstance(value, str) and len(value) > 70:
        value = value[:70] + "..."
    print(f"  {key:<24} = {value!r}")

config["enableBackgroundImage"] = True
config["backgroundMediaType"] = "image"
config["backgroundImage"] = local
config["backgroundImageMobile"] = local

print("修改后：")
for key in ("enableBackgroundImage", "backgroundMediaType", "backgroundImage", "backgroundImageMobile"):
    print(f"  {key:<24} = {config.get(key, '')!r}")

backup = f"{KDB}.bak-theme-bg-{time.strftime('%Y%m%d-%H%M%S')}"
shutil.copy2(KDB, backup)
conn.execute("update theme_configurations set data=? where short=?",
             (json.dumps(config, ensure_ascii=False), THEME))
conn.commit()
conn.close()
print(f"\n数据库已备份：{backup}")
print("已写入")
