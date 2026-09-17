#!/usr/bin/env python3
"""Update the favicon cache note in every locale.

The note told users the icon "can be slow to update and it is often necessary to
clear your browser's cache" -- true while /favicon.ico carried no cache
directives and the preview used a fixed URL. Both are fixed now, so the advice is
stale; an already-open tab may still hold the old icon until it reloads, which the
new wording says instead of blaming the cache.

Edits only the one line per file rather than re-serialising the JSON: these files
are ~1500 lines and a full rewrite would reformat them and bury the real change.
"""
import glob
import json
import re
import sys

NEW = {
    "en.json": "The preview updates immediately. An already-open browser tab may keep showing the previous icon until it is reloaded.",
    "zh_CN.json": "预览会立即更新。已打开的浏览器标签页可能仍显示旧图标，重新加载后生效。",
    "zh_TW.json": "預覽會立即更新。已開啟的瀏覽器分頁可能仍顯示舊圖示，重新載入後生效。",
    "ja_JP.json": "プレビューはすぐに更新されます。すでに開いているタブは、再読み込みするまで古いアイコンを表示し続けることがあります。",
    "id_ID.json": "Pratinjau diperbarui segera. Tab peramban yang sudah terbuka mungkin masih menampilkan ikon lama sampai dimuat ulang.",
}

changed, skipped = [], []
for path in sorted(glob.glob("frontend/src/i18n/locales/*.json")):
    name = path.replace("\\", "/").rsplit("/", 1)[-1]
    if name not in NEW:
        skipped.append(name)
        continue
    text = open(path, encoding="utf-8").read()

    # Locate the existing value so the replacement is anchored to real content.
    m = re.search(r'^(\s*)"favicon_note":\s*("(?:[^"\\]|\\.)*")', text, re.M)
    if not m:
        skipped.append(f"{name} (no favicon_note)")
        continue

    old_value = json.loads(m.group(2))
    if old_value == NEW[name]:
        continue

    new_line = f'{m.group(1)}"favicon_note": {json.dumps(NEW[name], ensure_ascii=False)}'
    text = text[:m.start()] + new_line + text[m.end():]
    open(path, "w", encoding="utf-8", newline="").write(text)

    # Confirm the file is still valid JSON and the key now reads back as intended.
    check = json.loads(open(path, encoding="utf-8").read())
    assert check["settings"]["custom"]["favicon_note"] == NEW[name], name
    changed.append(name)

for n in changed:
    print("updated", n)
for n in skipped:
    print("skipped", n, file=sys.stderr)
print(f"{len(changed)} file(s) updated, {len(skipped)} skipped")
