#!/bin/sh
# Fixed recipient-only producer for the dedicated, restricted OVH pull key.
set -eu
[ "$#" -eq 0 ] || exit 64
umask 077
export PATH=/usr/local/lib/nekomari/bin:/usr/bin:/bin
unset ENV BASH_ENV PYTHONPATH PYTHONHOME
scratch=$(mktemp -d /dev/shm/nekomari-encrypted-snapshot.XXXXXX)
trap 'rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
/usr/bin/python3 -I /usr/local/lib/nekomari/encrypted_backup.py create \
  --data-dir /opt/nekomari/data --panel-dir /opt/nekomari \
  --compose /opt/nekomari/docker-compose.yml \
  --recipients-file /etc/nekomari/backup.recipients \
  --work-dir "$scratch" --output - --timeout 240 \
  --max-archive-bytes 1073741824 --max-expanded-bytes 2147483648
