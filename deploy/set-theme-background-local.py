#!/usr/bin/env python3
"""把 LuminaPlus 的背景从「局域网 WebDAV 上的签名 URL」换成主题自带的本地背景。

问题
----
主题配置里的 backgroundImage / backgroundImageMobile 指向 dav.orderly2233.org，而那个
域名解析到 192.168.100.168 —— 一个【局域网地址】。所以：

  * 公网访客取不到它（DNS 指向内网）
  * 面板服务器自己也取不到
  * URL 还带签名（sign=...），会过期

结果就是背景永远不显示。主题自带的 LanternRivers 视频就在 /assets/ 下、由面板自己提供、
不会过期，是唯一对所有人都能工作的选择。

用法（在面板主机上）：
  sudo ./set-theme-background-local.py [--dry-run]
"""
import json
import shutil
import sqlite3
import sys
import time

KDB = "/opt/nekomari/data/komari.db"
THEME = "LuminaPlus"
VIDEO = "/assets/LanternRivers_1080p15fps2Mbps3s.mp4"
DRY_RUN = "--dry-run" in sys.argv

conn = sqlite3.connect(KDB)
row = conn.execute("select data from theme_configurations where short=?", (THEME,)).fetchone()
if not row:
    raise SystemExit(f"主题 {THEME} 没有配置记录")

config = json.loads(row[0])
print("修改前：")
for key in ("enableBackgroundImage", "backgroundMediaType", "backgroundImage",
            "backgroundImageMobile", "backgroundVideo"):
    value = config.get(key, "")
    if isinstance(value, str) and len(value) > 80:
        value = value[:80] + "..."
    print(f"  {key:<26} = {value!r}")

config["enableBackgroundImage"] = True
config["backgroundMediaType"] = "video"
config["backgroundVideo"] = VIDEO
# 清掉指向内网的两个 URL：留着它们，主题在视频不可用时还会回退过去，又变成一片空白。
config["backgroundImage"] = ""
config["backgroundImageMobile"] = ""

print("\n修改后：")
for key in ("enableBackgroundImage", "backgroundMediaType", "backgroundImage",
            "backgroundImageMobile", "backgroundVideo"):
    print(f"  {key:<26} = {config.get(key, '')!r}")

if DRY_RUN:
    print("\n--dry-run：未写入")
    raise SystemExit(0)

backup = f"{KDB}.bak-theme-bg-{time.strftime('%Y%m%d-%H%M%S')}"
shutil.copy2(KDB, backup)
print(f"\n已备份数据库：{backup}")

conn.execute("update theme_configurations set data=? where short=?",
             (json.dumps(config, ensure_ascii=False), THEME))
conn.commit()
conn.close()
print("已写入")
