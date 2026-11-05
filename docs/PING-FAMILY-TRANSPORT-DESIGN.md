# Family-pinned probes: transport fencing design

Status: draft implementation and test plan. The feature branch has a WS-only
challenge/ack handshake on both sides; no pinned event or result consumes it.
POST sessions, delivery and result fencing remain unimplemented. None of this
authorizes a release or changes live tasks.
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
pin, task/config generation, session token, or expiration without issuing any
probe. Legacy `agent.ping` and its wire shape stay unchanged.

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
arrive at its original Agent; therefore the Agent checks the session and event
expiration at **execution** time, not only at decoding. Its current session is
cleared immediately when the transport transitions or disconnects. Concurrent
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
configuration generation, Agent instance, transport session, role, protocol
and actual family when a probe was attempted. Panel intake checks active
credential, assignment, active session, current task/config generation and
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
