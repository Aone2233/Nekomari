# AGENTS.md

Instructions for coding agents working in this repository. They restate the house rules in
[`CONTRIBUTING.md`](./CONTRIBUTING.md) in the form an agent needs; where the two differ,
`CONTRIBUTING.md` and the documentation it links are authoritative. Nothing here overrides
the operator's own instructions.

## Read these first, in this order

| File | Why before you start |
|---|---|
| [`CONTRIBUTING.md`](./CONTRIBUTING.md) | House rules: no credentials in tracked files, upstream attribution, the agent binary name, what a good change looks like here |
| [`docs/OPEN-WORK.md`](./docs/OPEN-WORK.md) | The **only** file that says what is still open. Read this and nothing else for "what is left" |
| [`docs/REJECTED.md`](./docs/REJECTED.md) | Before proposing an approach: check it is not a path already closed, with the reason and the evidence |
| [`docs/TESTING.md`](./docs/TESTING.md) | Which tests are hermetic, which need root/IPv6/network, and why the CI skip list is a denylist |
| [`FORK.md`](./FORK.md) | Provenance, and the changes this fork made over upstream — several are load-bearing |

## The rule that is easiest to get wrong

`docs/` is the engineering record, organised by moment: a review, a follow-up, a deployment
on a given day. **A dated record is not a statement about now.** When you change a fact,
change it in the file that owns it, and do not add a fourth file that describes the same
state differently — three documents each presenting a superseded state as current is the
specific failure [`OPEN-WORK.md`](./docs/OPEN-WORK.md) was rewritten on 2026-10-13 to end.

`site/` holds product documentation for operators and is published at
`nekomari-docs.66040321.xyz`. `docs/` is deliberately **not** published; only
`docs/IP-INFO-API.md` and `docs/THIRD-PARTY-LICENSES.md` are reused, because they are
already written for an operator. Do not add other `docs/` pages to the site navigation.

## Hard constraints

- **CGO is required.** The server links SQLite through CGO; `CGO_ENABLED=0` does not build.
- **Never rename `komari-agent-<os>-<arch>`.** The agent's self-updater looks for exactly
  that filename and breaks silently otherwise.
- **The shipped version must be plain `X.Y.Z`.** `v1.6.11-rc1` and `v1.6.10-3-g4e7d389` both
  fail to parse on both ends of the plugin version gate; the tag, `build.sh` and
  `internal/plugin/version_test.go` each enforce this.
- **Preserve the MIT text and the upstream copyright notices** verbatim. User-visible
  product names say Nekomari; the notices do not get rebranded.
- **No credentials in tracked files.** This repository is public. A *redacted* token still
  counts as a leak — see [`docs/SECRETS.md`](./docs/SECRETS.md).
- **Raw machine evidence does not get committed.** `.runtest/`, `.accuracy-smoke/` and
  `theme-luminaplus/` are gitignored; the scripts and the redacted write-up are what land.

## Commands

```bash
./build.sh                      # frontend -> embedded theme -> server -> agent
go test ./... -count=1 -skip '^TestIpInfo$'
(cd agent && go test ./... -count=1 -skip 'TestICMPPing|TestTCPPing|TestHTTPPing')

cd frontend && npm ci --no-audit --no-fund
npm test && npm run lint && npm run audit:compiler && npm run build

node site/script/build.mjs stage && node site/script/build.mjs check   # docs site navigation

deploy/deploy-verify.sh         # downloads the published artifacts and proves they deploy
```

The skip patterns are anchored on purpose: `^TestIpInfo$` skips only the one test that
calls ipinfo.io, while Nekomari's thirty-plus hermetic `TestIpInfo*` cases keep running. Do
not convert them into a `-run` allowlist — that silently stopped every test written after
the list was made, and [`docs/TESTING.md`](./docs/TESTING.md) records what it cost.

`npm run lint` is not the React Compiler gate: eslint enables eight `react-hooks` rules, so
the shapes the compiler rejects are green under it. `audit:compiler` runs separately.

## What "done" looks like here

- **The commit message explains the cause, not just the fix** — including the hypotheses
  that were tested and eliminated, because that is the part a future reader cannot
  reconstruct from the diff.
- **Say what you verified, and how.** "Ran `deploy/deploy-verify.sh` against the published
  artifacts, 13/13" beats "tested locally". If something is untested, say so.
- **Prefer failing loudly.** A wrong number that looks plausible is worse than an error.
- **An approach you considered and dropped goes in [`docs/REJECTED.md`](./docs/REJECTED.md)**,
  with its best case and why it lost. An approach still open goes in
  [`docs/OPEN-WORK.md`](./docs/OPEN-WORK.md), in the same commit as the code.
- **When a check cannot be automated, write down what it ruled out.** That is cheaper than
  the next person spending the same time on the same hypothesis.

## Where things live

| Path | What it is |
|---|---|
| `main.go`, `cmd/`, `internal/`, `pkg/`, `web/`, `protocol/` | server |
| `agent/` | agent, a nested Go module |
| `frontend/` | panel UI (Vite + React), with browser specs under `frontend/script/` |
| `site/` | the published documentation site; `content/` is the only hand-edited Markdown |
| `deploy/` | deployment, verification and fleet scripts — `README.md` organises them by task |
| `docs/` | engineering record, not published |
| `CHANGELOG.md` | what was wrong and what changed, per release |
