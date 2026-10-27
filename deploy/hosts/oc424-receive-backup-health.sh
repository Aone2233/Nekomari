#!/bin/sh
# Dedicated forced-command key: bounded non-secret stdin, one fixed destination.
set -eu
[ "$#" -eq 0 ] || exit 64
umask 077
export PATH=/usr/bin:/bin
unset ENV BASH_ENV PYTHONPATH PYTHONHOME
exec /usr/bin/timeout --signal=KILL 10s /usr/bin/python3 -I /usr/local/lib/nekomari/backup_health.py ingest \
  --output /opt/nekomari/data/backup-health/ovh.json
