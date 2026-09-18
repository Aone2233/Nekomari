#!/usr/bin/env python3
"""把主题背景图从 OpenList 取下来装进主题资源目录，并让配置指向本地。

为什么落盘而不是直接把配置改成 OpenList 的 URL
---------------------------------------------
  1. 那些 URL 带签名，会过期。上一次坏掉就是签名换了实例。
  2. 面板自己提供 /assets/ 下的文件，不依赖 OpenList 是否在线。
  3. 以后再搬一次 OpenList，背景不会跟着坏。

主题的约定：backgroundImage 与 backgroundImageMobile 各是「亮色|暗色」两个 URL 用 |
拼起来的字符串，主题内部按 | 切开分别用于白天和晚上。这里保持同样的形状，只是把两个
分量都换成本地路径。

用法（在面板主机上）：sudo ./install-theme-background.py
"""
import json
import os
import shutil
import sqlite3
import sys
import time
import urllib.error
import urllib.request

KDB = "/opt/nekomari/data/komari.db"
THEME_DIR = "/opt/nekomari/data/theme/LuminaPlus/dist"
THEME = "LuminaPlus"

# 用户提供的四张图：桌面/手机的亮色与暗色。
SOURCES = {
    "desktop_light": ("bg-desktop-light.jpeg",
                      "https://dav.orderly2233.org:49185/d/images/file.tmpr8xKxe.jpeg"
                      "?sign=IOc82vQxnTbtCUfrtFjp7qks5M7FKSCFqGhmdLwtDo0=:0"),
    "desktop_dark": ("bg-desktop-dark.jpg",
                     "https://dav.orderly2233.org:49185/d/images/pkg9p3.jpg"
                     "?sign=axj2u8kPpLUuDYZ0CQlWKH2Vm4vvT8D8rBEDj6-XYBg=:0"),
    "mobile_light": ("bg-mobile-light.png",
                     "https://dav.orderly2233.org:49185/d/images/"
                     "dmds_%E3%81%AA%E3%82%93%E3%81%A7%E5%91%BC%E3%82%93%E3%81%A0%E3%81%AE"
                     "%EF%BC%9F_103661112_p0.png?sign=Tlsx5YHW_wls6njPNF3aSpBQ7NE1AwLl3Lw7AEs1R1k=:0"),
    "mobile_dark": ("bg-mobile-dark.jpg",
                    "https://dav.orderly2233.org:49185/d/images/89174736_p0.jpg"
                    "?sign=QcmwBMTSPv8sPVR5qf_yUvTu7INZ6R0UdQcQjjqKB94=:0"),
}

assets_dir = os.path.join(THEME_DIR, "assets")
os.makedirs(assets_dir, exist_ok=True)

installed = {}
failures = []
for slot, (filename, url) in SOURCES.items():
    request = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            data = response.read()
            content_type = response.headers.get("Content-Type", "")
    except urllib.error.HTTPError as exc:
        failures.append(f"{slot}: HTTP {exc.code}")
        print(f"  {slot:<14} FAIL  HTTP {exc.code}")
        continue
    except Exception as exc:
        failures.append(f"{slot}: {type(exc).__name__}: {exc}")
        print(f"  {slot:<14} FAIL  {type(exc).__name__}: {exc}")
        continue

    if len(data) < 4096:
        failures.append(f"{slot}: only {len(data)} bytes")
        print(f"  {slot:<14} FAIL  只有 {len(data)} 字节")
        continue

    destination = os.path.join(assets_dir, filename)
    with open(destination, "wb") as handle:
        handle.write(data)
    os.chmod(destination, 0o644)
    installed[slot] = f"/assets/{filename}"
    print(f"  {slot:<14} OK    {len(data):>9,} bytes  {content_type[:24]:<24} -> {filename}")

if failures:
    raise SystemExit("\n有图片没取到，未改动配置：\n  " + "\n  ".join(failures))

conn = sqlite3.connect(KDB)
row = conn.execute("select data from theme_configurations where short=?", (THEME,)).fetchone()
config = json.loads(row[0])

print("\n修改前：")
for key in ("enableBackgroundImage", "backgroundMediaType", "backgroundImage", "backgroundImageMobile"):
    value = str(config.get(key, ""))
    print(f"  {key:<24} = {value[:78]}{'...' if len(value) > 78 else ''}")

config["enableBackgroundImage"] = True
config["backgroundMediaType"] = "image"
# 保持主题约定的「亮色|暗色」形状，只是两个分量都换成本地路径。
config["backgroundImage"] = f"{installed['desktop_light']}|{installed['desktop_dark']}"
config["backgroundImageMobile"] = f"{installed['mobile_light']}|{installed['mobile_dark']}"

print("修改后：")
for key in ("enableBackgroundImage", "backgroundMediaType", "backgroundImage", "backgroundImageMobile"):
    print(f"  {key:<24} = {config.get(key, '')}")

backup = f"{KDB}.bak-theme-bg-{time.strftime('%Y%m%d-%H%M%S')}"
shutil.copy2(KDB, backup)
conn.execute("update theme_configurations set data=? where short=?",
             (json.dumps(config, ensure_ascii=False), THEME))
conn.commit()
conn.close()
print(f"\n数据库已备份：{backup}")
print("已写入")
