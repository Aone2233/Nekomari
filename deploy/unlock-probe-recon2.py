#!/usr/bin/env python3
"""Second pass: find markers that actually distinguish unlocked from blocked.

The first pass showed why the obvious ones do not work:
  * chatgpt.com and claude.ai answer 403 to a plain client from a datacenter IP --
    that is Cloudflare bot protection, not a country block, so status alone lies
  * disneyplus.com and primevideo.com contain "unavailable"/"not available" in their
    own bundle regardless of region, so substring presence proves nothing

So this pass prints the small bodies verbatim and counts a curated marker list on the
big ones, and it reports the Cloudflare `loc` for each host (that endpoint is not behind
the bot rule and is the one reliable region signal).

Usage: unlock-probe-recon2.py [host-label]
"""
import gzip
import io
import json
import re
import ssl
import sys
import urllib.error
import urllib.request

LABEL = sys.argv[1] if len(sys.argv) > 1 else "this host"
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")

# Small responses are printed in full; big ones get marker counts.
TARGETS = [
    ("chatgpt-trace", "https://chatgpt.com/cdn-cgi/trace", True),
    ("openai-compliance", "https://api.openai.com/compliance/cookie_requirements", True),
    ("claude-trace", "https://claude.ai/cdn-cgi/trace", True),
    ("gemini-trace", "https://gemini.google.com/cdn-cgi/trace", True),
    ("youtube-premium", "https://www.youtube.com/premium", False),
    ("netflix-original", "https://www.netflix.com/title/81280792", False),
    ("netflix-catalog", "https://www.netflix.com/title/80018499", False),
]

# Markers worth counting on the large pages, grouped by what they would mean.
MARKERS = {
    "youtube-premium": [
        '"countryCode":"', "premium_unavailable", '"isPremium"', "ad-free", "Premium is not available",
        "getPremium", '"offerId"',
    ],
    "netflix-original": ['"id":81280792', "81280792", "Not Available", "not available in your country"],
    "netflix-catalog": ['"id":80018499', "80018499", "Not Available", "not available in your country"],
}


def fetch(url, timeout=15):
    req = urllib.request.Request(url, headers={
        "User-Agent": UA,
        "Accept": "text/html,application/json;q=0.9,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.9",
    })
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ssl.create_default_context()) as r:
            raw = r.read()
            if r.headers.get("Content-Encoding") == "gzip":
                raw = gzip.GzipFile(fileobj=io.BytesIO(raw)).read()
            return r.status, raw.decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        raw = e.read()
        if e.headers.get("Content-Encoding") == "gzip":
            raw = gzip.GzipFile(fileobj=io.BytesIO(raw)).read()
        return e.code, raw.decode("utf-8", "replace")
    except Exception as e:
        return None, f"<{type(e).__name__}: {e}>"


print(f"=== unlock marker reconnaissance (pass 2) from {LABEL} ===\n")
out = {}
for label, url, small in TARGETS:
    status, body = fetch(url)
    print(f"--- {label}  [{status}]  {url}  ({len(body)} bytes)")
    if "loc=" in body:
        m = re.search(r"^loc=(\S+)", body, re.M)
        print(f"    loc      : {m.group(1) if m else '?'}")
    if small and status:
        print("    body     : " + body.strip().replace("\n", " | ")[:400])
    for marker in MARKERS.get(label, []):
        n = body.count(marker)
        if n:
            print(f"    marker   : {marker!r} x{n}")
    print()
    out[label] = {"status": status, "bytes": len(body)}

print("=== json ===")
print(json.dumps(out, ensure_ascii=False, indent=1))
