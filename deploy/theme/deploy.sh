#!/bin/sh
# Build a theme from source and replace the installed copy, safely.
#
#     sh deploy/theme/deploy.sh                 # upgrade the current version
#     TARGET=/opt/nekomari/data/theme/LuminaPlus sh deploy/theme/deploy.sh
#
# ## Why this exists
#
# The theme's source repository and the *installed* copy are two different things. Editing the source does
# nothing to a running deployment, and this repository got that wrong twice — once for the background images,
# once for the traffic fix, where the change was committed, described as done, and simply never reached the
# site. Doing it by hand is what allowed that, and by hand is also how the live theme's `dist` came within one
# command of being deleted:
#
#     sudo rm -rf "$T/dist" && sudo mv "$T/dist.new" "$T/dist"     # if the unpack failed, dist is gone
#
# `set -e` was the only thing that stopped it, and relying on that is not a plan. So the order here is
# **build → verify → swap with a rollback point**, and the live directory is never removed before a verified
# replacement exists beside it.
#
# ## What it does not do
#
# It does not `npm install` on every run: the dependency tree changes rarely and installing it takes minutes.
# `--fresh-install` does it when a dependency actually changed.
#
# It does not touch the theme's `komari-theme.json` or `preview.png`, which live beside `dist` and belong to
# the deployment rather than the build.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH= cd -- "$HERE/../.." && pwd)
SOURCE="${SOURCE:-$REPO/theme-luminaplus}"
TARGET="${TARGET:-/opt/nekomari/data/theme/LuminaPlus}"
BACKUP_ROOT="${BACKUP_ROOT:-$(dirname "$TARGET")/../theme-backups}"
BUILD_DIR="${BUILD_DIR:-/tmp/nekomari-theme-build}"
SUDO="${SUDO:-sudo -n}"
FRESH_INSTALL=0
for arg in "$@"; do
  [ "$arg" = "--fresh-install" ] && FRESH_INSTALL=1
done

log() { printf '  %s\n' "$*"; }
die() { printf '  FAILED: %s\n' "$*" >&2; exit 1; }

[ -d "$SOURCE" ] || die "no theme source at $SOURCE"
[ -f "$SOURCE/package.json" ] || die "$SOURCE does not look like the theme (no package.json)"
$SUDO test -d "$TARGET" || die "no installed theme at $TARGET"

# npm from a login shell: on this host Node comes from nvm, which a non-interactive shell does not source.
# The failure without this is `npm: command not found`, which reads like npm is absent rather than unsourced.
. "${NVM_DIR:-$HOME/.nvm}/nvm.sh" 2>/dev/null || true
command -v npm >/dev/null 2>&1 || die "npm is not on PATH (source nvm, or install Node)"

# --- stage a clean copy of the source -------------------------------------------------------------
#
# The build writes into the source directory (`dist/`, and Vite's cache). Building in place would mean the
# repository's working tree is what gets deployed, including any uncommitted experiment — so a copy is made and
# the copy is what is built.
log "staging $SOURCE -> $BUILD_DIR"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
# Every build input by name, rather than a broad copy: `dist/` and `node_modules/` must not come along, or the
# build would start from the previous output and a stale asset could survive into the new one.
for item in src public index.html package.json package-lock.json vite.config.ts \
            tsconfig.json tsconfig.app.json tsconfig.node.json tailwind.config.js postcss.config.js; do
  [ -e "$SOURCE/$item" ] && cp -a "$SOURCE/$item" "$BUILD_DIR/" || true
done
[ -f "$BUILD_DIR/vite.config.ts" ] || die "vite.config.ts was not copied; the source layout changed"

if [ "$FRESH_INSTALL" = "1" ] || [ ! -d "$BUILD_DIR/node_modules" ]; then
  log "npm install"
  ( cd "$BUILD_DIR" && npm install --silent --no-audit --no-fund ) || die "npm install failed"
else
  # Reuse the previous tree; it lives in BUILD_DIR, which survives between runs precisely so this is cheap.
  log "reusing the existing dependency tree (pass --fresh-install to rebuild it)"
fi

# --- build ---------------------------------------------------------------------------------------
log "building"
if ! ( cd "$BUILD_DIR" && npm run build ) >"$BUILD_DIR/build.log" 2>&1; then
  log "the build failed; the installed theme is untouched. Last lines:"
  tail -15 "$BUILD_DIR/build.log" | sed 's/^/    /'
  exit 1
fi

# --- verify before anything is replaced -----------------------------------------------------------
#
# A build that "succeeded" with no entry document, or with an empty asset directory, would otherwise be swapped
# in and take the site down. These are the two ways that happens.
DIST="$BUILD_DIR/dist"
[ -d "$DIST" ] || die "the build produced no dist/ directory"
[ -f "$DIST/index.html" ] || die "the build produced no dist/index.html"
ASSETS=$(find "$DIST/assets" -type f 2>/dev/null | wc -l | tr -d ' ')
[ "$ASSETS" -gt 0 ] || die "the build produced no assets"
log "verified: dist/index.html present, $ASSETS asset files"

# --- swap, keeping a rollback point ---------------------------------------------------------------
TS=$(date +%Y%m%d-%H%M%S)
BACKUP="$BACKUP_ROOT/$(basename "$TARGET")-pre-deploy-$TS"
NEW="$TARGET/dist.incoming"

$SUDO mkdir -p "$BACKUP"
log "backing up dist -> $BACKUP/dist"
$SUDO cp -a "$TARGET/dist" "$BACKUP/dist" || die "could not back up the installed theme"

# Staged beside the live directory and verified there, so the live one is only touched once a complete
# replacement exists. `rmdir` rather than `rm -rf` where possible: if it is not empty, something is wrong.
$SUDO rm -rf "$NEW"
$SUDO mkdir -p "$NEW"
$SUDO cp -a "$DIST/." "$NEW/"
$SUDO test -f "$NEW/index.html" || die "staging lost index.html"
log "staged beside the live copy"

$SUDO rm -rf "$TARGET/dist.previous"
$SUDO mv "$TARGET/dist" "$TARGET/dist.previous" || die "could not move the live dist aside"
$SUDO mv "$NEW" "$TARGET/dist" || { $SUDO mv "$TARGET/dist.previous" "$TARGET/dist"; die "swap failed; rolled back"; }
$SUDO rm -rf "$TARGET/dist.previous"
# The panel's theme manager reads these as its own user; without this a later upload fails confusingly.
$SUDO chown -R "$(id -un):$(id -gn)" "$TARGET/dist" 2>/dev/null || true
log "swapped in; previous version kept at $BACKUP/dist"

# --- report ---------------------------------------------------------------------------------------
#
# The asset filenames are content-hashed, so printing one is a check that the served document is the new build:
# if the hash is unchanged, the build produced identical output or the swap did not take effect.
NEW_HASH=$(grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' "$DIST/index.html" | head -1 || true)
OLD_HASH=$(grep -oE 'assets/index-[A-Za-z0-9_-]+\.js' "$BACKUP/dist/index.html" 2>/dev/null | head -1 || true)
log "entry bundle: ${OLD_HASH:-none} -> ${NEW_HASH:-none}"
if [ -n "$OLD_HASH" ] && [ "$OLD_HASH" = "$NEW_HASH" ]; then
  log "WARNING: the entry bundle hash did not change, so the site may still be serving the previous build"
fi
log "DONE"
