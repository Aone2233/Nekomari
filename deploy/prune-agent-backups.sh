#!/bin/bash
# Keep only the newest rollback copy of the agent binary in each agent directory.
# Manual upgrades left 8-9 MB *.pre-v* copies; the ledger is retention-bounded, these are not.
set -euo pipefail
apply=0
dirs=()
for arg in "$@"; do
  case "$arg" in
    --apply) apply=1 ;;
    --dir=*) dirs+=("${arg#--dir=}") ;;
    -h|--help) echo "usage: prune-agent-backups.sh [--apply] [--dir=<agent-dir>]..."; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done
if [ "${#dirs[@]}" -eq 0 ]; then
  for candidate in /opt/nekomari-agent /root/nekomari-agent /home/*/nekomari-agent /home/*/komari-agent; do
    [ -d "$candidate" ] && dirs+=("$candidate")
  done
fi
removed=0
kept=0
for dir in "${dirs[@]}"; do
  [ -d "$dir" ] || continue
  mapfile -t backups < <(find "$dir" -maxdepth 1 -type f -name "komari-agent-*" \( -name "*.pre-v*" -o -name "*.bak-*" -o -name "*.old" \) -printf "%T@ %p\n" | sort -nr | cut -d" " -f2-)
  [ "${#backups[@]}" -eq 0 ] && continue
  kept=$((kept + 1))
  for old in "${backups[@]:1}"; do
    if [ "$apply" -eq 1 ]; then rm -f -- "$old"; echo "removed $old"; else echo "would remove $old"; fi
    removed=$((removed + 1))
  done
done
echo "removed=$removed kept-newest=$kept"
