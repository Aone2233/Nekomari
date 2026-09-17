#!/usr/bin/env python3
"""Bump every GitHub Action in .github/workflows to its latest major.

Pinned majors are what produced the Node 20 deprecation warning on every job:
GitHub now forces those actions onto Node 24, which works but is unsupported and
will stop working. The fix is to move to the majors that ship a Node 24 runtime.

The target versions are read from each action's own latest release rather than
hardcoded, because they differ per publisher -- docker/build-push-action is
already past v5 while actions/checkout is not, so "bump everything to v5" would
leave most of them behind.

Usage: bump-actions.py [--apply]
"""
import json
import pathlib
import re
import sys
import urllib.request

APPLY = "--apply" in sys.argv
WF = pathlib.Path(".github/workflows")


def latest_major(repo):
    req = urllib.request.Request(f"https://api.github.com/repos/{repo}/releases/latest")
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "bump-actions")
    with urllib.request.urlopen(req, timeout=25) as r:
        tag = json.load(r).get("tag_name", "")
    m = re.match(r"v(\d+)", tag)
    return f"v{m.group(1)}" if m else None


# Resolve once per action.
needed = {}
for f in WF.glob("*.yml"):
    for m in re.finditer(r"uses:\s*([\w.-]+/[\w.-]+)@([\w.]+)", f.read_text(encoding="utf-8")):
        needed[m.group(1)] = m.group(2)

targets = {}
for repo, pinned in sorted(needed.items()):
    try:
        tgt = latest_major(repo)
    except Exception as e:
        print(f"  {repo}: lookup failed ({type(e).__name__}), leaving at {pinned}")
        continue
    targets[repo] = (pinned, tgt)

print(f"{'action':<38} {'now':<6} -> {'target':<6}")
print("-" * 54)
for repo, (pinned, tgt) in targets.items():
    mark = "" if pinned == tgt else "  (bump)"
    print(f"{repo:<38} {pinned:<6} -> {tgt:<6}{mark}")

if not APPLY:
    print("\ndry run -- pass --apply to write")
    raise SystemExit(0)

changed = 0
for f in WF.glob("*.yml"):
    text = f.read_text(encoding="utf-8")
    orig = text
    for repo, (pinned, tgt) in targets.items():
        if pinned != tgt:
            text = text.replace(f"{repo}@{pinned}", f"{repo}@{tgt}")
    if text != orig:
        f.write_text(text, encoding="utf-8")
        changed += 1
        print(f"  updated {f}")
print(f"\n{changed} workflow file(s) updated")
