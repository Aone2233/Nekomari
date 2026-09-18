#!/usr/bin/env python3
"""从 OpenList 取回主题背景图（新端口 49185）。

背景一直加载不出来，真正的原因是 OpenList 换了端口（443 -> 49185），而主题配置里那两条
URL 还指着默认端口。dav.orderly2233.org 本身是分离解析的 —— 局域网内 192.168.100.168，
公网 59.66.23.138 —— 所以从公网访问是通的，只是端口不对。

注意：换对端口只是让请求能到达 OpenList，**不代表能取到文件**。URL 上的 sign= 是旧实例
签的，新实例会返回 401；不带签名的路径同样 401（整个 /d/ 都需要鉴权）。这个脚本会把
每条 URL 的真实结果打出来，而不是假设换端口就够了。

配置里每条值其实是「亮色|暗色」两个 URL 用 | 拼起来的，这里两个都取。

用法：sudo python3 fetch-theme-background.py [数据库备份路径]
"""
import json
import re
import sqlite3
import sys
import urllib.error
import urllib.request

NEW_PORT = "49185"
OUT_DIR = "/tmp"


def candidates():
    backup = sys.argv[1] if len(sys.argv) > 1 else None
    source = f"file:{backup}?mode=ro" if backup else "file:/opt/nekomari/data/komari.db?mode=ro"
    conn = sqlite3.connect(source, uri=True)
    data = json.loads(conn.execute(
        "select data from theme_configurations where short='LuminaPlus'").fetchone()[0])
    urls = []
    for key in ("backgroundImage", "backgroundImageMobile"):
        for part in (data.get(key) or "").split("|"):
            part = part.strip()
            if part:
                urls.append((key, part))
    return urls


def with_port(url):
    return re.sub(r"^(https://dav\.orderly2233\.org)(:\d+)?/", r"\1:" + NEW_PORT + "/", url)


def fetch(url):
    request = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status, response.read(), response.headers.get("Content-Type", "")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()[:200], ""
    except Exception as exc:
        return None, f"{type(exc).__name__}: {exc}".encode(), ""


for index, (key, url) in enumerate(candidates(), start=1):
    new = with_port(url)
    status, body, content_type = fetch(new)
    name = new.split("/images/")[-1].split("?")[0]
    print(f"[{index}] {key}  ->  {name}")
    print(f"    url    : {new[:110]}")
    if status == 200 and len(body) > 4096:
        path = f"{OUT_DIR}/bg-{index}-{name}"
        with open(path, "wb") as handle:
            handle.write(body)
        print(f"    OK     : {len(body)} bytes  {content_type}  -> {path}")
    else:
        print(f"    FAIL   : status={status}  {body[:160]!r}")
