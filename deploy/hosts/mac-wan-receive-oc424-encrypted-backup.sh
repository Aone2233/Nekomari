#!/bin/sh
# MAC-WAN user service: the SSH key can invoke only the fixed encrypted producer.
set -eu
[ "$#" -eq 0 ] || exit 64
umask 077
export PATH=/usr/bin:/bin
unset ENV BASH_ENV PYTHONPATH PYTHONHOME
base=/home/macos/.local/share/nekomari-recovery
exec /usr/bin/python3 -I "$base/encrypted_backup.py" receive \
  --output-dir "$base/scheduled" --prefix oc424 --keep 14 \
  --timeout 300 --max-archive-bytes 1073741824 --max-expanded-bytes 2147483648 \
  -- /usr/bin/ssh -F "$base/credentials/oc424-proxy-ssh-config" -T -i "$base/credentials/oc424-backup-pull" \
     -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes \
     -o UserKnownHostsFile="$base/credentials/oc424-known-hosts" \
     -o GlobalKnownHostsFile=/dev/null -o ConnectTimeout=12 \
     -o ServerAliveInterval=15 -o ServerAliveCountMax=2 \
     oc424-backup-producer
