#!/usr/bin/env bash
# Push the tokens from /tmp/rotated2.txt into each node's agent unit and restart.
#
# Reads the token inside the remote command from a file it copies over, so the
# value never appears in this script, in the local process list, or in a
# transcript. Run from OC424.
set -uo pipefail

MAP=/tmp/rotated2.txt
[ -f "$MAP" ] || { echo "missing $MAP"; exit 1; }

while IFS='|' read -r label target unit needs_sudo token; do
  [ -n "$label" ] || continue
  echo "== ${label} (${target}) =="

  # Ship the token as a file rather than interpolating it into the ssh command
  # line, which keeps it out of the remote process table too.
  printf '%s' "$token" > /tmp/.tok
  chmod 600 /tmp/.tok
  scp -q -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 \
      /tmp/.tok "${target}:/tmp/.tok" || { echo "  scp failed"; continue; }

  # `ssh` inherits the loop's stdin and would swallow the rest of the map file,
  # so the loop stops after the first host. Redirect its stdin from /dev/null
  # (and pass -n, which does the same for the ssh client itself).
  ssh -n -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 "$target" \
    "T=\$(cat /tmp/.tok); sed -i -E \"s/-t [A-Za-z0-9]{22}/-t \$T/\" '${unit}' && \
     systemctl daemon-reload && systemctl restart nekomari-agent && sleep 10 && \
     echo \"  state: \$(systemctl is-active nekomari-agent)\" && \
     echo \"  token prefix: \$(grep -oE -- '-t [A-Za-z0-9]{4}' '${unit}' | head -1)…\" && \
     journalctl -u nekomari-agent --no-pager -n 2 | tail -1 | sed 's/^/  /' && \
     rm -f /tmp/.tok" < /dev/null
  rm -f /tmp/.tok
done < "$MAP"

echo
echo "done"
