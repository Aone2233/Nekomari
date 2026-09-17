#!/usr/bin/env python3
"""Check that every `with:` input a workflow passes still exists on the pinned
action version.

An action's major bump can drop or rename inputs, and that failure only shows up
when the workflow actually runs -- which for release.yml means at release time,
the worst moment to find out. This reads each action's action.yml at the pinned
tag and compares it against the inputs the workflows pass.

Usage: check-action-inputs.py
"""
import json
import pathlib
import re
import urllib.request

try:
    import yaml
except ImportError:
    raise SystemExit("pyyaml required")

WF = pathlib.Path(".github/workflows")
UA = {"User-Agent": "check-action-inputs",
      "Accept": "application/vnd.github+json"}


def get(url):
    req = urllib.request.Request(url, headers=UA)
    with urllib.request.urlopen(req, timeout=25) as r:
        return r.read()


def action_inputs(repo, tag):
    """Read action.yml (or .yaml) at the tag and return its declared input names."""
    for name in ("action.yml", "action.yaml"):
        try:
            raw = get(f"https://raw.githubusercontent.com/{repo}/{tag}/{name}")
        except Exception:
            continue
        try:
            doc = yaml.safe_load(raw.decode("utf-8"))
        except Exception:
            continue
        return set((doc.get("inputs") or {}).keys())
    return None


problems = 0
for f in sorted(WF.glob("*.yml")):
    doc = yaml.safe_load(f.read_text(encoding="utf-8"))
    for job_name, job in (doc.get("jobs") or {}).items():
        for step in job.get("steps") or []:
            uses = step.get("uses") or ""
            if "@" not in uses or "with" not in step:
                continue
            repo, tag = uses.split("@", 1)
            if repo.startswith("./"):
                continue
            declared = action_inputs(repo, tag)
            if declared is None:
                print(f"  {repo}@{tag}: could not read action.yml (composite/local?)")
                continue
            used = set(step["with"].keys())
            unknown = used - declared
            if unknown:
                problems += 1
                print(f"  {f.name} / {job_name} / {step.get('name', uses)}")
                print(f"      {repo}@{tag} does NOT declare: {sorted(unknown)}")
                print(f"      it declares: {sorted(declared)}")
            else:
                print(f"  ok  {repo}@{tag}  ({len(used)} input(s) all declared)")

print()
print(f"  problems: {problems}")
