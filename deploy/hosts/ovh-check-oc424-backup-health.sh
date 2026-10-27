#!/bin/sh
set -eu
[ "$#" -eq 0 ] || exit 64
umask 077
export PATH=/usr/bin:/bin
unset ENV BASH_ENV PYTHONPATH PYTHONHOME
exec /usr/bin/python3 -I /usr/local/lib/nekomari/backup_health.py inspect \
  --output-dir /srv/backups/oc424-encrypted --prefix oc424 \
  -- /usr/bin/ssh -F /dev/null -T -i /etc/nekomari-backup/oc424-health-push \
     -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes \
     -o UserKnownHostsFile=/etc/nekomari-backup/oc424-known-hosts \
     -o GlobalKnownHostsFile=/dev/null -o ConnectTimeout=12 \
     -o ServerAliveInterval=5 -o ServerAliveCountMax=2 \
     ubuntu@213.35.99.48
