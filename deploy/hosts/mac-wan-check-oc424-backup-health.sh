#!/bin/sh
# Report actual inspected ciphertext status; never manufacture a fresh heartbeat.
set -eu
[ "$#" -eq 0 ] || exit 64
umask 077
export PATH=/usr/bin:/bin
unset ENV BASH_ENV PYTHONPATH PYTHONHOME
base=/home/macos/.local/share/nekomari-recovery
exec /usr/bin/python3 -I "$base/backup_health.py" inspect \
  --output-dir "$base/scheduled" --prefix oc424 \
  -- /usr/bin/ssh -F "$base/credentials/oc424-proxy-ssh-config" -T -i "$base/credentials/oc424-health-push" \
     -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes \
     -o UserKnownHostsFile="$base/credentials/oc424-known-hosts" \
     -o GlobalKnownHostsFile=/dev/null -o ConnectTimeout=12 \
     -o ServerAliveInterval=5 -o ServerAliveCountMax=2 \
     oc424-backup-producer
