#!/usr/bin/env python3
"""What do the unlock-check endpoints actually return? Run this before writing the probe.

Unlock detection is a pile of string matching against pages that change without notice,
so the signals have to be read off real responses rather than recalled. This dumps the
status, the interesting headers and the marker strings for each candidate, from
whichever host it runs on (that host's IP is what gets tested).

Usage: unlock-probe-recon.py [host-label]
"""
import gzip
import io
import json
import ssl
import sys
import urllib.request

LABEL = sys.argv[1] if len(sys.argv) > 1 else "this host"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")

CANDIDATES = [
    # (label, url, markers to look for in the body)
    ("cloudflare-trace", "https://www.cloudflare.com/cdn-cgi/trace", ["loc=", "ip="]),
    ("chatgpt-trace", "https://chatgpt.com/cdn-cgi/trace", ["loc="]),
    ("chatgpt-home", "https://chatgpt.com/", ["unsupported_country", "not available", "Just a moment"]),
    ("openai-compliance", "https://api.openai.com/compliance/cookie_requirements", []),
    ("claude-home", "https://claude.ai/", ["not available", "unavailable", "App unavailable"]),
    ("gemini-home", "https://gemini.google.com/", ["not available", "unavailable"]),
    ("netflix-original", "https://www.netflix.com/title/81280792", ["81280792", "Not Available", "not available"]),
    ("netflix-catalog", "https://www.netflix.com/title/80018499", ["80018499", "Not Available"]),
    ("youtube-premium", "https://www.youtube.com/premium", ["countryCode", "premium", "not available"]),
    ("disneyplus", "https://www.disneyplus.com/", ["not available", "unavailable", "disney"]),
    ("primevideo", "https://www.primevideo.com/", ["not available", "unavailable"]),
    ("spotify", "https://open.spotify.com/", ["not available", "unavailable"]),
]


def fetch(url, timeout=12):
    req = urllib.request.Request(url, headers={
        "User-Agent": UA,
        "Accept": "text/html,application/json;q=0.9,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.9",
    })
    ctx = ssl.create_default_context()
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ctx) as r:
            raw = r.read()
            if r.headers.get("Content-Encoding") == "gzip":
                raw = gzip.GzipFile(fileobj=io.BytesIO(raw)).read()
            return r.status, dict(r.headers), raw[:400000].decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        raw = e.read()
        return e.code, dict(e.headers), raw[:400000].decode("utf-8", "replace")
    except Exception as e:
        return None, {}, f"<{type(e).__name__}: {e}>"


print(f"=== unlock endpoint reconnaissance from {LABEL} ===\n")
summary = {}
for label, url, markers in CANDIDATES:
    status, headers, body = fetch(url)
    found = [m for m in markers if m.lower() in body.lower()]
    loc = ""
    for line in body.splitlines():
        if line.startswith("loc="):
            loc = line.strip()
            break
    ctype = headers.get("Content-Type", "")
    server = headers.get("Server", "")
    print(f"--- {label}")
    print(f"    url      : {url}")
    print(f"    status   : {status}   server={server!r}  type={ctype[:40]!r}  bytes={len(body)}")
    if loc:
        print(f"    {loc}")
    print(f"    markers  : {found if found else '(none of ' + str(markers) + ')'}")
    if status is None or not body:
        print(f"    body     : {body[:200]}")
    summary[label] = {"status": status, "markers": found, "bytes": len(body), "loc": loc}
    print()

print("=== summary (json) ===")
print(json.dumps(summary, ensure_ascii=False, indent=1))
