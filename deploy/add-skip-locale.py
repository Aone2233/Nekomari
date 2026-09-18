#!/usr/bin/env python3
"""Add the ping.skipped_family string to every locale.

Inserted next to default_on_short rather than rewritten wholesale: the locale files
are ~1500 lines and re-serialising them would reformat everything and bury the real
change. Each edit is validated by parsing the file before it is written.
"""
import glob
import json
import re

NEW = {
    "en.json": "Address family mismatch, skipped",
    "zh_CN.json": "地址族不匹配，已跳过",
    "zh_TW.json": "位址族不相符，已略過",
    "ja_JP.json": "アドレスファミリ不一致のためスキップ",
    "id_ID.json": "Keluarga alamat tidak cocok, dilewati",
}

for path in sorted(glob.glob("frontend/src/i18n/locales/*.json")):
    name = path.replace("\\", "/").rsplit("/", 1)[-1]
    if name not in NEW:
        continue
    text = open(path, encoding="utf-8").read()
    if '"skipped_family"' in text:
        print("  already present:", name)
        continue

    m = re.search(r'^(\s*)"ping":\s*\{', text, re.M)
    if not m:
        print("  !! no ping section in", name)
        continue
    indent = m.group(1) + "  "

    anchor = re.search(
        r'^(\s*)"default_on_short"\s*:\s*"(?:[^"\\]|\\.)*",?\s*$', text, re.M)
    if not anchor:
        print("  !! no default_on_short anchor in", name)
        continue

    # Two shapes, depending on whether the anchor is the last key in its object:
    #   anchor ends with ","  -> more keys follow, so our entry needs a trailing ","
    #   anchor has no comma   -> it was last, so our entry needs a leading ","
    # Getting this wrong produces invalid JSON; the parse below is what catches it.
    entry = indent + '"skipped_family": ' + json.dumps(NEW[name], ensure_ascii=False)
    if anchor.group(0).rstrip().endswith(","):
        insert = "\n" + entry + ","
    else:
        insert = ",\n" + entry
    text = text[:anchor.end()] + insert + text[anchor.end():]

    parsed = json.loads(text)  # validate before writing
    assert parsed["ping"]["skipped_family"] == NEW[name], name
    open(path, "w", encoding="utf-8", newline="").write(text)
    print("  added to", name)
