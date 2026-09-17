#!/usr/bin/env python3
"""Print the full release notes for specific action versions, for the ones whose
breaking changes could actually affect this repo's workflows."""
import json
import urllib.request

WANT = [
    ("actions/download-artifact", "v8.0.0"),
    ("actions/upload-artifact", "v7.0.0"),
    ("actions/upload-artifact", "v8.0.0"),
    ("actions/checkout", "v6.1.0"),
]


def api(url):
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "action-version-check")
    with urllib.request.urlopen(req, timeout=25) as r:
        return json.load(r)


for repo, tag in WANT:
    try:
        rel = api(f"https://api.github.com/repos/{repo}/releases/tags/{tag}")
    except Exception as e:
        print(f"=== {repo} {tag}: {type(e).__name__}\n")
        continue
    print(f"=== {repo} {tag} ===")
    body = (rel.get("body") or "").strip()
    print(body[:2200])
    print()
