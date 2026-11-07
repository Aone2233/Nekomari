# Family-pinned probes: transport fencing design

Status: local release candidate and historical design record. The production
task RPC, scheduler, WebSocket-only dispatcher and Agent execution path are now
wired in the uncommitted candidate; an admin-only diagnostic snapshot is also
available. The browser-to-handler test exercises these paths with isolated
Agent processes, fake DNS and IPv6 loopback. POST-only Agents are deliberately
skipped, and safe rollback and live acceptance remain open. No candidate has
been deployed. Earlier implementation checkpoints in this document describe
their own time and may state that these entrypoints were not yet wired; the
current status is maintained in [OPEN-WORK.md](./OPEN-WORK.md).
The parent contract is [PING-DOMAIN-FAMILY-DESIGN.md](./PING-DOMAIN-FAMILY-DESIGN.md).

## Why a capability flag is insufficient

The panel currently stores a `v2Clients` generation that is allocated only
when the UUID has no entry. A replacement WebSocket can inherit that marker;
POST `agent.pull` and `agent.report` can consume the same queue, and queued
events can also be pushed to a connecting WebSocket before it advertises any
capability. A node's stored version and the `capabilities` field of a previous
pull therefore cannot establish which binary will receive the next event.
Further, an old long poll may already have selected events while a new binary
connects. Checking only when enqueueing would be racy.

Use a distinct wire method such as `agent.pingFamilyV1`; old Agents do not
recognize it. This is a second safety boundary, not a substitute for the
generation and result checks. A new Agent must reject a missing or malformed
pin, task/config generation, session token, or freshness ticket without issuing
any probe. Legacy `agent.ping` and its wire shape stay unchanged.

## Runtime identities and leases

Each new Agent process has a cryptographically random instance ID; failure to
generate it disables the feature. Each transport session also gets an opaque
panel-issued session nonce. IDs are bounded and syntactically validated. Neither
is an authentication credential: the existing node token, current-credential
admission and WebSocket identity checks remain mandatory. Authentication does
not establish that a binary enforces this feature; an affirmative handshake by
the currently bound session does.

- A WebSocket starts *unknown*, including on replacement or reconnect. The
  current socket sends its instance ID and supported protocol version. Under
  credential admission, the panel binds a fresh nonce to that exact connection
  object and Agent instance. Only a valid acknowledgement from that same
  socket activates the lease. A late acknowledgement or close from a replaced
  socket cannot renew or retire the successor. Expiry, close, replacement,
  token reset, node deletion and panel restart invalidate the session.
- Stage one can safely leave POST-only Agents unsupported and skip their pinned
  probes while continuing legacy monitoring. That is a partial rollout, not
  completion of the parent design's POST requirement. Do not inherit a WS
  capability into POST or enqueue pinned work for generic POST delivery.
- A POST fallback session needs a challenge/acknowledgement and a short lease.
  A first pull obtains a new nonce but no pinned event; the subsequent pull
  echoes the nonce and current instance ID before the panel marks it capable.
  This alone does **not** order competing first pulls: the transition fence in
  the next bullet is also mandatory. Every delivery and renewal requires the
  exact active pair. A stale pull with an old nonce cannot revive or overwrite a
  newer session. A report lacking a valid session may receive legacy events only.
- WebSocket and POST transitions are explicit. Beginning a new session retires
  the old session before pinned dispatch is allowed. A delayed bootstrap cannot
  roll the session backwards. In particular, allowing any old request without
  a nonce to create a new POST session is unsafe: a stale HTTP request can arrive
  after a newer handshake. The implementation must fence bootstraps using a
  server-issued challenge tied to current transport admission, or reject the
  ambiguous transition and retry a new handshake. Merely comparing timestamps,
  Agent versions or random instance IDs does not establish their ordering.
- Define lease duration against the existing 25-second long poll and delivery
  TTL, with a bounded skew margin. A lease is checked when a response is built,
  not just when the wait starts. Expired evidence fails closed; recovery needs a
  fresh handshake. Do not keep a stale capability alive through normal reports.

This contract permits temporary skipped probes during an ambiguous transition;
it does not permit the panel to guess which process is current. Session state is
bounded by active node count and expires/sweeps independently of metrics.

## Clock-independent freshness (partial Agent implementation)

A three-second panel-issued absolute expiration is not a reliable execution
boundary: the Agent and panel may disagree on wall-clock time. Likewise an
Agent-supplied completion timestamp cannot prove that a result is fresh. Do not
activate the wire helpers based solely on syntactically valid identifiers.

- When a pinned probe is due, the panel must request authorization from the
  exact active transport session rather than send the target immediately.
  The Agent issues an unpredictable one-use ticket bound to that request,
  session and a short local monotonic deadline. The panel echoes it with the
  complete task and a distinct random dispatch ID; under the lock that retires
  the session, the Agent verifies and consumes the ticket exactly once before
  starting work. An expired, reused or wrong-session ticket starts nothing.
  Session retirement cancels the work already admitted.
- The Agent controller implements local ticket issuance and an isolated atomic
  start boundary with finite pending/active limits. Permit requests
  include a strictly increasing, session-bound sequence, so replaying an old
  request cannot mint a fresh ticket. Concurrent permit writes from the panel
  must preserve that sequence order or tolerate a skipped probe. Local lease
  expiry also retires tickets and cancels work; a delayed renewal cannot revive
  the expired session. A target-free permit request is handled over WebSocket,
  and an isolated panel helper sends it, but no production scheduler invokes
  the helper. The Agent execution path consumes the ticket only when its
  dispatch hook is explicitly enabled by a test; it is nil in production.
  The Agent now also refuses to issue a permit when that hook is nil, so a
  partially deployed build cannot advertise execution readiness merely by
  completing the handshake. An isolated real-Agent/actual-handler test checks
  that a handshaken disabled Agent rejects the permit and receives no probe.
  The local session timer follows a successful renewal rather than retaining
  the earlier deadline; a stale expiry callback rechecks the current session.
  A bounded control write avoids holding the session lock indefinitely, and
  may briefly wait for a concurrent ordinary report before failing closed.
  Tests exercise an in-flight probe across renewal, expiry without traffic,
  an old callback after replacement and a blocked renewal write. They do not
  prove fleet coverage or real-time scheduling under resource exhaustion.
- An isolated panel reservation map allocates per-session sequence numbers,
  tracks a bounded number of pending permit responses, and wakes waiters on
  replacement, re-handshake, disconnect, token reset or observed lease expiry.
  It rejects mismatched responses and never evicts an unexpired reservation.
  An isolated helper serializes reservation and direct socket writes and
  recognizes replies on the credential-admitted WS reader; it is not called
  by the scheduler and never enters the generic queue. Busy session/socket
  writers skip rather than build an unbounded wait queue. The bounded write
  restores the previous deadline; a failed or canceled write aborts the socket.
  A blocked plugin hook cannot be forcibly interrupted, so a process-wide
  limit of 64 such workers causes further pinned requests to fail closed until
  they finish. A loopback test exercises the panel's target-free wire request
  and response with a simulated peer, not a real Agent probe. An isolated
  permit is not authorization to send a target or write a result.
- The Agent can parse a strictly bounded distinct probe frame and, in tests,
  consume its one-use ticket, execute the resolver and emit a result only over
  the same WebSocket. The real Agent entrypoint now enables this hook locally;
  the production panel remains gated and Agent rollout has not begun. A real
  IPv6 loopback test exercises handshake, permit, execution and replay against
  that entrypoint. Result writes have
  bounded deadlines, never use the generic POST fallback and abort/retire the
  socket on write failure. Session cancellation stops the context-aware primary
  and independently resolved reference probe. A reference selects its own
  first address and family; HTTP redirect handling stays in that family, which
  is stricter than the legacy reference when redirects change families.
- The HTTP transport can detach a dial context from the request context. The
  local draft additionally binds DNS/dial to the probe deadline and retirement,
  records the chosen family as the dial starts, and retains it when a timeout
  races the dial callback. Focused race tests cover unresolved timeouts, both
  fast and delayed timeout callbacks, and cancellation after a dial starts.
  Cancellation depends on the resolver/dialer honoring its context; these
  synthetic tests do not establish production reachability or coverage.
- An isolated per-session registration primitive binds a dispatch ID to the
  active socket, instance and supplied task generation, retains consumed or
  canceled IDs as tombstones through expiry, and enforces a finite capacity.
  It consumes primary protocol legs and the independent reference leg once;
  malformed results do not consume a leg. A local unwired delivery helper now
  registers it before a bounded direct-WS write and cancels on failure. That
  helper obtains a permit outside the fences, then reloads the actual task and
  node under credential, task and per-node session fences before delivery.
  A local draft admission helper reads the task and assignment under a task
  guard, checks an internal random task incarnation and calls a synchronous
  metric writer. A bounded WS-only handler calls it under current-credential and
  current-socket admission; generic POST rejects the method. Local handler tests
  reject malformed frames, stale sockets and unregistered dispatches. An
  isolated test also exercises the real Agent against actual HTTP credential
  admission and the WebSocket handler, with a test-only probe hook, fake DNS
  and local dial. The test invokes dispatch explicitly; it does not run the
  scheduler. Default-on assignment
  and the private create/edit helpers renew the incarnation atomically; local
  SQLite batch tests cover rollback and unassign/reassign, but the public API
  and configuration import are not enabled or fully integrated.
  The draft now holds a per-node session fence rather than the global Agent
  mutex across the write. Race tests with a blocked injected writer cover other
  nodes' progress, replacement, disconnect, reset, re-handshake and expiry.
  The draft now uses a nonblocking, current-bucket append in the real store:
  it acquires all relevant locks with `TryLock`, validates every point before
  modifying raw/hot data, and makes no SQL call or asynchronous follow-up
  while admission is held. A busy store, disabled metric, absent definition
  snapshot, duplicate timestamp or minute boundary may drop the sample rather
  than stall retirement or append later. Normal compaction persists admitted
  hot buckets. Local tests exercise real-store contention, cancellation,
  atomic refusal, rollup persistence and concurrent ordinary ingest. A separate
  loopback harness exercises the actual Agent with these helpers, and the
  separate transport fixture runs through real panel HTTP/WS handlers.
  Neither establishes a hard real-time bound on CPU scheduling and in-memory mutation. Credential admission
  remains mandatory. Expired, duplicate, unknown or stale results must never
  write a metric.
- A bounded in-memory diagnostic primitive now records admitted
  non-probe reasons, metric-write refusal and successful ingestion. It stores
  task incarnation, session and panel permit sequence; under the existing
  task/session fences a late older result cannot overwrite a newer one in the
  same session. Dual primary legs and reference are separate, entries expire
  after two minutes, and contended/full writes increment a dropped counter
  instead of blocking retirement. An admin-only, on-demand single-node
  snapshot reloads the assignment and fences the current socket; it exposes
  dropped updates and expires stale state in the UI. A failed reserved permit
  records only the generic reason-unknown `permit_unavailable` state, fenced
  by task/session/sequence, not a guessed Agent version. Canceled or
  unreserved requests, scheduler saturation, and a probe silently refused
  after its permit can remain unknown. A snapshot can become stale immediately
  after it is read; the UI says so. Absence never implies successful execution.
  Diagnostic state is lost on restart and is not historical monitoring data.
- The helper assigns the panel's result-admission time, not the Agent clock or
  the earlier dispatch time. At a minute rollover the store may conservatively
  skip a result rather than revive an already-flushed historical bucket. This
  behavior and its effect on coverage still need end-to-end validation.
  A result missing its pending dispatch must fail closed, even after restart.
- The isolated scheduler bounds concurrent delivery attempts, but a successful
  socket write finishes before the Agent probe. The ledger refuses another
  dispatch for the same task/session while any prior leg is pending, avoiding
  starvation when an interval is shorter than the probe time. A missing leg
  delays another attempt until bounded expiry. A tombstone also refuses a
  delayed older permit after a newer dispatch has registered. The scheduler's
  worker bound does not limit Agent-side execution across different tasks;
  cross-task fairness and production behavior under sustained overload are
  not established.

This is not a finished bootstrap or queue. The production task-guard path rejects
pinned edits/imports, drains queued legacy observations before deletion, and skips
pinned tasks in the legacy scheduler. Authenticated POST/real SQLite/metric
store tests verify that deletion removes flushed and queued legacy samples and
that an old legacy result cannot enter a missing or pinned task. A subsequent
local change checks the durable deletion reservation before legacy result
admission, pinned result admission, and pinned delivery; the authenticated
lifecycle test now deliberately reuses the reserved ID for both legacy and
pinned rows and expects refusal. The check requires the reservation to exist
and only covers the current process paths; old binaries, restoring an earlier
main database without the reservation, direct SQL and other processes remain
outside this protection. SQLite `AUTOINCREMENT` avoids reuse in normal fixture
creation, but the pinned incarnation alone does not establish identity. Draft exports retain
the family, omit the runtime generation and use schema 2, which both the
current schema-1 importer and older panels refuse. Imports/previews also refuse
before any writes while pinned rows exist. These exports are not currently
importable; import round-trip is still not implemented.
This is not yet a complete history or rollback boundary.
The local migration draft excludes pinned rows from the startup-time legacy
all-clients expansion, with a SQLite restart regression test. Older binaries
still lack that exclusion and do not understand the pin, so rollback with
pinned rows present remains unsafe and must be explicitly addressed.
Production scheduler/delivery integration, comprehensive task and credential
fence coverage, per-node diagnostics, and further scheduler, deadline and
cancellation tests with real Agents remain required before delivery.

The current scratch mutation path is exercised through RPC binding and a
test-only REST-bridge method with admin authorization. An opt-in Chromium
fixture now also sends the actual component's request, using a real admin
session, through that bridge to a scratch SQLite database, isolated scheduler
and three real Agent test processes. Fake DNS and an IPv6 loopback service
verify a recorded IPv6 result, a no-family non-probe without false loss, a
disabled Agent receiving no probe, and absence of legacy queued events. The
test also checks that anonymous calls fail and the real production add method
still rejects the pin. The CI panel-smoke job opts into this test. It is not a
live-node or full production browser-to-Agent flow: production creation,
scheduling and the Agent execution hook remain disabled. Draft import excludes concurrent draft creation with a
process-local fence, but the planner, cross-entity atomicity and cross-process
coordination remain unresolved. A committed create also needs visible reload
failure and reconciliation before release.

The local POST handler conservatively retires WS capability on every bounded,
authenticated POST. This avoids treating an ambiguous transport transition as
proof that the same Agent is active, but the current Agent also uses POST for
ordinary unlock and exec results while WS is healthy. A local loopback test
now uses the actual Agent reader and panel helpers: after simulated POST
retirement the old capability cannot issue a permit, a failed renewal clears
the Agent state, a later heartbeat starts a fresh handshake, and another IPv6
sample is admitted. A separate test uses the real POST/WS handlers and checks
credential retirement during an in-flight Agent probe. Both accelerate the
heartbeat and use a test-only Agent hook; neither quantifies production
coverage or exercises the production Agent socket registry's transport-failure
path. A subsequent isolated real-scheduler test uses the normal 25-second
handshake cadence and regular application keepalives: after a POST it observes
a temporary gap followed by an IPv6 sample. The Agent now begins a fresh
handshake immediately after a rejected active renewal. The fixture still uses
fake DNS, an enabled test-only hook and a one-second task interval, so it does
not measure fleet coverage. A newer loopback fixture also uses the real Agent
reader and production `*ws.SafeConn` registry, enables the probe hook only in
the fixture, starts a blocked synthetic lookup and reproduces the report
writer's close/error path. The same abort handler retires the registered
session, cancels that lookup and refuses an unused old ticket, including on a
replacement controller; ten race-enabled repetitions pass. This is not a
full `EstablishWebSocketConnection` reconnection or live-Agent test. POST and
transport churn can lose probe cycles, so surface the reason before treating
the WS-only rollout as usable. Delayed old POSTs may cause a skipped
observation, not authorize a probe.

The private draft edit helper now refuses target, type, reference and family
changes under the same pinned task ID. A new incarnation fences in-flight
results but is not a metric-series label, so it cannot separate historical
samples after such an edit. Renames, intervals and assignments may still be
edited; a change of measurement identity requires creating a new task. This
rule is not yet connected to the public API or configuration import.

## Queue and direct delivery

Do not put a pinned event into the existing generic queue as an ordinary event.
It needs internal-only metadata: the bound instance, session, expiration, task
configuration generation and assigned client. These are checked atomically at
enqueue and again at dequeue/response construction. No generic
`TakeV2Events`, report response, or reconnect queue push may expose it to a
consumer that cannot prove the exact active session. Legacy events keep their
existing acknowledgement and reconnect semantics.

If the guarded event has already been selected when the session changes, it
must not be returned to a replacement consumer. A late response can still
arrive at its original Agent; therefore the Agent checks the session and local
ticket expiration at **execution** time, not only at decoding. Its current
session is cleared immediately when the transport transitions or disconnects. Concurrent
checks and transition must be serialized with the start of a probe; cancellation
of an already-started old probe must not be mistaken for a network loss. A
panel-side session/assignment/config check must reject any result after the
session is retired even if an old network operation cannot be interrupted.

For direct WebSocket dispatch, capture the current session and exact socket;
serialize replacement with admission/write without holding a global connection
mutex across an unbounded write. If the write fails, enqueue only if the same
session is still active. A late failed write cannot recreate a retired queue.
Bounded write deadlines and identity checks are required. Race tests must cover
each transition at the decision, write, enqueue, dequeue and response edges.

## Results and non-probes

Use a distinct pinned result/diagnostic method carrying task ID, opaque task
configuration generation, Agent instance, transport session, dispatch ID,
role, protocol and actual family when a probe was attempted. Panel intake checks
active credential, assignment, active session, current task/config generation and
consistent actual family before any metric is written. Reject stale legacy
results for a task once pinned mode exists; otherwise an old `agent.pingResult`
could bypass the new method. Reference outcomes are separately identified and
must follow the declared independent-family contract.

The Agent's isolated probe engine separates pre-probe DNS/no-family/local
socket failures from a network failure after choosing an address. They must
become bounded, expiring per-node/task diagnostics, **not** `value=-1` samples.
The wire format and panel writer must make this invariant enforceable rather
than depend on callers remembering it. Success clears the corresponding
diagnostic; unknown/unsupported/offline remain distinguishable.

## Required executable tests

1. Old/new Agents, WS and POST, absent/malformed/expired capabilities,
   unsupported protocol versions, reconnect and downgrade never dispatch a
   pinned event through legacy handling. A capability from a different
   instance/session/socket never counts.
2. Control both sides of delayed handshakes and long polls, including a stale
   no-nonce bootstrap arriving after a newer Agent has become active. Prove no
   old request can replace a new session or dequeue its event. Cover duplicate
   responses, acknowledgement and event TTL.
3. Race session retirement against queue insert, direct WS write failure,
   fallback, report responses, disconnect and token reset. Both event delivery
   and result admission fail closed; ordinary queued commands still work.
4. Simulate old results after edits, task deletion, node reassignment, downgrade
   and reconnect; verify that neither a legacy nor pinned response can write a
   stale or mismatched-family metric, including references.
5. Verify the end-to-end fake DNS/HTTP/TLS/ICMP cases in the parent design,
   then exercise synthetic multi-node integration before any rollout.

The exact protocol, bootstrapping fence and queue implementation remain open;
this document is not proof that these races have been solved.
