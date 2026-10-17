#!/usr/bin/env bash
# Keep only the N newest pre-upgrade backups in the panel's data/backup directory.
#
# Why this exists: the server writes data/backup/upgrade-<ts>.zip on every version
# change, 60-100 MB each. Four days of upgrades left 17 files and 1.4 GB in there
# (measured 2026-09-21). Keeping the newest few is the rollback safety net; the rest
# is dead weight on the same disk as the databases.
#
# Survivors are verified as readable zips before anything is deleted, so a listing
# alone can never cost the last good copy. A corrupt survivor is reported and kept:
# it must not abort the prune, or one bad archive left in the directory would block
# disk cleanup forever (and the file it protects is kept either way).
#
# Usage: prune-upgrade-backups.sh [--keep N] [--dir DIR] [--dry-run]
set -euo pipefail

KEEP=3
DIR=/opt/nekomari/data/backup
DRY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --keep)     KEEP="${2:?--keep needs a value}"; shift 2 ;;
    --keep=*)   KEEP="${1#*=}"; shift ;;
    --dir)      DIR="${2:?--dir needs a value}"; shift 2 ;;
    --dir=*)    DIR="${1#*=}"; shift ;;
    --dry-run)  DRY=1; shift ;;
    -h|--help)  sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

case "$KEEP" in ''|*[!0-9]*) echo "--keep must be a number" >&2; exit 2 ;; esac
[ "$KEEP" -ge 1 ] || { echo "--keep must be at least 1" >&2; exit 2; }
[ -d "$DIR" ] || { echo "no such directory: $DIR" >&2; exit 1; }

mapfile -t files < <(ls -1t "$DIR"/upgrade-*.zip 2>/dev/null || true)
echo "found ${#files[@]} pre-upgrade backups in $DIR (keeping $KEEP)"
if [ "${#files[@]}" -le "$KEEP" ]; then
  echo "nothing to prune"
  exit 0
fi

unreadable=0
for f in "${files[@]:0:$KEEP}"; do
  if command -v python3 >/dev/null 2>&1; then
    # `if !` 而不是裸跑：在 set -e 下，一个坏档（或 zipfile 抛出的任何异常）会
    # 中止整个脚本 —— 而这一段在「保留」循环里，于是连过期备份都不会被删，
    # 磁盘会一直涨，运维看到的只是一段 Python traceback。
    if ! python3 - "$f" <<'PY'
import sys, zipfile
bad = zipfile.ZipFile(sys.argv[1]).testzip()
if bad:
    raise SystemExit("corrupt entry: %s" % bad)
PY
    then
      echo "  WARN    $(basename "$f") is not a readable zip; keeping it anyway" >&2
      unreadable=$((unreadable + 1))
    fi
  fi
  echo "  keep    $(basename "$f") ($(du -h "$f" | cut -f1))"
done
if [ "$unreadable" -gt 0 ]; then
  echo "warning: $unreadable of the kept archives could not be read; they were kept, not deleted" >&2
fi

for f in "${files[@]:$KEEP}"; do
  if [ "$DRY" = 1 ]; then
    echo "  would remove $(basename "$f")"
  else
    rm -f "$f"
    echo "  removed $(basename "$f")"
  fi
done

if [ "$DRY" != 1 ]; then
  df -h "$DIR" | tail -1
fi
