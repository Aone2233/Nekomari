#!/usr/bin/env python3
"""Build a local Aero keyboard-focus candidate from the pinned market ZIP."""
import argparse
import hashlib
from pathlib import Path
import zipfile

EXPECTED = 'fd27cf3e0dae2bad8d58a31a3ed303a101816cc43626a7b9a0a1c6fb19758233'
SCRIPT = 'dist/assets/v1.4.5/app.js'
BEFORE = b"$('cards').addEventListener('focusin',e=>{const anchor=e.target.closest('.node-card');if(anchor){peekMode='keyboard';showNodePeek(anchor);}});"
AFTER = b"$('cards').addEventListener('focusin',e=>{const anchor=e.target.closest('.node-card');if(anchor&&anchor.matches(':focus-visible')){peekMode='keyboard';showNodePeek(anchor);}});"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    if args.source.resolve() == args.output.resolve():
        parser.error('output must differ from source')
    if hashlib.sha256(args.source.read_bytes()).hexdigest() != EXPECTED:
        parser.error('source ZIP does not match the pinned market archive')
    with zipfile.ZipFile(args.source) as src:
        names = src.namelist()
        if names.count(SCRIPT) != 1 or len(names) != len(set(names)):
            parser.error('unexpected archive structure')
        script = src.read(SCRIPT)
        if script.count(BEFORE) != 1:
            parser.error('expected focus handler not found exactly once')
        with args.output.open('xb') as output, zipfile.ZipFile(output, 'w') as dst:
            for item in src.infolist():
                content = src.read(item)
                if item.filename == SCRIPT:
                    content = content.replace(BEFORE, AFTER)
                dst.writestr(item, content)
    print(f'{hashlib.sha256(args.output.read_bytes()).hexdigest()}  {args.output}')


if __name__ == '__main__':
    main()
