# Retiring CF-Server-Monitor

On 2026-09-17 the CF-Server-Monitor stack was retired fleet-wide. This records
what was removed, what was deliberately left, and how to undo it.

## Why this needed care

Two monitoring systems were running side by side on **all nine** nodes, and they
are easy to confuse:

| | `cf-probe` | `nekomari-agent` |
|---|---|---|
| Reports to | `monitor.orderly2233.org` (CF-Server-Monitor) | `komari.orderly2233.org` (this panel) |
| Unit | `cf-probe.service` (system, root) | `nekomari-agent.service` (system, or **user** on MAC-WAN) |
| Traffic accounting | `RESET_DAY=1` | `--month-rotate <billing day>` |

`cf-probe` was not leftover debris from the old Komari setup — it was the
platform that *replaced* it, and it was live production monitoring. It was also
in the MAC-WAN watchdog's service list, so simply stopping it would have raised
an alert every six hours.

## Order of operations

1. **Watchdog first**, so stopping the probes could not trigger false alarms.
2. Nodes, one at a time, each verified before moving on.
3. DNS last.

## What was removed, per node

- `systemctl disable --now cf-probe` — disabled, not masked, so rollback is easy
- `/usr/local/bin/cf-probe` deleted
- `/etc/config/cf-probe/` deleted
- The unit file **kept** (disabled) as a rollback aid

Everything is backed up before deletion to `/root/cf-probe-retired-<timestamp>/`:

```
_etc_config_cf-probe_config.conf
_etc_config_cf-probe_traffic.dat
_etc_systemd_system_cf-probe.service
_usr_local_bin_cf-probe
```

Rollback on any node:

```bash
sudo cp /root/cf-probe-retired-*/_usr_local_bin_cf-probe /usr/local/bin/cf-probe
sudo mkdir -p /etc/config/cf-probe
sudo cp /root/cf-probe-retired-*/_etc_config_cf-probe_* /etc/config/cf-probe/
sudo systemctl enable --now cf-probe
```

## Watchdog changes (`macwan_health_check.py`)

- `cf-probe` removed from the service list; `nekomari-agent` added.
- The probe-freshness check no longer reads `cf-probe`'s `traffic.dat`. It now
  looks for `Get IPV4 Success` in the agent's journal.
  Deliberately **not** `Basic info uploaded successfully`: `UpdateBasicInfo()` is
  called once from `root.go`, while the 10-minute ticker calls `uploadBasicInfo()`
  without logging — using it produced a false "111 minutes stale" alert.
- User-level units are now queried through
  `sudo -u macos env XDG_RUNTIME_DIR=/run/user/1000 systemctl --user …`.
  Previously the script ran `systemctl --user` as root, which fails with
  "Failed to connect to bus: No medium found", so `hermes-gateway` read as
  `unknown` and **the watchdog had been alerting on it every run**.

## What could not be removed

`monitor.orderly2233.org` is an `AAAA 100::` record, the Cloudflare placeholder
that binds a Worker to a hostname. Deleting it fails with:

```
Unable to edit this record as this has been configured as read only.
```

Worker-managed records cannot be edited through the DNS API, and the token
available here is DNS-scoped (a `purge_cache` call returns
`Authentication error`). **The Worker and its route must be removed in the
Cloudflare dashboard** (Workers & Pages). With every probe retired the endpoint
now receives no traffic, so this is tidiness rather than function.

## Verifying

`deploy/verify-cf-probe-retired.sh` checks each node for: cf-probe inactive and
disabled, binary gone, a rollback backup present, and the Nekomari agent still
active. Expected output per node:

```
cf-probe=inactive enabled=disabled binary=gone backup=1 agent=active
```
