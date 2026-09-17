#!/usr/bin/env bash
# Push the current rotated tokens to MAC-WAN and PZYC, whose agents did not get
# them, then confirm each reconnects.
#
# Reads label->token from /tmp/rt.txt so no token appears in this file or in the
# process list. Run from OC424.
#
# Note: the ssh options are written inline at each call rather than stored in a
# variable -- an unquoted "$SSH_OPTS" word-splits in a way that made ssh treat the
# hostname as an option argument ("Could not resolve hostname mac-wan").
set -uo pipefail

TOK_MACWAN=$(awk '$1=="MACWAN"{print $2}' /tmp/rt.txt)
TOK_PZYC=$(awk '$1=="PZYC"{print $2}' /tmp/rt.txt)
if [ -z "$TOK_MACWAN" ] || [ -z "$TOK_PZYC" ]; then
  echo "missing tokens in /tmp/rt.txt"; exit 1
fi

echo "== MAC-WAN (user unit) =="
ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 MAC-WAN \
  "sed -i -E 's/-t [A-Za-z0-9]{22}/-t ${TOK_MACWAN}/' ~/.config/systemd/user/nekomari-agent.service && \
   systemctl --user daemon-reload && systemctl --user restart nekomari-agent && sleep 10 && \
   echo \"  state: \$(systemctl --user is-active nekomari-agent)\" && \
   journalctl --user -u nekomari-agent --no-pager -n 3 | tail -2 | sed 's/^/  /'"

echo "== PZYC (sudo) =="
ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 PZYC \
  "sudo -n sed -i -E 's/-t [A-Za-z0-9]{22}/-t ${TOK_PZYC}/' /etc/systemd/system/nekomari-agent.service && \
   sudo -n systemctl daemon-reload && sudo -n systemctl restart nekomari-agent && sleep 10 && \
   echo \"  state: \$(sudo -n systemctl is-active nekomari-agent)\" && \
   sudo -n journalctl -u nekomari-agent --no-pager -n 3 | tail -2 | sed 's/^/  /'"
