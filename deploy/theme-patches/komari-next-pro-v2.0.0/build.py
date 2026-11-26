#!/usr/bin/env python3
"""Build a local Next Pro variant from the pinned source and market ZIP."""
import argparse
import hashlib
from pathlib import Path
import re
import subprocess
import tempfile
import zipfile

COMMIT = "d2e23107fc16f37bffd738f16a6598e742e90cfb"
ARCHIVE_SHA256 = "e557a5fe89eab5ec0b553f631511e499a01b611481e13771e8c5d3f8268b50ba"
SCRIPTS = [
    r'<script src="/_next/static/chunks/komari-mood-v21\.js" defer></script>',
    r'<script src="/_next/static/chunks/komari-asset-fix\.js" defer></script>',
    r'<script data-k="f1a3">.*?</script>',
    r'<script data-k="b2c7">.*?</script>',
    r'<script data-k="w9d1">.*?</script>',
    r'<script id="komari-expiry-timeline">.*?</script>',
]

def run(*command, cwd=None):
    subprocess.run(command, cwd=cwd, check=True)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("original", type=Path, help="hash-verified market ZIP")
    parser.add_argument("output", type=Path, help="new local ZIP; must not exist")
    args = parser.parse_args()
    if hashlib.sha256(args.original.read_bytes()).hexdigest() != ARCHIVE_SHA256:
        parser.error("source ZIP does not match the pinned market archive")
    if args.output.exists():
        parser.error("output already exists")
    patch = Path(__file__).with_name("nekomari.patch").resolve()
    with tempfile.TemporaryDirectory(prefix="next-pro-build-") as temp:
        source = Path(temp) / "source"
        run("git", "clone", "--quiet", "--depth", "1", "--branch", "v2.0.0", "https://github.com/fanchengliu/komari-next-pro.git", str(source))
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip()
        if head != COMMIT:
            raise RuntimeError("upstream tag no longer points to the pinned commit")
        run("git", "apply", str(patch), cwd=source)
        theme = source / "theme"
        run("npm", "ci", "--no-audit", "--no-fund", cwd=theme)
        run("npm", "run", "build", cwd=theme)
        dist = theme / "dist"
        with zipfile.ZipFile(args.original) as original:
            html = original.read("dist/index.html").decode("utf-8")
            extras = []
            for pattern in SCRIPTS:
                matches = re.findall(pattern, html, re.DOTALL)
                if len(matches) != 1:
                    raise RuntimeError("market archive scripts do not match expected structure")
                extras.append(matches[0])
            index = dist / "index.html"
            rebuilt = index.read_text(encoding="utf-8")
            if rebuilt.count("</body>") != 1:
                raise RuntimeError("rebuilt page has unexpected structure")
            index.write_text(rebuilt.replace("</body>", "".join(extras) + "</body>"), encoding="utf-8")
            for name in ("komari-mood-v21.js", "komari-asset-fix.js"):
                path = dist / "_next" / "static" / "chunks" / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(original.read("dist/_next/static/chunks/" + name))
            with args.output.open("xb") as out, zipfile.ZipFile(out, "w", compression=zipfile.ZIP_DEFLATED) as result:
                for info in original.infolist():
                    if not info.filename.startswith("dist/"):
                        result.writestr(info, original.read(info))
                for path in sorted(dist.rglob("*")):
                    if path.is_file():
                        result.write(path, "dist/" + path.relative_to(dist).as_posix())
    print(f"{hashlib.sha256(args.output.read_bytes()).hexdigest()}  {args.output}")

if __name__ == "__main__":
    main()
