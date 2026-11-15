# Contributing

Nekomari is a maintained fork of [Komari](https://github.com/komari-monitor/komari),
which upstream archived at `1.5.0-fix1`. Issues and pull requests are welcome.

## Before you start

For anything larger than a bug fix, open an issue first. It is cheaper to disagree
about an approach in a paragraph than in a finished patch — especially for changes
to the RPC surface or the database schema, where a wrong direction is expensive to
undo.

## Building and testing

Requires Go (see `go.mod`), Node 22+, and a C toolchain: the server links SQLite
through CGO, so `CGO_ENABLED=0` will not build it.

```bash
./build.sh          # frontend -> embedded theme -> server -> agent
```

Run the hermetic tests the way CI does:

```bash
go test ./... -count=1 -skip '^TestIpInfo$'
(cd agent && go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing')
```

It is a denylist, not an allowlist. An allowlist was in use here until 2026-10-13 and
silently stopped running every test written after the list was made — 552 root tests down
to 79, which hid two `agent/unlock` tests that had been failing since the commit that
added them. The anchors in the patterns matter: `^TestIpInfo$` skips only the one test that
calls ipinfo.io, while the thirty-plus hermetic `TestIpInfo*` cases keep running.

[`docs/TESTING.md`](./docs/TESTING.md) explains which tests are hermetic, which need
a privileged or IPv6-capable host, and which two fail in unprivileged environments
for reasons unrelated to the code.

CI runs on Linux and Windows for every push and pull request. A release also runs
`deploy/deploy-verify.sh`, which downloads the published artifacts and proves they
actually deploy.

## What a good change looks like here

**Explain the cause, not just the fix.** The commit messages in this repository
describe what was wrong and how it was established — including the hypotheses that
were tested and eliminated. That is the part a future reader cannot reconstruct from
the diff.

**Say what you verified, and how.** "Tested locally" is weaker than "ran
`deploy/deploy-verify.sh` against the published artifacts and it passed 13/13". If
something is untested, say so rather than implying otherwise.

**Prefer failing loudly to failing silently.** A wrong number that looks plausible
is worse than an error. Several of the fixes in `CHANGELOG.md` are of this kind: a
favicon that silently kept the old icon, a version banner that silently compared
against the wrong repository, a container that built green and could not start.

**When a check cannot be automated, write down what it ruled out.** The IP-panel
investigation in [`docs/IP-INFO-API.md`](./docs/IP-INFO-API.md) records four
eliminated hypotheses so the next person does not spend the same time on them.

**An approach you drop goes in [`docs/REJECTED.md`](./docs/REJECTED.md).** That file is the
register of paths already closed, with the best case each one had and why it lost — so a
plausible-sounding idea that already failed does not get proposed again. It is not the only
place a decision is recorded, but it is the only place they are all listed.

## House rules

**No credentials in tracked files.** This repository is public. Agent tokens, panel
passwords, bot tokens and API keys go in the panel, an environment variable, or the
host's own unit file. Helper scripts take them from the environment. See
[`docs/SECRETS.md`](./docs/SECRETS.md) for the rule, the incident it came from, and
why a *redacted* token still counts as a leak.

**Keep the upstream attribution.** The MIT licence text and the copyright notices
naming Komari Monitor are required by the licence and must not be rebranded. Product
names that users see — the 2FA issuer, the PWA manifest — should say Nekomari.

**Do not rename the agent binary.** `komari-agent-<os>-<arch>` is the filename the
agent's self-updater looks for; renaming it breaks auto-update silently.

## Reporting a bug

Include what you expected, what happened, and how you got there. If it involves
monitoring data, say which target and protocol the task uses — a flat 100% loss is
more often a protocol or address-family mismatch than an outage, and `nekomari
netcheck <target>` will tell you which. See [`docs/TESTING.md`](./docs/TESTING.md).

For anything security-related, please do not open a public issue first.
