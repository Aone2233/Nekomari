#!/usr/bin/env python3
"""把 LuminaPlus 的背景从「外部 OpenList 上的签名 URL」换成主题自带的本地背景。

问题
----
主题配置里的 backgroundImage / backgroundImageMobile 指向 dav.orderly2233.org，那两条
URL 是取不到的。原因我一开始判断错了，记在这里免得下次再错：

  ✗ 「该域名只解析到局域网」—— 不对。它是【分离解析】：局域网内是 192.168.100.168，
    公网是 59.66.23.138。从公网解析是通的，我第一次只在本机查了 DNS 就下了结论。

  ✓ 真正的原因是 OpenList 换了端口（443 -> 49185）。那两条 URL 用的是默认端口，所以
    一直超时。换到 :49185 之后 OpenList 首页立刻 200。

  但换端口还不够：URL 上的 sign= 是旧实例签的，新实例一律返回 401；不带签名的路径同样
  401（整个 /d/ 都需要鉴权）。所以要么拿一条新签名，要么在 OpenList 里把该路径设为公开。

在拿到可用 URL 之前，主题自带的 LanternRivers 视频是唯一对所有人都能工作的背景：它就
在 /assets/ 下、由面板自己提供、不会过期。这个脚本切的就是它。

用法（在面板主机上）：
  sudo ./set-theme-background-local.py [--dry-run]

写完之后必须重启面板才生效
--------------------------
实测（2026-09-21）：面板把主题配置读进内存，这个脚本写库之后 `/api/public` 仍然返回旧值，
直到 `docker compose restart nekomari`。排查过程记在这里，免得下次重走：

  * 不是 dbcache —— `theme_configurations` 不在它监视的 configs/clients/sessions/users 里；
  * 不是 managedconfig —— LuminaPlus 的 komari-theme.json 根本没有 configuration 段；
  * 不是数据库选错 —— 容器挂载的 /app/data/komari.db 与宿主机是同一个 inode；
  * 定位办法：往同一行的 JSON 里加一个 zzProbe 键，重启前 API 看不到、重启后立刻看到。

所以脚本最后会打印重启提示。写完就刷新页面看不到变化时，先重启，别怀疑背景图本身。
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
print("")
print("!! 面板把主题配置读在内存里，必须重启才生效：")
print("     cd /opt/nekomari && docker compose restart nekomari")
print("   不重启的话 /api/public 仍然返回旧值（详见本文件顶部的实测记录）。")
