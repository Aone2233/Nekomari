#!/usr/bin/env bash
# Retire cf-probe (CF-Server-Monitor) on one host, reversibly.
#
# Two systems are being separated here: cf-probe reports to
# monitor.orderly2233.org, the Nekomari agent reports to komari.orderly2233.org.
# This touches only cf-probe.
#
# Everything removed is first copied to a backup directory, and the unit is
# disabled rather than deleted, so a rollback is: restore the files, re-enable.
#
# Also reports any *other* trace of the CF stack on the host (a second copy of the
# binary, a cfsm install dir, cron entries, MOTD/banner references), because a
# retired probe that keeps running from a different unit would defeat the point.
#
# Usage: retire-cf-probe.sh [--dry-run]
set -uo pipefail

DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1

STAMP=$(date +%Y%m%d-%H%M%S)
BACKUP="/root/cf-probe-retired-${STAMP}"

log() { printf '  %s\n' "$*"; }
run() {
  if [ "$DRY" = 1 ]; then log "[dry-run] $*"; else eval "$@"; fi
}

echo "== retiring cf-probe =="

# 1. Stop and disable the unit(s). Disable keeps the unit file for rollback.
UNITS=$(systemctl list-unit-files --type=service --no-pager --plain 2>/dev/null \
        | awk '{print $1}' | grep -E '^cf-probe' || true)
if [ -z "$UNITS" ]; then
  log "no cf-probe unit found"
else
  for u in $UNITS; do
    log "unit $u: $(systemctl is-active "$u" 2>/dev/null || echo n/a) / $(systemctl is-enabled "$u" 2>/dev/null || echo n/a)"
    run "systemctl disable --now '$u' >/dev/null 2>&1 || true"
    log "disabled and stopped $u"
  done
fi

# 2. Back up everything before removing it.
run "mkdir -p '$BACKUP'"
for f in /etc/config/cf-probe/config.conf /etc/config/cf-probe/traffic.dat \
         /etc/systemd/system/cf-probe.service /usr/local/bin/cf-probe; do
  if [ -e "$f" ]; then
    run "cp -a '$f' '$BACKUP/$(echo "$f" | tr '/' '_')'"
    log "backed up $f"
  fi
done

# 3. Remove the binary and config so it cannot be restarted by accident.
run "rm -f /usr/local/bin/cf-probe"
run "rm -rf /etc/config/cf-probe"
log "removed binary and config (unit file kept, disabled)"

# 4. Sweep for anything else that would keep reporting.
echo "  -- residual traces --"
for path in /opt/cfsm /usr/local/bin/cfsm-agent /etc/cfsm /var/lib/cf-probe; do
  [ -e "$path" ] && log "STILL PRESENT: $path"
done
CRON=$( { crontab -l 2>/dev/null; ls /etc/cron.d 2>/dev/null | sed 's/^/cron.d: /'; } | grep -iE 'cf-probe|cfsm' || true)
[ -n "$CRON" ] && log "cron references: $CRON"
OTHER=$(find / -xdev -name 'cf-probe*' -o -xdev -name 'cfsm*' 2>/dev/null | grep -v "^$BACKUP" | head -10)
if [ -n "$OTHER" ]; then
  log "other files found:"
  echo "$OTHER" | sed 's/^/    /'
else
  log "no other cf-probe/cfsm files on this host"
fi

echo "  backup: $BACKUP"
echo "  state now: $(systemctl is-active cf-probe 2>/dev/null || echo 'not running')"
echo "  DONE"
