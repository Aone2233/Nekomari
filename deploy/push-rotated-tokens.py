#!/usr/bin/env python3
"""Push freshly rotated agent tokens to their nodes and restart the agents.

Reads label->token from /tmp/rotated.txt (written by the rotation step) so no
token is ever printed or committed. Run from OC424.
"""
import subprocess

toks = {}
for line in open("/tmp/rotated.txt"):
    parts = line.split()
    if len(parts) >= 2 and parts[0] in ("MACWAN", "OC424", "PZYC"):
        toks[parts[0]] = parts[1]

if len(toks) != 3:
    raise SystemExit(f"expected 3 tokens, got {sorted(toks)}")

SSH = "ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=20"
SED = "sed -i -E 's/-t [A-Za-z0-9]{22}/-t %s/'"


def sh(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True)


# OC424: local, system unit
sh("sudo -n " + (SED % toks["OC424"]) +
   " /etc/systemd/system/komari-agent-oc424-original-node.service")
sh("sudo -n systemctl daemon-reload && sudo -n systemctl restart komari-agent-oc424-original-node")
print("OC424  :", sh("systemctl is-active komari-agent-oc424-original-node").stdout.strip())

# MAC-WAN: user unit
sh(f"{SSH} MAC-WAN \"{SED % toks['MACWAN']} ~/.config/systemd/user/nekomari-agent.service"
   " && systemctl --user daemon-reload && systemctl --user restart nekomari-agent\"")
print("MAC-WAN:", sh(f"{SSH} MAC-WAN 'systemctl --user is-active nekomari-agent'").stdout.strip())

# PZYC: non-root + sudo
sh(f"{SSH} PZYC \"sudo -n {SED % toks['PZYC']} /etc/systemd/system/nekomari-agent.service"
   " && sudo -n systemctl daemon-reload && sudo -n systemctl restart nekomari-agent\"")
print("PZYC   :", sh(f"{SSH} PZYC 'sudo -n systemctl is-active nekomari-agent'").stdout.strip())
