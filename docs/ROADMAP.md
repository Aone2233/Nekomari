# Roadmap

Consolidated 2026-09-24, after the v0.1.26 rollout. This is the single list of
what is left, in priority order, with the evidence for each item and what
"done" would mean. It supersedes the scattered `FOLLOWUP-v*.md` lists for
planning purposes; those stay as the record of what each release addressed.

State at the time of writing: production panel `v0.1.26`, **ten** nodes all on
`v0.1.26` agents, the address-family split live in the metric store, CI green on
`main`. `docs/OPEN-WORK.md` tracks the older 2026-09-18 batch and its remaining
decisions; `docs/DEPLOY-OC424.md` is the current-state runbook.

## A. Correctness and coverage

### A1. Split `frontend/src/pages/admin/index.tsx`, with a fixture first

2 962 lines, larger than the next two files combined, and the structural review
calls it "the single worst compound-risk item". Its sections
(`AutoDiscoverySection`, `GenerateCommandButton`, `NodeTable`, `EditButton`)
already look like separate components.

The ordering matters and is the review's: **a mounted regression for the file
comes first**, then the split, so the split is held by a test rather than by
review. The v0.1.26 work added a server-backed regression for the node-edit and
offline-notification write paths, which covers the mutations but not the file's
own state wiring.

Acceptance: a mounted fixture over the node table's state (poll, filter, sort,
selection) passing before and after; the file under ~600 lines with no behaviour
change.

### A2. Accessibility and integration the fixtures cannot prove

Two things are still asserted only by construction:

- **Chart keyboard and announced status.** `chart-a11y.spec.py` covers keyboard
  entry, but nobody has driven a screen reader against live dashboard data. The
  Recharts 3 upgrade also flipped `accessibilityLayer` on by default for two
  dashboard charts, which changed what is announced.
- **OAuth admission against a real provider.** Production OAuth is disabled, so
  every provider path is tested against a fake. `docs/OAUTH-INTEGRATION.md`
  records exactly which claims are test-verified and which need a real provider;
  a controlled instance with a real GitHub (and one QQ aggregator) app is the
  only way to close them.

Acceptance: a recorded screen-reader pass over the node-detail load chart and the
dashboard, and one real end-to-end login per provider family on a throwaway
instance.

### A3. Make the authentication suite repeatable in-process

`-count=10` runs of the auth tests fail on the later iterations because the rate
limiter and database fixtures are shared. Fresh processes pass, so the suite is
not wrong — it is only non-repeatable, which is why it cannot be used as a stress
check yet.

Acceptance: `go test ./database/accounts/... ./web/api/public/... -count=10`
passes, with the limiter and fixtures isolated per test.

### A4. React Compiler rules: keep advisory, do not adopt yet

`react-hooks` v7 folds 14 compiler rules into `recommended`; on this codebase they
raise 133 findings (ref writes during render, setState inside effects, manual
memoization). `eslint.config.js` names the two classic rules explicitly so the
upgrade did not silently adopt them. The compiler audit
(`npm run audit:compiler`) is in CI and currently reports 0.

Acceptance for adoption: the 133 findings triaged into real / cosmetic, with the
real ones fixed and the rest suppressed per-rule with a reason. Not before.

## B. Performance — measure, then change

The rule this project has adopted, after the upload-lock proposal was correctly
deferred: **instrument first, change second.** Each item below says what to
measure before touching anything.

### B1. What the service worker precaches (the biggest first-visit cost)

Measured: a first visit transfers **1.9 MB gzipped across 540 files**, because
every route chunk is precached rather than just the shell. The editor (3.09 MB
raw) is already excluded and the stylesheet is 96 KB gzipped, so neither is the
lever.

Next: measure the first visit's actual transfer and cache contents on a fresh
profile, then decide which route chunks belong in the precache. Acceptance: a
first-visit figure and a stated policy per route group, not a guess.

### B2. Upload scan duration and reservation-lock contention

v0.1.20 added the instrumentation: refusals and hold durations per operation,
exposed through `Store.Stats()` and logged hourly **only when caller work
happened**. The reason it was not acted on is the measurement: production had
served **zero** archive uploads across every retained nginx log, so there was
nothing to size. That is still true.

Next: exercise concurrent admin uploads against a throwaway instance and read the
counters. Only then decide whether the single store lock needs splitting.

### B3. The edge path — the largest real latency factor

Measured, repeatedly: the origin answers in **0.002 s**, while the same request
through Cloudflare ranges **0.17 s to 26.9 s** depending on which POP serves it.
US-East POPs (IAD, EWR, MIA, ATL) sit at 3-17 s for this origin; Asian and
European POPs sit at 0.2-1.5 s. Two ~20 s outliers predate the probe fix, so
this is not a regression from it.

This is not application performance, and no amount of frontend work moves it. The
open questions are whether the origin's own network path to those POPs can
improve, and whether `PUBLIC_MAX` (default 3.0 s) is the right alert threshold
given the spread. The probe now records the distinction, which is what makes the
question answerable.

### B4. Agent sampling interval versus traffic

Measured across the fleet: `-i 5` costs **4-7 MB/day per node** (roughly
125-220 MB/month), and it is the single biggest lever on that figure. The default
is 3 s, and the fleet was set to 5 s. Raising it trades chart resolution for
bandwidth; with ten nodes the absolute numbers are small, so this is a dial to
note rather than turn.

### B5. SQLite connection pool

Deferred by two reviews for the same reason: no evidence of pool waits, and a
guest API latency sample is not evidence. Revisit only with `Store.Stats()`-style
pool-wait counters showing a queue.

## C. Visualization

### C1. Ping latency has one-millisecond resolution, so intra-city paths read 0

The agent reports `stats.AvgRtt.Milliseconds()` — an integer. Two hosts in the
same city therefore flatline at `0.0`, which is what the new JPKD2 → NOSLA relay
task shows (0 ms) against CLISP → MegaBox (1-2 ms). Sub-millisecond numbers are
all rendered as zero, so the chart cannot distinguish "0.2 ms" from "0.9 ms" and
a regression inside that band is invisible.

Next: carry microsecond precision (or a float millisecond) through the ping
result and the metric store, and check that existing charts still scale. Note the
compatibility constraint that made the family work safe: the store's series
identity is its tags, so this is a value-precision change, not a tag change.

### C2. The address-family split is now live — check how it reads

As of v0.1.26 a mixed-family task renders as two series tagged `family:ipv4` and
`family:ipv6`. 39 of 122 `ping.loss` series carry a family tag. Nobody has looked
at the result on a real chart yet: whether the labels are legible, whether the
legend distinguishes them, and whether the statistics table is clear.

Next: open a mixed task in the dashboard and evaluate. Acceptance: a reader can
tell the two paths apart without knowing the tag format.

### C3. Long-window charts and the "last 1 day" panel

The Recharts 3 verification rendered the instance LoadChart, the dashboard traffic
AreaChart and MiniPingChart with real data, but **`MiniMetricChart` ("最近 1 天")
was never rendered with data** — it needed no change (single default-id axis) and
shares the code path, but it is unverified. Worth one pass, together with dark
theme, since the migration touched every chart's wrapper.

## D. Security and deployment hardening

### D1. The agent token is visible in `ps`

Every unit in the fleet passes `-t <token>` on the command line, so any local user
on a node can read its token. This was found while deploying JPKD2 and is
fleet-wide, not specific to it. Options, cheapest first: read the token from an
environment file or the config file (`--config` already exists), or keep the
command line but restrict the unit's visibility.

Acceptance: `ps aux | grep komari-agent` on a node shows no token, and the
installer's generated one-liner is updated to match.

### D2. Prefer `AmbientCapabilities` over running as root

Eight of ten nodes run the agent as root. NOSLA shows the better pattern — a
dedicated unprivileged user with `AmbientCapabilities=CAP_NET_RAW` — and
`docs/AGENT-FOOTPRINT.md` records it as the recommendation. JPKD2 runs as root
because Alpine has no `setcap` and the agent needs raw ICMP for ICMP-type tasks.

Next: move the systemd nodes onto the NOSLA pattern; for Alpine, either install
`libcap` or accept root there with a note.

### D3. Release checks that still need a human

`deploy/deploy-verify.sh` runs in the release pipeline and proves a fresh install
end to end, including an authenticated admin flow. What it does not do: exercise
the **installed production theme** (only the embedded default UI), or an
authenticated admin smoke test against the real panel. Both were proposed in the
v0.1.19 review and remain open; the theme half could reuse
`deploy/theme-contract-check.mjs`.

## E. Agent-side improvements

Reviewed 2026-09-25 against `agent/`. These are the things worth changing in the
probe itself, ordered by how much they remove rather than how clever they are.
Note the cost: any of these makes the fleet move, per the cadence rule in
`docs/RELEASING.md`, so they are worth batching into one release.

### E1. ICMP does not need root, but the agent demands it

`icmpPing` calls `pinger.SetPrivileged(true)` unconditionally
(`agent/server/task.go:190`), which asks pro-bing for a **raw** socket — and a raw
socket needs root or `CAP_NET_RAW`. The kernel also offers an unprivileged ICMP
socket (`SOCK_DGRAM`, gated by `net.ipv4.ping_group_range`), which pro-bing uses
with `SetPrivileged(false)` and which is enough to measure echo latency.

Nothing is lost by using it: the agent sets no TTL, traffic class, mark or source
address on the pinger, which are the options the unprivileged socket cannot
carry. Measured on NOSLA as the agent's own user: a raw socket answers
`PermissionError: [Errno 1] Operation not permitted` while a `SOCK_DGRAM` ICMP
socket opens fine.

So this is a deployment constraint the agent imposes on itself. It is why eight of
ten nodes run the agent as root, and why JPKD2 runs as root specifically — Alpine
ships no `setcap`, so the file-capability route is closed there even though the
agent's own capability handling would otherwise allow a dedicated user.

Acceptance: ICMP tasks work on a host whose process has no `CAP_NET_RAW` and whose
`ping_group_range` permits the process's group, with the raw path kept as the
fallback for hosts where it does not.

### E2. A denied ICMP probe is reported as packet loss

Only the `auto` protocol path consults `isPermissionErr`
(`agent/server/task.go:369`), which falls back to TCP when the local permission is
what failed. A task typed explicitly as `icmp` calls `icmpPing` directly on every
dispatch path and turns the error into `-1`, which the panel converts to packet
loss — indistinguishable from the target not answering. The node's journal has the
truth; the panel does not.

The comment on `isPermissionErr` states the principle exactly: *"把工具的限制误读成
目标的事实，会得出相反的结论"*. This is the same misreading, one layer down.
MAC Server's documented 15.2% phantom loss came from precisely this, and it was
fixed by restoring the capability rather than by making the agent honest.

E1 removes the cause on most hosts; E2 is what makes the remaining ones
self-describing. Acceptance: a locally-denied ICMP probe is distinguishable from
target loss in the panel, not only in the journal.

### E3. Reconnects have no backoff

The WebSocket loop retries on a fixed `--reconnect-interval` (default 5 s) with
`--max-retries` (default 3) inner attempts, forever, and logs each attempt. A
panel outage therefore has every node retrying every few seconds for as long as it
lasts. At ten nodes that is harmless; it is the shape that stops being harmless
when the fleet grows.

Measured churn over 24 hours: 华纳云 HN-JP1 logged 166 WebSocket lines and NOSLA
181, against 10-18 on every other node.

Acceptance: bounded exponential backoff with jitter, reset on a successful
connect, so a single blip still recovers in seconds. It should also cut those two
nodes' log volume by an order of magnitude.

### E4. The traffic ledger swallows its own save errors

`monitoring/netstatic/static.go` discards the result of `saveToFileLocked()` in
both the periodic rewrite (L344) and the immediate flush (L595). A full disk or an
unwritable file therefore stops persisting traffic accounting silently, and the
first symptom is wrong traffic numbers — after a restart, or after the plan limit
is hit. The panel's own upload-cleanup path was changed to log once per failure
burst for the same reason.

Acceptance: the failure is logged (rate-limited, so a persistent error does not
become its own outage) and visible in the agent's output.

## F. Decisions waiting on you

1. **Mixed-family ping tasks.** The panel now *shows* the mix; it still does not
   *prevent* one. The durable fix (option C) made it visible; option B — restrict
   a task to one family at creation — was never implemented. Do you want creation
   blocked, warned, or left alone?
2. ~~**Nomao's orphaned agent.**~~ **Closed.** The agent was already gone when
   checked on 2026-09-24: no unit, no binary, no `/opt/komari*`, and the panel has
   logged no request from that address since. `docs/OPEN-WORK.md` entry 4 carries
   the full record.
3. ~~**Release cadence.**~~ **Decided: the fleet moves only when the agent source
   changes.** A panel-only release leaves the agents alone — which is what the four
   releases before v0.1.26 did, and v0.1.26 is the exception that proves the rule,
   because it changed agent behaviour and the fleet had to move. The check before
   every rollout is `git diff --stat <previous-tag>..<tag> -- agent/ protocol/ pkg/`:
   empty means the agents stay. Recorded in `docs/RELEASING.md`.
4. ~~**The `MAC Server` jitter.**~~ **Resolved 2026-09-25, and the premise was
   wrong.** It was never MAC. The panel's task is a **TCP** probe, and the TCP
   handshake to that one target intermittently takes 240-590 ms from that host,
   while **ICMP to the very same address from the very same machine stays clean**
   (0 of ~900 pings over 50 ms, in parallel with 27 of ~900 TCP connects). MAC
   just has the best baseline in the fleet — p50 **3 ms** on that task against
   OC424's 68 ms — which is exactly why a 250 ms handshake stands out there and is
   invisible in everyone else's noise. Full measurements in
   `docs/DEPLOY-OC424.md`.

## G. Not on this list on purpose

- **Increasing SQLite concurrency** (see B5) — no evidence.
- **Splitting the editor chunk or trimming the CSS** — the editor is already out
  of the precache and the CSS is 96 KB gzipped; neither is the first lever.
- **Migrating Vite or TypeScript across major versions** — no forcing reason yet.
- **Replacing the npm `lodash/throttle` import with a local throttle** — declared
  now (`v0.1.26`), and a behaviour-changing cleanup that belongs with someone
  actually touching the terminal page.
