# Deploy verification (MAC-WAN)

How the v0.1.1 release was verified against a **real host, real agent, real
notification channel** — and what that verification found.

Everything below was executed on **MAC-WAN** (`macos@59.66.23.138:52022`, Ubuntu
24.04.5, x86_64). The existing production monitoring on that host
(`cf-probe` → CF-Server-Monitor, plus an unrelated `thu-electric-monitor` Docker
stack) was **not touched**.

## What was deployed

| Piece | Detail |
|---|---|
| Instance | Docker container `nekomari-test`, **host network**, port **25775** |
| Image | `ghcr.io/aone2233/nekomari:v0.1.2` (anonymous pull) |
| Data | named volume `nekomari-test-data` → `/app/data` |
| Agent | `komari-agent-linux-amd64` v0.1.1, systemd **user** service `komari-agent-test` |
| Notification sink | `webhook_receiver.py` on `:25999` (artificial channel, records JSONL) |
| Real channel | Telegram bot `@THU_Elec_BW2233_bot` → chat `5847308461` |

The verification ran against the published **v0.1.1** image; the instance was
afterwards moved to **v0.1.2**, which contains the fix below and is otherwise
byte-identical (no Go code changed between the two tags, only the Dockerfile and
workflows). The binary inside the v0.1.1 image was
`sha256 fd0be077f14bb8c65d01dbfc9c3223c008b0013b1a8334bb5ed73db6d5776fe9`,
i.e. exactly the `nekomari-linux-amd64` release asset, so what was verified is
what users download.

Port 25775 was chosen so it cannot collide with a default (`25774`) instance.
`--network host` was used so the panel sees the agent's real IP rather than a
bridge address.

## Reproducing

```bash
# 1. server
docker run -d --name nekomari-test --network host --restart unless-stopped \
  -e KOMARI_LISTEN=0.0.0.0:25775 \
  -v nekomari-test-data:/app/data \
  ghcr.io/aone2233/nekomari:latest

# 2. first-run install is a web API, so it can be driven non-interactively
#    (metric DSN is required — it will not default)
curl -s -X POST http://127.0.0.1:25775/api/install/complete \
  -H 'Content-Type: application/json' -d '{
    "username":"admin","password":"<8+ chars, upper+lower+digit>",
    "sitename":"...","description":"...","metric_dsn":"./data/metrics.db"}'

# 3. agent token
curl -s -c cookie.txt -X POST http://127.0.0.1:25775/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"..."}'
curl -s -b cookie.txt -X POST http://127.0.0.1:25775/api/rpc2 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"admin:addClient","params":{"name":"..."},"id":1}'

# 4. agent (user service; see deploy/komari-agent-test.service)
systemctl --user enable --now komari-agent-test.service
```

Admin RPC lives at **`POST /api/rpc2`** with a session cookie. Settings are
`admin:editSettings`; the message channel is `admin:setMessageSenderProvider`
plus `notification_enabled` / `notification_method`.

## The alert path, as verified

1. Ping task 1 `Gateway TCP:443` — healthy control.
2. Ping task 2 `DELIBERATELY FAILING` → `127.0.0.1:9` (closed port).
3. Alert rule: `metric=ping_loss`, `threshold=20`, `interval=1` minute, `tasks=["2"]`,
   **no clients** (a ping rule derives its servers from the task).
4. The agent returned `value = -1` (loss); the server's evaluator fired and
   dispatched. Two receipts arrived, one per minute:

```json
{"title":"Alert","message":"⚠️⚠️⚠️\nEvent: Alert\nClients: MAC-WAN (deploy verify)\nMessage: Ping loss > 20% (deploy verify)\nTime: 2026-09-17 03:36:58"}
```

Cooldown equals the rule `interval`, so a persisting condition re-alerts once per
interval — that is the designed behaviour, and it is why the receipt repeated.

## Findings

### 1. The published container image could never start (fixed here)

`docker run ghcr.io/aone2233/nekomari:latest` failed immediately:

```
exec /app/nekomari: no such file or directory
```

The file existed and was executable. The message means **the program
interpreter is missing**: the release builds the server with `CGO_ENABLED=1` on
`ubuntu-latest`, so it is dynamically linked against **glibc**
(`/lib64/ld-linux-x86-64.so.2`), while the Dockerfile used **`alpine`** (musl).
`ldd` inside the image confirms it:

```
libc.so.6 => /lib64/ld-linux-x86-64.so.2
Error relocating /app/nekomari: __vfprintf_chk: symbol not found
```

Upstream could use alpine because it cross-compiled with `zig cc` and shipped
musl binaries; this fork switched to native builds, which silently invalidated
that assumption. Fixed by moving the base image to `debian:bookworm-slim`, and
released as **v0.1.2** (the published `v0.1.1` image was broken; rebuilding it
was not an option because `docker.yml` checks out the **tag**, so it would just
rebuild the same broken Dockerfile).

Why CI stayed green: the only image check was "is the manifest anonymously
pullable", which tests that the image *exists*, not that it *runs*. A smoke test
that actually executes the binary has been added to `docker.yml`. It was
validated in both directions — exit 0 on the fixed image, exit 255 with the
original error on the broken one — and it now runs on every docker build.

The `debian:bookworm-slim` base grows the image from 72 MB to 184 MB. That is
the price of matching the libc the binary is actually linked against; a
distroless base would be smaller but needs `docker.yml` validation first.

### 2. A target that is "unreachable" may not be

The first attempt used `192.0.2.1` (RFC 5737 TEST-NET-1, reserved and
non-routable) as the failing target. It reported **1 ms with 0% loss** — because
this network answers *every* outbound TCP connect in ~1 ms from an intercepting
device, including for a reserved address. A reliable failure target has to be one
that cannot be intercepted: a **closed port on loopback**.

### 3. The rebrand missed a user-visible string (fixed here)

`adminTestSendMessage` still sent `"This is a test message from Komari."`
(`web/rpc/jsonrpc/admin.system.go`). It is the first string a user sees when
testing a notification channel, and it was found by doing exactly that against
the live instance. Fixed in the same batch as this document.

A sweep for other `Komari` strings found three more that are **deliberately left
alone**, because they sit on the contract boundary described in `FORK.md` rather
than being branding. They are recorded here so the next person does not
"helpfully" change them:

| Left as-is | Why |
|---|---|
| `TwoFactorIssuer = "Komari Monitor"` (`database/accounts/2fa.go`) | The TOTP issuer already stored in every user's authenticator app. Changing it relabels existing entries and splits one account across two names. |
| `"Komari Official"` theme market source (`web/api/admin/theme_market.go`) | The display name of a catalog that really is upstream's; it points at `defaultThemeMarketURL`. |
| `./data/komari.db` (CLI default) | On-disk filename; existing installs depend on it. |

### 4. ICMP needs privileges the agent cannot assume

The agent calls `pinger.SetPrivileged(true)`, i.e. raw ICMP sockets. On
MAC-WAN `net.ipv4.ping_group_range = 1 0` (nothing permitted), and there is no
passwordless sudo, so an unprivileged agent cannot send ICMP at all.
`/usr/bin/ping` works only because it carries `cap_net_raw=ep`.

This is exactly the case the fork's `auto` task type was built for: it probes
ICMP, recognises the failure as a **permission** error rather than
"target unreachable", and falls back to TCP. Observed in the agent log:

```
ping task 1: auto resolved target=192.168.100.1 -> type=tcp target=192.168.100.1:443
```

Note the fallback needs an open 443/80; when both are closed, `decideAuto`
returns `("icmp", target)` and the probe then fails on the permission error,
reporting loss. Wiring `auto` to TCP-only when ICMP is unavailable would report
something closer to the truth.

## Notes for running this again

- The first scheduler tick happens one full `interval` after the rule is created
  (`runImmediately` is false), so a 1-minute rule shows nothing for ~60 s.
- `ssh MAC-WAN '<cmd with &>'` does not keep the process alive; use
  `systemd-run --user` or a user unit (`Linger=yes` is already set on that host).
- The webhook receiver is reachable from the container at the host's LAN address
  (`192.168.100.168:25999`), not `127.0.0.1`.
