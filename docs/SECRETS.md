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

## Incident: agent tokens committed to a public repository (2026-09-17)

### What happened

The `deploy/hosts/` files added while reconnecting the nine nodes embedded each
node's agent token literally, so that the units could be copied straight to the
host. Four tokens were committed to `main` on a **public** repository:

| File | Token status |
|---|---|
| `hosts/macwan.service` | live — node "MAC Server \| 微服务器" |
| `hosts/oc424.service` | live — node "甲骨文 OC424" |
| `hosts/pzyc.sh` | live — node "并行智算云服务器" |
| `hosts/oc424-verification.service` | dead — the node was already deleted |
| `hosts/macwan-verification-agent.service` | dead — the node was already deleted |

Found by scanning the working tree for the known token shapes while checking
whether the repository needed updating.

### Impact

Small but real:

- The panel sets `private_site: true`, and anonymous `/api/nodes` returns **401**,
  so **no node data was exposed** — not the metrics, not the history.
- The tokens *do* authenticate. A request to
  `/api/clients/v2/rpc?token=<token>` is accepted, which is the agent's own
  endpoint: a leaked token lets someone impersonate that node and report bogus
  metrics. That is enough to pollute charts and trigger false alerts.

### Response

The three live tokens were **rotated** rather than merely deleted, because a
credential that has been public must be assumed compromised — removing it from
the tree does not un-publish it.

1. New 22-character tokens generated and written through
   `admin:editClient`, then read back to confirm.
2. Each node's agent unit updated on the host and restarted.
3. Old tokens verified rejected and new ones accepted:

```
old  ZIbRPVhoIjF7wRzGwZffaC  ->  401
old  HW41Se2JiX2861PARbuyni  ->  401
old  HK8hyFjfQxB4Dgx8cW2Zzp  ->  401
new  JFslls5bBfrYyWfsEonicc  ->  400   (400 = authenticated, rejected only
new  Z2FG4IYqyO1hUdqnUynhCp  ->  400    for lacking WebSocket headers)
new  rridzGHbcYtAoLKavOxKNE  ->  400
```

The two dead-token files were deleted outright: their nodes no longer exist and
nothing referenced them.

### Notes

- The tokens remain in git history. They are worthless now, so the history was
  **not** rewritten — rewriting `main` on a public repository would invalidate
  every clone and fork to remove something that no longer grants access.
- The 400-versus-401 distinction is the check worth reusing: a plain HTTP request
  to the agent's WebSocket route never returns 200, so "not 401" is what proves a
  token still authenticates. An earlier attempt used a `Bearer` header and
  `/v2/rpc` and reported every token — valid and invalid alike — as accepted,
  because that route is not the authenticated one.
