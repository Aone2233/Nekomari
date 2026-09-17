#!/usr/bin/env python3
"""Resolve the latest release tag of every GitHub Action used by this repo.

Actions pinned to a Node 20 runtime still run, but GitHub now forces them onto
Node 24 and warns on every job. Upgrading needs the actual current major version
per action, which differs per publisher -- guessing v5 for everything would break
the ones that are already at v6 (docker/build-push-action).

Reads the action list out of the workflows so it cannot drift from what is used.
"""
import json
import pathlib
import re
import urllib.request

WF = pathlib.Path(".github/workflows")
uses = set()
for f in WF.glob("*.yml"):
    for m in re.finditer(r"uses:\s*([\w.-]+/[\w.-]+)@([\w.]+)", f.read_text(encoding="utf-8")):
        uses.add((m.group(1), m.group(2)))


def latest(repo):
    req = urllib.request.Request(f"https://api.github.com/repos/{repo}/releases/latest")
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "action-version-check")
    try:
        with urllib.request.urlopen(req, timeout=25) as r:
            return json.load(r).get("tag_name", "?")
    except Exception as e:
        return f"error: {type(e).__name__}"


print(f"{'action':<38} {'pinned':<8} {'latest':<10} status")
print("-" * 74)
for repo, pinned in sorted(uses):
    lt = latest(repo)
    if lt.startswith("error"):
        status = lt
    elif lt == pinned:
        status = "up to date"
    else:
        status = "UPDATE AVAILABLE"
    print(f"{repo:<38} {pinned:<8} {lt:<10} {status}")
