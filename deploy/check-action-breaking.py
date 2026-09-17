#!/usr/bin/env python3
"""Show what changed between the pinned action version and the latest, so a major
jump is made with the breaking changes visible rather than discovered in CI."""
import json
import pathlib
import re
import urllib.request

WF = pathlib.Path(".github/workflows")
uses = set()
for f in WF.glob("*.yml"):
    for m in re.finditer(r"uses:\s*([\w.-]+/[\w.-]+)@([\w.]+)", f.read_text(encoding="utf-8")):
        uses.add((m.group(1), m.group(2)))


def api(url):
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "action-version-check")
    with urllib.request.urlopen(req, timeout=25) as r:
        return json.load(r)


for repo, pinned in sorted(uses):
    try:
        rels = api(f"https://api.github.com/repos/{repo}/releases?per_page=40")
    except Exception as e:
        print(f"=== {repo}: {type(e).__name__}")
        continue

    pin_major = int(re.sub(r"\D", "", pinned) or 0)
    print(f"=== {repo}  pinned {pinned}")
    for r in rels:
        tag = r.get("tag_name", "")
        mm = re.match(r"v?(\d+)", tag)
        if not mm:
            continue
        major = int(mm.group(1))
        if major <= pin_major:
            continue
        body = (r.get("body") or "").strip()
        # Only surface the breaking-change portion; the rest is noise here.
        keep = []
        for line in body.splitlines():
            if re.search(r"breaking|BREAKING|removed|no longer|requires|must now", line):
                keep.append(line.strip())
        print(f"  --- {tag} ---")
        for k in keep[:4]:
            print(f"      {k[:150]}")
        if not keep:
            print("      (no breaking-change lines found)")
