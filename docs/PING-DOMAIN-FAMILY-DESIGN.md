# Family-pinned hostname probes: design and safety contract

Status: design only. This does not change tasks, dispatch probes, update Agents,
or permit a release before the existing owner-acceptance and deployment gates.

## Problem and evidence

The 2026-11-05 report attempts `tj-ct-v6.ip.zstaticcdn.com:80` with three
nodes, `auto`, and default-on disabled. The add RPC rejects it because a selected
node is dual-stack or has not reported its addresses. This is the intended
conservative rule in `utils/pingFamily.go`: separate Agents resolve hostnames
independently, and a multi-node task with an ambiguous family can compare
different IPv4 and IPv6 routes. The create and edit RPCs enforce this rule.
It is independent of the `auto` protocol choice.

One local resolver returned only AAAA at investigation time. That is not proof
of what each node resolves, what a CDN will return later, or what the target
will return from another network. A panel-side lookup followed by removing the
check would be unsound. Pinning a literal IPv6 address works around the check
but loses DNS/CDN changes; separate single-node tasks retain DNS but do not
force IPv6. Neither is the intended long-term behavior.

The relevant path is broader than the form:

- `models.PingTask` and `database/tasks/ping.go` persist tasks; both admin task
  and server views send full edit payloads. In particular the server view omits
  `reference`, while `EditPingTask` currently does not update that field.
- `admin.ping.go` validates add/edit; configuration import uses the separate
  `admin.configxport_apply.go` write path and currently bypasses that validator.
  `admin.configxport.go` exports an explicit field list. The current generic
  planner identifies non-settings records by `uuid`, whereas ping exports use
  `id`; import behavior must be repaired and regression-tested before relying
  on it for ping-task round trips.
- `utils/pingSchedule.go` filters only IP literals and dispatches
  `protocol/v2.PingParams` through WebSocket or queued POST-pull delivery.
- `agent/server/websocket.go` decodes ping params into a local struct and
  silently ignores unknown fields. The Agent's `resolveIP` takes the first
  `net.LookupHost` result. `icmp`, `tcp`, `http`, `auto`, `dual`, the reference
  leg and HTTP redirects have separate paths. Merely adding an optional wire
  field will cause old Agents to run an unpinned probe, not to fail closed.
- `agent.pull` advertises capabilities in its payload, but the panel currently
  decodes and ignores them. WebSocket delivery needs an equivalent capability
  signal; a cached `client.Version` is not proof of the current connection's
  executable or protocol support.
- Results carry the *actual* family when known. A negative value writes a
  `ping.loss` sample; treating DNS or protocol incompatibility as negative
  latency would manufacture packet loss and may trigger alerts.

## Proposed behavior

Add an explicit, separately named task field, e.g. `target_family`, with three
states: empty/legacy (existing selection), `ipv4`, and `ipv6`. UI text must
explain that this constrains the main target address family, not the choice of
ICMP/TCP protocol. The old mixed-family guard continues for empty mode. For an
explicit family, create/edit/import may accept hostname + multiple nodes only
when the compatibility, scheduler, Agent and measurement rules below are
implemented together. Validate enum values and reject conflicting IP literals;
do not infer a mode from DNS, hostname spelling, node names or a panel lookup.
Existing tasks default to empty and keep their behavior; no backfill or implicit
conversion. Explicitly selecting `ipv6` for this task should preserve its
hostname and port on every probe.

The first release should scope this setting to the **main target only**.
Reference targets keep their existing independently resolved behavior and
`role=reference` label; UI must not imply the reference is constrained by the
main-target setting. If later an operator needs a pinned reference, it needs its
own explicit contract and field rather than overloading this one.

### Resolution and measurement

- For explicit mode the Agent resolves on the node at probe time using only the
  requested family. Handle literals, bracketed IPv6 and ports correctly;
  IPv4-mapped IPv6 is IPv4. Never fall back to the other family, including when
  the requested DNS record is missing, returns a wrong-family literal, a DNS
  server changes its answer, or auto/dual probes choose a protocol/port.
- Apply the pin across ICMP, TCP and HTTP, auto's decision and cache identity,
  both dual legs, retries, and each HTTP redirect dial. Preserve the original
  hostname for HTTP Host and TLS SNI/certificate checks. Redirects must not
  escape the requested family. Test the lookup/dial through injected resolvers
  and local listeners, without external DNS or live node commands.
- A successful sample reports the actual connection family, not the requested
  one. A connection/refused/timeout *after* a requested-family target address
  is chosen follows existing loss semantics, tagged with that family. A DNS
  resolution failure, no requested-family answer, unsupported mode/Agent, or
  an operation not performed because the node is skipped is **not** a failed
  network probe: it must not create a loss or latency point. Expose a bounded,
  current per-node/task diagnostic distinguishing these cases from real packet
  loss; expire stale diagnostic state and clear it on successful recovery.
- Existing Agent behavior for empty mode, old samples with missing family, and
  protocol/role/family series identities remain unchanged. An operator changing
  an existing task's mode must be warned that historical samples describe the
  old configuration; never relabel or merge them into new data.

### Compatibility and safety

- Extend the shared wire contract and both Agent and Panel implementations;
  unknown, offline, old, downgraded or capability-expired Agents must never
  receive a pinned task they could silently run without enforcing it. Dispatch
  must require affirmative, fresh capability evidence for the **current Agent
  generation** on both WebSocket and POST-pull paths. Define a bounded lease for
  POST-only agents and expire it on disconnect/reconnect, restart, retirement,
  token change and any generation change. Specify and test concurrency with
  queued events: a pin queued for one generation cannot reach an older Agent
  after a reconnect/downgrade. Re-check at dequeue or bind the event to an
  appropriate generation; never trust stale DB version fields as the gate.
- Display per-node `unsupported/unknown`, `offline`, and known address-family
  mismatch separately. Configured assignments should survive unsupported or
  offline states; skip dispatch until eligible. An explicit mode and
  `default_on` may coexist only if future nodes get the same dispatch gate.
  Node-reported addresses are hints, not proof that a destination is reachable;
  applying the existing heuristic may skip valid NAT/tunnel setups, so make any
  such exclusion visible and separately testable.
- Add a pinned-result contract as well as a dispatch guard. A capable Agent
  echoes an opaque task/configuration generation and target role for each
  result; verify it against the current task, node assignment, pin and active
  generation at intake. Never accept a main-target point with a missing or
  contradictory actual-family tag for pinned mode, including delayed replies
  after a task edit, delete or Agent reconnect. Distinguish the reference role
  and preserve legacy result behavior for legacy tasks. This gate must not
  trust a request's claimed family alone or reinterpret stale legacy replies.
- Keep family handling correct through REST/RPC add, task-view edit,
  server-view membership edits, config export/import and migrations on SQLite,
  MySQL and PostgreSQL. Missing fields from legacy callers must not silently
  erase a pin; an explicit clear must remain possible. Older clients must not
  accidentally cause a downgrade from pinned to legacy on a membership edit.
  Preserve reference as well, including the existing edit persistence gap.
  Apply import validation after its intended client mapping and before any
  writes, with transactional/preview semantics; invalid imports must not partly
  apply or bypass the invariant.
- Treat history as a first-class compatibility problem: family tags distinguish
  v4 from v6, but not pinned from previously unpinned v6 under the same task ID.
  Define a configuration-generation/series boundary and apply it consistently
  to graphs, SLA, alerts and CSV, or require creating a new task when that
  boundary would otherwise be crossed. A warning alone is not sufficient if
  the implementation silently aggregates old and new measurement policies.
- Panel rollback must not silently dispatch pinned configurations as unpinned:
  require a pre-upgrade backup and a tested downgrade procedure that restores
  compatible task state or disables affected schedules before old code starts.
  Do not alter the production tasks as part of testing. A stable Agent release
  can move auto-updating nodes, so identify update policy and stage the Agent
  release deliberately rather than assuming a panel-only release is inert.

## Acceptance and release sequence

1. First implement the Agent protocol/handshake and fail-closed behavior; prove
   that old/new and reconnect combinations cannot execute a pinned task
   unpinned. Test duplicate/expired events, WS fallback and capability leases.
2. Implement persistence, add/edit/import/export and scheduler/UI together,
   with a cross-backend migration/round-trip/rollback check and a mounted
   browser test for the actual multi-node hostname workflow. Ensure legacy
   callers cannot erase a pin or reference inadvertently.
3. Run hermetic fake-DNS cases: A-only, AAAA-only, dual-stack with reversed
   order, answer changes, NXDOMAIN, mismatched literal, HTTP redirects/TLS,
   auto cache isolation, dual legs, and both primary/reference roles. Verify
   no opposite-family connection and no false loss on non-probes. Include
   unknown/unsupported nodes, capability expiry and recovery.
4. Only after local and exact-commit CI pass, plan Agent rollout and an isolated
   integration test with synthetic nodes. Production must have fresh encrypted
   off-host backup and isolated restore proof before OC424 deployment; honor
   the existing owner CSV UI acceptance gate first. No automatic token rotation,
   production remote execution or unapproved changes to existing tasks.

This is a proposal, not evidence of implemented compatibility, real DNS or
production reachability. A separate implementation PR and explicit rollout
plan are required before advertising the option in the panel.
