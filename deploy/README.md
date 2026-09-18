# deploy/

Operational scripts and host configuration for running Nekomari. None of this is
needed to *use* the project — it is the tooling used to deploy, verify and operate
the reference instance, kept in-tree so the steps are reproducible rather than
folklore.

**No credentials belong in this directory.** Tokens and passwords come from the
panel or the environment; see [docs/SECRETS.md](../docs/SECRETS.md) for why, and
for the incident that rule came from.

## Deploy

| Script | What it does |
|---|---|
| `deploy-verify.sh` | Proves a **published release** actually deploys: downloads the assets, checks `SHA256SUMS.txt`, starts the server on its own port and data directory, completes the first-run install via the API, connects an agent, confirms the node reports. Cleans up after itself. Runs in CI as the `verify` job. |
| `verify-image.sh` | Pulls a published container image and proves the binary inside it actually starts. Listing tags needs `read:packages`, but that is not the question — the question is whether the binary executes on the image's own base, which is what broke in v0.1.1 (glibc binary in a musl base). |
| `verify-settings-persist.sh` | Runs a real upgrade (old image → new image) against a **copy** of production data and diffs the settings. Evidence that an update does not reset anything, rather than an assurance. |
| `test-volume-footgun.sh` | Reproduces the anonymous-volume trap: install without `-v`, recreate the container, watch the settings disappear. |
| `test-persist-warning.sh` | Checks the startup warning fires on an anonymous volume and stays quiet on a named volume and a bind mount. |
| `docker-compose.yml` | The reference container setup: host networking off, bound to `127.0.0.1`, bind-mounted `./data`. |
| `nginx-nekomari.conf` | Host nginx vhost. WebSocket-safe (agents hold long-lived connections) and forwards `CF-Connecting-IP` so the panel records the real visitor rather than Cloudflare's edge. |
| `install-node-agent.sh` | Installs the agent on a node: refuses to run if one is already active, verifies the download against `SHA256SUMS.txt`, retires older agent units, writes a systemd unit. Works as root or through passwordless sudo. |
| `enable-webssh-all.sh` | Re-applies units without `--disable-web-ssh`. That flag also disables remote command execution, so it is opt-in per node. Tokens come from `NEKOMARI_NODE_TOKENS`. |
| `set-month-rotate.sh` | Sets `--month-rotate` to a node's billing day and adopts the previous agent's `net_static.json`. Without it the panel divides a since-boot counter by the plan limit and reports inflated traffic. |

## Check

| Script | What it does |
|---|---|
| `healthcheck.py` | Post-deployment check of the running instance: host services, container, agent unit, databases, API reachability, which nodes are actually reporting, retained history, the ip-info endpoints, private-IP rejection, active theme. |
| `node_status.py` | Which nodes are live. Distinguishes a restored-but-silent node from a reconnected one. |
| `ping-task-stats.py` | Per-node loss and latency for one ping task. Picks the newest series tag shape automatically — the pre-fork agents tagged `{"task_id":"N"}` and this fork's tag `{"protocol":"tcp","task_id":"N"}`, so matching only the old shape returns history that stopped when the old instance was retired and makes every node look dead. |
| `ping-tasks-audit-all.py` | Fleet-wide: which tasks pair a node with a target its address family cannot reach, and which carry references to deleted nodes. |
| `fix-ping-family.py` | Removes those mismatches and stale references. Reads address families from the database, not `/api/nodes`, which does not expose them. `--apply` to write; dry run by default. |
| `ping-store-locate.py` | Where ping results are stored and at what resolution, for when a chart's granularity is mistaken for packet loss. |
| `ping-history-depth.py` | How much ping history is actually retained. |
| `check-node-freshness.py` | Which tasks are producing fresh samples and whether a given node is in any of them. Answers "is this node actually contributing anything" after the address-family filter means an IPv6-only node legitimately reports on some targets and not others. |
| `test-family-skip-live.py` | End-to-end: re-adds an IPv6-only node to an IPv4 task and confirms the panel skips it without configuration help. |
| `unhide-node.py` | Unhides a node once it is genuinely contributing data. Dry run by default, and refuses while nothing is fresh — a hidden node with no data is usually hidden for a reason. |
| `webhook_receiver.py` | A throwaway HTTP endpoint that records every request it receives, used to prove the notifier really dispatched an alert rather than trusting the UI's "sent" state. |
| `traffic_explain.py` | Prints, per node, the counters the panel divides by the limit and the resulting percentage — so a traffic figure can be checked against the provider's dashboard instead of guessed at. |
| `validate_contract.py` | Field-level validation of the `/api/*/ip-info/v1` responses against the shape the theme requires. |
| `inventory-monitoring.sh` | Lists every monitoring agent on a host. Kept because two systems coexisted here and are easy to confuse. |
| `inspect-node-traffic.sh`, `inspect-netstatic.sh` | Per-interface kernel counters and the agent's netstatic coverage. |

## Browser checks

Run with Playwright against a live panel. Each drives the real UI rather than the
API, because that is the only way to see what a user sees.

### Setup

Needs Node 20+ and a Chromium download (~115 MB). From this directory:

```bash
npm install          # installs playwright (declared in deploy/package.json)
npm run setup        # playwright install chromium
```

### Running

All four take the same arguments — panel URL, username, password, and optionally
an output directory for screenshots:

```bash
node ip_panel_check.js    https://your-panel admin 'password' /tmp
node ip_panel_capture.js  https://your-panel admin 'password'
node favicon_ui_check.js  https://your-panel admin 'password' /tmp
node admin_version_check.js https://your-panel admin 'password'
```

The password goes on the command line, so it is visible in `ps` while the script
runs and lands in your shell history. That is acceptable for a throwaway test
account and not for a real one; see [docs/SECRETS.md](../docs/SECRETS.md).

A panel with `private_site` enabled answers anonymous requests with 401, so the
scripts log in through `/api/login` first rather than driving a login form.

| Script | What it does |
|---|---|
| `ip_panel_check.js` | Logs in, opens a node detail, reports which tabs render and every ip-info response the browser saw. |
| `ip_panel_capture.js` | Installs a fetch interceptor before app code runs, so the exact bodies the theme parses are recorded. This is what separates "the server sent something the theme rejects" from "the theme gates the UI elsewhere". |
| `favicon_ui_check.js` | Uploads a favicon through the admin UI and reports whether the icon actually changed. |
| `admin_version_check.js` | Confirms the admin version banner queries this repository, not upstream's. |

Each exits non-zero if its own expectations fail, so they can be wired into a
script; none of them are part of CI, because all four need a running panel and
real credentials.

## Themes

| Script | What it does |
|---|---|
| `upload_theme.py` | Uploads a theme archive through the server's chunked upload API, so its own extract-and-validate path runs rather than files being written into the data volume by hand. |
| `rebrand_theme.py` | Rebrands a third-party theme's user-visible strings for Nekomari, while deliberately leaving the theme *identifier* alone — that string is the theme's contract with the server. |

## Cloudflare

| Script | What it does |
|---|---|
| `cloudflare_dns.py` | Lists, shows or points a hostname at the origin. Reuses the token `cloudflared tunnel login` wrote, so no separate secret is needed. |
| `retire-monitor-dns.py` | Removes the DNS record fronting the retired CF-Server-Monitor worker. Documents why it cannot finish the job: worker-managed records are read-only through the DNS API. |

## CI maintenance

Actions pinned to a Node 20 runtime still run, but GitHub forces them onto Node 24
and warns on every job. These four make the upgrade a re-run rather than a
research task, and they check the things that only fail at run time.

| Script | What it does |
|---|---|
| `check-action-versions.py` | Which pinned actions are behind their latest release. Reads the list out of the workflows, so it cannot drift from what is used. |
| `check-action-breaking.py` | Surfaces the breaking-change lines between the pinned version and the latest, before jumping majors. |
| `check-action-inputs.py` | Confirms every `with:` input a workflow passes still exists on the pinned action. A dropped input only fails when the workflow runs — for `release.yml` that means at release time, which is the worst moment to find out. |
| `bump-actions.py` | Rewrites the pins to each action's latest major. Targets come from the actions themselves, because they differ per publisher — `docker/build-push-action` was already past the version `actions/checkout` needed. Dry run by default. |

Watch out for one thing when bumping `setup-go`: `go-version-file` must point at
the **higher** of the two modules' requirements. The root `go.mod` says 1.25.0 and
`agent/go.mod` says 1.26.0, and both are built from the same job. Pointing at the
root installed too old a toolchain and the agent tests failed with
`go.mod requires go >= 1.26.0`. Older versions of the action tolerated the
mismatch; v7 installs exactly what the file asks for, which is correct behaviour
that simply exposed the wrong file being named.

## Retiring the previous stack

See [docs/RETIRE-CF-PROBE.md](../docs/RETIRE-CF-PROBE.md) for the full record.
These are kept because the rollback path matters more than the removal path.

| Script | What it does |
|---|---|
| `retire-cf-probe.sh` | Per host: backs up, disables, removes, and sweeps for residual traces. `--dry-run` supported. |
| `retire-cf-probe-all.sh` | Drives the above across the fleet. |
| `verify-cf-probe-retired.sh` | Confirms cf-probe is gone **and** that the Nekomari agent is still active. |

## Token rotation

| Script | What it does |
|---|---|
| `rotate-all-tokens.py` | Issues a new token for every node through `admin:editClient` and writes a push map to a `0600` file. Tokens never reach stdout. |

Pushing the new tokens to each host depends on where the SSH keys live, so it is
done per-reachability rather than by one script: some hosts are reachable from the
panel host, others only from a workstation, one only over IPv6, and two need a
sudo password. That variation is why the ad-hoc push scripts were removed once
used — keeping them implied a single supported path that does not exist.

## Script authentication, and what 2FA changes

Every script here that talks to the panel logs in through `nekomari_auth.py`, which
handles TOTP. **Enabling 2FA on the account breaks any script that logs in with just
a username and password** — they start returning `401 2FA code is required`. That is
not a bug in the scripts; it is 2FA working.

```bash
export NEKOMARI_PASSWORD='<panel password>'
export NEKOMARI_2FA_SECRET='<base32 secret from enrolment>'
sudo -n env NEKOMARI_PASSWORD="$NEKOMARI_PASSWORD" \
            NEKOMARI_2FA_SECRET="$NEKOMARI_2FA_SECRET" \
            python3 deploy/<script>.py
```

`nekomari_auth.py` implements RFC 6238 directly rather than depending on a TOTP
library, so the scripts stay runnable on a host with nothing installed. If the
variable is missing while 2FA is on, login fails with a message naming the variable
instead of a bare 401.

`enable-2fa.py` enrols the account and prints the secret and `otpauth://` URI; that
secret is the only way back in short of the `disable-2fa` CLI.

## hosts/

Per-host configuration that does not fit the generic installer.

| File | Why it is separate |
|---|---|
| `oc424.service` | The reference panel host. Uses the restored node's own token rather than `--auto-discovery`, so the panel keeps that node's group, tags and history. |
| `macwan.service` | Runs as a **user** unit: that host has no passwordless sudo. Also documents that `PrivateTmp` and `NoNewPrivileges` must stay unset or a file capability stops working. |
| `pzyc.sh` | Cannot fetch release assets (the CDN resets TLS), so it installs a separately fetched, checksum-verified binary. Takes its token from `NEKOMARI_AGENT_TOKEN`. |
| `macwan-webhook-sink.service` | An artificial notification channel used to prove alerts are really dispatched. |

## experiments/

One-off probes kept because they captured knowledge that was otherwise expensive
to obtain. Not part of any operational path.

| File | What it established |
|---|---|
| `capability-probe.sh` | That `NoNewPrivileges=yes` and `PrivateTmp=yes` each defeat a `setcap cap_net_raw` file capability — with `/proc/<pid>/status` still reporting `CAP_NET_RAW` in `CapEff`. This is why MAC-WAN's ICMP tasks failed while an identical command from a login shell worked. |
| `rawicmp-probe.go` | Minimal raw-ICMP socket probe, used to separate "the capability does not work here" from "the agent does something else". |
