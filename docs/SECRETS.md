# Secrets in this repository

## Rule

**No credential goes in a tracked file.** This repository is public. Agent tokens,
panel passwords, bot tokens and API keys belong in the panel, in an environment
variable, or in the host's own unit file on that host — never in git.

Helper scripts that need a token take it from the environment:

```bash
export NEKOMARI_AGENT_TOKEN='<token from the panel>'
bash deploy/hosts/pzyc.sh
```

Get a node's token from the panel, or via RPC:

```
POST /api/rpc2   {"jsonrpc":"2.0","method":"admin:getClientToken",
                  "params":{"uuid":"<node-uuid>"},"id":1}
```

Even a **redacted** token is a leak. Never paste one into a document, a commit
message, a screenshot or a terminal transcript, even to demonstrate that it no
longer works — write `token-1`, `token-A`, or the first two characters instead.

## Incident 1 — agent tokens committed to a public repository

### What happened

The `deploy/hosts/` files added while reconnecting the nine nodes embedded each
node's agent token literally, so the unit files could be copied straight to a
host. **Eight live tokens** were committed to `main` on a **public** repository:

| File | Nodes affected |
|---|---|
| `hosts/macwan.service` | MAC Server |
| `hosts/oc424.service` | 甲骨文 OC424 |
| `hosts/pzyc.sh` | 并行智算云服务器 |
| `enable-webssh-all.sh` | 华纳云, HK04, AkkoCloud, CloudLeadInno, Nomao |
| `hosts/oc424-verification.service` | dead — node already deleted |
| `hosts/macwan-verification-agent.service` | dead — node already deleted |

The first scan found only the `-t <token>` form. The five in
`enable-webssh-all.sh` used a `label|token` list and were missed until a second
scan looked for the token shape itself rather than the flag that precedes it.

### Impact

Small but real:

- The panel sets `private_site: true` and anonymous `/api/nodes` returns **401**,
  so **no node data was exposed** — not the metrics, not the history.
- The tokens *do* authenticate: `/api/clients/v2/rpc?token=<token>` is the agent's
  own endpoint, so a leaked token lets someone impersonate that node and report
  bogus metrics. Enough to pollute charts and trigger false alerts.

### Response

Rotated rather than merely deleted, because a credential that has been public must
be assumed compromised — removing it from the tree does not un-publish it. New
tokens were written through `admin:editClient` and read back to confirm, each
node's unit was updated on the host, and the old tokens were verified rejected
(401) while the new ones authenticated.

All eight nodes were reconnected and confirmed reporting afterwards.

The two dead-token files were deleted outright: their nodes no longer exist and
nothing referenced them. `enable-webssh-all.sh` now takes its tokens from
`NEKOMARI_NODE_TOKENS` in the environment instead of carrying them.

## Incident 2 — the rotated tokens were then written into docs/SECRETS.md

While documenting incident 1, the verification output was pasted into this file
verbatim — including the three *new* tokens. That is the same mistake the file was
being written to prevent, and it made the rotation pointless: the replacement
credentials were public before they had been in use for an hour.

Caught by scanning the tree for 22-character token shapes immediately after
committing. Fixed by rotating all three again, pushing the new values to the
agents, and replacing the evidence block with `token-1` / `token-2` / `token-3`.

Lesson: the check has to run **after** the commit that documents the fix, not only
before it. Documentation is a tracked file like any other.

## Notes

- Tokens from both incidents remain in git history. They are worthless now, so the
  history was **not** rewritten: rewriting `main` on a public repository would
  invalidate every clone and fork to remove something that no longer grants access.
- The check worth reusing: a plain HTTP request to the agent's WebSocket route
  never returns 200, so **"not 401"** is what proves a token still authenticates.
  An earlier attempt used a `Bearer` header against `/v2/rpc` and reported every
  token — valid and invalid alike — as accepted, because that is not the
  authenticated route. The real one is `/api/clients/v2/rpc?token=<token>`.
