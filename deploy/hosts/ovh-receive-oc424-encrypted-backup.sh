#!/bin/sh
# Opt-in canary receiver; this file does not enable or change a scheduler.
set -eu
[ "$#" -eq 0 ] || exit 64
umask 077
export PATH=/usr/bin:/bin
unset ENV BASH_ENV PYTHONPATH PYTHONHOME
exec /usr/bin/python3 -I /usr/local/lib/nekomari/encrypted_backup.py receive \
  --output-dir /srv/backups/oc424-encrypted --prefix oc424 --keep 14 \
  --timeout 300 --max-archive-bytes 1073741824 --max-expanded-bytes 2147483648 \
  -- /usr/bin/ssh -F /dev/null -T -i /etc/nekomari-backup/oc424-encrypted-pull \
     -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes \
     -o UserKnownHostsFile=/etc/nekomari-backup/oc424-known-hosts \
     -o GlobalKnownHostsFile=/dev/null -o ConnectTimeout=12 \
     -o ServerAliveInterval=15 -o ServerAliveCountMax=2 \
     ubuntu@213.35.99.48
