"""Read-only, time-bracketed kernel snapshots for the MAC-WAN canary."""
import argparse
import json
import os
import time
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument("--seconds", type=int, default=120)
a = p.parse_args()
ignored = ("br", "cni", "docker", "podman", "flannel", "lo", "veth",
           "virbr", "vmbr", "tap", "fwbr", "fwpr")
names = sorted(n.name for n in Path("/sys/class/net").iterdir()
               if not n.name.startswith(ignored))
observations = []
# Verified current physical mounts on MAC-WAN; container/virtual mounts excluded.
mounts = ["/", "/boot", "/boot/efi"]


def socket_counts():
    counts = {}
    for name in ["tcp", "tcp6", "udp", "udp6"]:
        counts[name] = max(0, len(Path("/proc/net", name).read_text().splitlines()) - 1)
    return counts["tcp"] + counts["tcp6"], counts["udp"] + counts["udp6"]


deadline = time.monotonic() + a.seconds
while time.monotonic() < deadline:
    before = time.time()
    up = sum(int((Path("/sys/class/net") / n / "statistics/tx_bytes").read_text())
             for n in names)
    down = sum(int((Path("/sys/class/net") / n / "statistics/rx_bytes").read_text())
             for n in names)
    mem = {parts[0].rstrip(":"): int(parts[1]) * 1024
           for line in Path("/proc/meminfo").read_text().splitlines()
           if len(parts := line.split()) >= 2}
    disks = [os.statvfs(mount) for mount in mounts]
    tcp, udp = socket_counts()
    observations.append({"before": before, "after": time.time(), "up": up,
                         "down": down, "ram_total": mem["MemTotal"],
                         "ram_available_used": mem["MemTotal"] - mem["MemAvailable"],
                         "ram_htoplike_used": mem["MemTotal"] - mem["MemFree"]
                         - mem["Buffers"] - mem["Cached"] - mem["SReclaimable"] + mem["Shmem"],
                         "swap_total": mem["SwapTotal"],
                         "swap_used": mem["SwapTotal"] - mem["SwapFree"] - mem["SwapCached"],
                         "disk_total": sum(d.f_blocks * d.f_frsize for d in disks),
                         "disk_used": sum((d.f_blocks - d.f_bfree) * d.f_frsize for d in disks),
                         "connections_tcp": tcp, "connections_udp": udp})
    time.sleep(0.2)
print(json.dumps({"interfaces": names, "mounts": mounts, "snapshots": observations}))
