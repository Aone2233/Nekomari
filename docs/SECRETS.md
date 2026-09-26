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

## Incident 3 — a live token was printed during a read-only audit (2026-09-26)

`systemctl show -p ExecStart` prints the whole command line, token included. An
audit of OC424 ran exactly that to find out whether the agent was on v0.1.27, and
the token came back in the output. Nothing in the repository changed, but the
credential was on screen and the session's command log keeps it.

Two lessons, and both are about the audit rather than the deploy:

- `systemctl show -p ExecStart` is a **credential read**, not a status check. Use
  `systemctl show -p ExecStart` only when the token's absence is the thing being
  verified, and pipe it through something that redacts the value:
  `systemctl show -p ExecStart myservice | sed -E 's/(-t|--token) +[^ ]+/\1 <redacted>/g'`
  — or read `/proc/<pid>/cmdline` the same way. `systemctl is-active` and the
  binary hash answer "which version is running" without touching the token.
- A token printed once is a token to rotate, even when it never entered git. The
  panel's node token is the node's whole identity: it authenticates
  `/api/clients/v2/rpc?token=…`, so a reader can report as that node.

This is also why the fix below matters: the exposure is not the audit's fault,
it is `-t` being on the command line at all.

## The fix — the token comes from a file, not the command line

`-t` still works: the panel hands out `-t <token>`, and command lines in existing
unit files are not going to be rewritten by a release. What changed is where the
token *ends up*:

| Path | Where the token lives |
|---|---|
| `deploy/install-node-agent.sh` | `-t` is moved into `<install-dir>/.agent-credentials` (mode 0600, owned by the service account) and replaced with `--token-file`, so `ExecStart` has no token |
| `deploy/install-node-agent.ps1` | same file, ACL restricted to `SYSTEM` and `Administrators`; the scheduled task's arguments carry `--token-file` |
| an existing unit | unchanged, and the agent logs a warning at startup naming the exposure and the fix |
| `--config` / a systemd `EnvironmentFile` | still supported; see the precedence below |

**Deployed on MAC-WAN on 2026-09-26** — the first node, done by hand rather than by
reinstalling. Before: `-t <token>` in a user unit, readable by any local user.
After: `--token-file /home/macos/nekomari-agent/.agent-credentials` (mode 600,
`macos:macos`), the token absent from `/proc/<pid>/cmdline` and from
`systemctl show -p ExecStart`, and ICMP/TCP tasks still reporting real values.
The full record, including the rollback and the `setcap` trap that bites any
hand-upgrade, is in `docs/DEPLOY-OC424.md`.

Reading a credential file is now a supported path, so the audit rule above needs a
second half: **`--token-file` in `ExecStart` is safe to print; `-t` is not.** A
redaction pattern that turns `-t <value>` into `<redacted>` therefore stays useful
even after a node is migrated, because it distinguishes the two cases at a glance.

The agent reads the token in this order, and never overwrites one that is already
set: `-t` / `AGENT_TOKEN`, then `--token-file` / `AGENT_TOKEN_FILE`, then
`AGENT_ENV_FILE`, then the `token` field of `--config`. A token file is an
`AGENT_TOKEN=<token>` line with a trailing newline, which means the same file
works as a systemd `EnvironmentFile=` and the unit can carry
`EnvironmentFile=/etc/nekomari-agent.env` instead of an argument. The file must not
be group- or world-readable; the agent refuses to start with a 0644 file rather
than pretend the exposure was fixed.

To move an existing node by hand, without reinstalling:

```bash
sudo sh -c 'umask 077; printf "AGENT_TOKEN=%s\n" "<token>" > /opt/nekomari-agent/.agent-credentials'
sudo chmod 600 /opt/nekomari-agent/.agent-credentials
sudo systemctl edit nekomari-agent        # then, in the drop-in:
#   [Service]
#   ExecStart=
#   ExecStart=/opt/nekomari-agent/komari-agent-linux-amd64 -e <endpoint> --token-file /opt/nekomari-agent/.agent-credentials -i 5
sudo systemctl daemon-reload && sudo systemctl restart nekomari-agent
# Verify the absence, with the output redacted as above:
systemctl show -p ExecStart nekomari-agent | sed -E 's/(-t|--token) +[^ ]+/\1 <redacted>/g'
```

Rollback is `systemctl revert nekomari-agent`, which restores the old unit with
`-t` and the original token.

## Incident 4 — the rotation script carried the admin password

`deploy/rotate-all-tokens.py` logged in with the administrator's username and
password written literally in the file, in a public repository. The one tool whose
entire job is rotating credentials was itself a published credential, on an account
that also has 2FA — so the password alone is not enough to log in, which limits the
damage to "one factor of two leaked" rather than "the panel is open".

Found on 2026-09-26 while rotating the token from incident 3, and fixed the same
way: `NEKOMARI_USER` and `NEKOMARI_PASSWORD` now come from the environment, the
TOTP half is delegated to `nekomari_auth.login` (whose `2fa_code` field is the one
the server actually reads) instead of a hand-rolled import that did not exist, and
the node map was corrected against the live `clients` table — it had a UUID for a
node that no longer exists and was missing two that do.

**The password still has to be changed.** Deleting it from the file does not
un-publish it, exactly as in incident 1. Until it is changed, the account is
protected by 2FA alone.

## Incident 5 — JPKD2's token is in its service configuration

While mapping the fleet for the same rotation, JPKD2's OpenRC service file
`/etc/conf.d/nekomari-agent` (mode 0600, which is the right idea) was read for its
shape and printed its `NEKOMARI_TOKEN` value into a terminal transcript. The same
read showed why it matters: `/etc/init.d/nekomari-agent` builds

```
command_args="-e ${NEKOMARI_ENDPOINT} -t ${NEKOMARI_TOKEN} ${NEKOMARI_EXTRA_ARGS}"
```

so the token reaches the process command line on that node anyway. The 0600
conf.d protects it at rest and not at run time.

Two things follow. The token must be rotated. And the OpenRC path needs the same
treatment D1 gave systemd: `--token-file` in `command_args` instead of `-t`, with
the token file itself kept at 0600. `deploy/hosts/nekomari-agent.openrc` is the
template to fix, and `deploy/rotate-all-tokens.py` already lists JPKD2
(`JPKD2-OPENRC`, unit `/etc/conf.d/nekomari-agent`) as a rotation target.

**Both are done, on JPKD2, on 2026-09-26.** The token was rotated (old one
invalidated), `/opt/nekomari-agent/.agent-credentials` holds the new one at mode
600 root:root, the init script passes `--token-file` and conf.d no longer has a
`NEKOMARI_TOKEN` line, and the service restarted with **zero** `-t`/`--token`
occurrences in its command line. The template now reads
`--token-file ${NEKOMARI_TOKEN_FILE}` so a new Alpine node starts on the safe
path.

One trap worth recording from doing it: the migration script originally sent
itself to the node as `ssh JPKD2 sh -s <<< payload` with the token on the first
line of stdin. That cannot work — `sh -s` consumes the *whole* stdin as the
script, so the script's own `read` gets EOF and the token is silently empty. It
was checked rather than guessed at (the same payload with the script as a file
works, with `sh -s` prints an empty variable), and the fix is to copy the script
to the node first and then feed the token on stdin.

## Incident 6 — a node's login password is in the operator's ssh config

`~/.ssh/config` on the workstation carries PZYC's password as a comment:

```
Host PZYC
    HostName 119.45.118.213
    User ubuntu
    # 密码: <plaintext>
```

Same class as incidents 1-4: a credential in a file that is not protected by being
a credential file, on a machine that is not the host it authenticates to. It was
found on 2026-09-26 while deploying v0.1.28 to that node — it is the only node not
reachable by key, which is why the password was there at all.

**Fixed during that deploy, for this node:** OC424's public key is now in both
`ubuntu`'s and `root`'s `authorized_keys`, so the fleet has a key route to PZYC and
deploys no longer need the password. The comment has not been removed, and the
password still works — that is the operator's call, and rotating it means updating
whatever else uses it.

Two lessons that generalise:

- **A password in a config comment is still a password.** Comments are not
  protected by ssh's own file checks, they are read by anyone with the file, and
  they end up in transcripts and backups. A key is the fix, not a quieter comment.
- **`sudo tee` truncates.** Writing root's `authorized_keys` with
  `sudo -n tee /root/.ssh/authorized_keys` replaced the file. Appending needs
  `tee -a`. Recorded in `docs/DEPLOY-OC424.md` with what was lost, which for this
  node is unknown because no backup existed.

## Notes

- Tokens from both incidents remain in git history. They are worthless now, so the
  history was **not** rewritten: rewriting `main` on a public repository would
  invalidate every clone and fork to remove something that no longer grants access.
- The check worth reusing: a plain HTTP request to the agent's WebSocket route
  never returns 200, so **"not 401"** is what proves a token still authenticates.
  An earlier attempt used a `Bearer` header against `/v2/rpc` and reported every
  token — valid and invalid alike — as accepted, because that is not the
  authenticated route. The real one is `/api/clients/v2/rpc?token=<token>`.
