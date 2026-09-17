#!/usr/bin/env python3
"""Point the notification-template help text at this repo's docs.

The locale files told users to read the upstream guide at
komari-document.pages.dev, which documents Komari. This fork ships its own
documentation, so the hint now points at the repository's docs directory instead
of sending readers to a different project's site.

Only the URL changes; the surrounding translated sentence is left as-is, because
the wording is accurate either way.
"""
import glob
import sys

OLD = "https://komari-document.pages.dev/faq/notification-template.html"
NEW = "https://github.com/Aone2233/Nekomari/tree/main/docs"

changed = []
for path in sorted(glob.glob("frontend/src/i18n/locales/*.json")):
    text = open(path, encoding="utf-8").read()
    if OLD not in text:
        continue
    open(path, "w", encoding="utf-8").write(text.replace(OLD, NEW))
    changed.append(path)

for p in changed:
    print("updated", p)
print(f"{len(changed)} file(s) updated")

# Fail loudly if any upstream doc URL survives anywhere in the frontend.
leftover = []
for path in glob.glob("frontend/src/**/*", recursive=True):
    if not path.endswith((".ts", ".tsx", ".json", ".html")):
        continue
    try:
        text = open(path, encoding="utf-8").read()
    except (OSError, UnicodeDecodeError):
        continue
    for needle in ("komari-document.pages.dev", "komari.wiki"):
        if needle in text:
            leftover.append((path, needle))

if leftover:
    print("\nremaining upstream doc references:", file=sys.stderr)
    for path, needle in leftover:
        print(f"  {path}: {needle}", file=sys.stderr)
    sys.exit(1)
print("no upstream documentation URLs remain")
