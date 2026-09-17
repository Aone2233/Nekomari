#!/usr/bin/env bash
# Re-apply each node's agent unit without --disable-web-ssh.
#
# The first rollout passed --disable-web-ssh to every agent (the installer's
# default at the time), which disabled the panel's terminal -- the agent flag
# covers "remote control (web ssh and rce)" as one switch. These nodes had it
# enabled before this fork's panel existed, so this restores that behaviour.
#
# TOKENS ARE NOT STORED HERE. This repository is public, and an earlier revision
# of this file embedded every node's agent token, which had to be rotated twice.
# Supply them in the environment as a node|token list, e.g.
#
#   export NEKOMARI_NODE_TOKENS='华纳云HN-JP1|xxxx
#   HK04|yyyy'
#   bash deploy/enable-webssh-all.sh
#
# Get each token from the panel, or:
#   POST /api/rpc2  {"jsonrpc":"2.0","method":"admin:getClientToken",
#                    "params":{"uuid":"<node-uuid>"},"id":1}
#
# Run from a host that can reach all of them (OC424 reaches the IPv6-only node).
set -uo pipefail

: "${NEKOMARI_NODE_TOKENS:?set NEKOMARI_NODE_TOKENS to a 'label|token' list}"

# label -> ssh target. Tokens come from the environment, matched by label.
targets() {
  case "$1" in
    "华纳云HN-JP1") echo "root@177.2.185.85" ;;
    "HK04")        echo "root@82.152.161.202" ;;
    "AkkoCloud")   echo "root@185.218.4.64" ;;
    "CloudLeadInno") echo "root@192.220.32.17" ;;
    "Nomao-v6")    echo "root@[2604:abc0:50::11:601e]" ;;
    *) echo "" ;;
  esac
}

# The flag lives in ExecStart; drop it and restart. Token is written to the unit
# on the host, never echoed.
while IFS='|' read -r label token; do
  [ -n "${label:-}" ] || continue
  target=$(targets "$label")
  if [ -z "$target" ]; then
    echo "== ${label}: unknown target, skipping"
    continue
  fi
  echo "== ${label} (${target}) =="

  printf '%s' "$token" > /tmp/.tok
  chmod 600 /tmp/.tok
  scp -q -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 \
      /tmp/.tok "${target}:/tmp/.tok" || { echo "  scp failed"; rm -f /tmp/.tok; continue; }

  # -n / </dev/null: ssh would otherwise swallow the rest of the loop's input.
  ssh -n -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20 "$target" \
    "T=\$(cat /tmp/.tok); U=/etc/systemd/system/nekomari-agent.service; \
     sed -i -E \"s/-t [A-Za-z0-9]{22}/-t \$T/\" \"\$U\"; \
     if grep -q -- '--disable-web-ssh' \"\$U\"; then \
       sed -i 's/ *--disable-web-ssh//' \"\$U\"; echo '  web-ssh flag removed'; \
     else echo '  web-ssh flag already absent'; fi; \
     systemctl daemon-reload && systemctl restart nekomari-agent && sleep 8; \
     echo \"  state: \$(systemctl is-active nekomari-agent)\"; \
     rm -f /tmp/.tok" < /dev/null
  rm -f /tmp/.tok
done <<< "$NEKOMARI_NODE_TOKENS"

echo
echo "done — MAC-WAN and PZYC use their own units (no passwordless sudo / user unit)"
