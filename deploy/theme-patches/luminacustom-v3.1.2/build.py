#!/usr/bin/env python3
"""Create an explicitly local LuminaCustom patch from the pinned market ZIP."""
import argparse
import hashlib
from pathlib import Path
import zipfile

EXPECTED = "56784c204656a6310867ca6671f210dfcbaef935c1d41c537765fa882cbdd001"
CSS = "dist/assets/index-RHdai548.css"

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    if args.source.resolve() == args.output.resolve():
        parser.error("output must differ from source")
    data = args.source.read_bytes()
    if hashlib.sha256(data).hexdigest() != EXPECTED:
        parser.error("source ZIP does not match the pinned market archive")
    patch = (Path(__file__).parent / "mobile-tooltip.css").read_bytes()
    with zipfile.ZipFile(args.source) as src:
        names = src.namelist()
        if names.count(CSS) != 1 or len(names) != len(set(names)):
            parser.error("unexpected archive structure")
        with args.output.open("xb") as out:
            with zipfile.ZipFile(out, "w") as dst:
                for item in src.infolist():
                    content = src.read(item)
                    if item.filename == CSS:
                        content += b"\n" + patch
                    dst.writestr(item, content)
    print(f"{hashlib.sha256(args.output.read_bytes()).hexdigest()}  {args.output}")

if __name__ == "__main__":
    main()
