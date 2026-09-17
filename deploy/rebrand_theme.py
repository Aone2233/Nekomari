#!/usr/bin/env python3
"""Rebrand the LuminaPlus theme for Nekomari.

Deliberately narrow. The theme bundle refers to "Komari" in two very different
ways, and only one of them may change:

  * user-visible text (document title, meta description, "未在 Komari 后台绑定")
    -> rebranded, because that text is what a visitor reads.
  * the theme identifier `Komari-Theme-LuminaPlus` in komari-theme.json and in
    the theme-info fallback -> left alone. That string is the theme's `short`/
    `name` contract with the server; changing it would desynchronise the
    installed directory, the `themes` setting and the manifest. It is an
    identifier that happens to contain the old project name, not branding.

Usage: rebrand_theme.py <extracted-theme-dir> <out-zip>
"""
import os
import re
import sys
import zipfile

SRC = sys.argv[1]
OUT = sys.argv[2]

# (relative path, description, [(old, new), ...])
EDITS = {
    "dist/index.html": [
        # <title> and every meta that repeats the theme name
        ("<title>Komari-Theme-LuminaPlus</title>",
         "<title>Nekomari Theme LuminaPlus</title>"),
        ('content="Komari-Theme-LuminaPlus"',
         'content="Nekomari Theme LuminaPlus"'),
        ('content="A Komari monitor theme."',
         'content="A Nekomari monitor theme."'),
        ('content="Komari-Theme-LuminaPlus, LuminaPlus, server monitor',
         'content="Nekomari Theme LuminaPlus, LuminaPlus, server monitor'),
    ],
    "dist/manifest.json": [
        ('"name": "Komari Monitor"', '"name": "Nekomari Monitor"'),
        ('"short_name": "Komari"', '"short_name": "Nekomari"'),
        ('"description": "A Komari server monitor."',
         '"description": "A Nekomari server monitor."'),
    ],
    "dist/assets/index-Kbf1m-l1.js": [
        # var yi = document title, var bi = meta description, injected at runtime
        ("var yi=`Komari-Theme-LuminaPlus`,bi=`A Komari monitor theme.`",
         "var yi=`Nekomari Theme LuminaPlus`,bi=`A Nekomari monitor theme.`"),
    ],
    "dist/assets/ThemeManage-aswmbkUa.js": [
        # shown when a probe is not bound to this server; the panel is Nekomari
        ("未在 Komari 后台绑定到此服务器。", "未在 Nekomari 后台绑定到此服务器。"),
    ],
    "komari-theme.json": [
        # version marks the fork; `name`/`short` stay as the identifier contract
        ('"version": "1.3.3"', '"version": "1.3.3-nk1"'),
    ],
}

changed_any = False
for rel, edits in EDITS.items():
    path = os.path.join(SRC, rel)
    if not os.path.exists(path):
        print(f"  !! missing: {rel}")
        continue
    text = open(path, encoding="utf-8").read()
    for old, new in edits:
        if old not in text:
            print(f"  ?? not found in {rel}: {old[:60]}")
            continue
        text = text.replace(old, new)
        changed_any = True
        print(f"  ok {rel}: {old[:58]} -> {new[:58]}")
    open(path, "w", encoding="utf-8").write(text)

if not changed_any:
    print("nothing changed")
    raise SystemExit(1)

# Repack preserving the theme's expected layout (komari-theme.json at the root).
print(f"\npacking {OUT}")
with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _dirs, files in os.walk(SRC):
        for f in files:
            # Skip anything that is not theme content: running this script from
            # inside the theme directory would otherwise pack the script itself,
            # and the server's extractor would carry a stray file into the theme.
            if f.endswith(".py"):
                continue
            full = os.path.join(root, f)
            z.write(full, os.path.relpath(full, SRC))
print("done")
