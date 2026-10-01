# VERIFICATION 2026-10-01 — independent reverse review of the v1.6.9 hardening batch

Reviewer: `notifier-fix`, acting as an independent verifier. I wrote **none** of the changes below.
Branch: `codex/v1.6.9-hardening`, working tree frozen by the Lead (`git status` at the start of the
review: 29 modified files, 22 untracked — reproduced at the end, unchanged except this file).

**How to read this document.** Every claim is tagged:

- **[executed]** — I ran the command in this workspace and quote its output. I do **not** rely on any
  author's screenshot, comment or summary.
- **[read-only]** — I could only read the code; I state the reasoning and the exact file:line, and do
  not present it as observed behaviour.

Nothing in this review changes the tree permanently. Temporary reversions were made from
`$TEMP` backups and restored; the final re-gate and `git status` are in §9/§10.

---

## 0. Verdict

| # | Item | Verdict |
|---|---|---|
| P1-5 | `/api/public/ip-info/v1/lookup?uuid=` no longer leaks a node's `egress_ip` | ✅ fix correct, reverse-verified |
| P1-5‑x | **Same class still open on three anonymous RPC surfaces** (`common:getNodeRecentStatus`, `common:getRecords`, `public:getPublicPingTasks`) | ❌ **counterexample I constructed, executed** |
| P1-6 | agent no longer reports "slow but reachable" as 100 % packet loss | ✅ fix correct, reverse-verified; real loss still recorded |
| P1-7 | SSO callback requires the account's second factor before `CreateSession` | ✅ fix correct, reverse-verified at HTTP **and** DB level |
| P1-7 | `/oauth2/bind` + `/unbind` behind `RequireSensitive2FA` | ✅ fix correct, reverse-verified |
| P1-7 | limiter reuse, single-use state, API-key exemption, no-2FA path | ✅ confirmed (one item **[read-only]**) |
| T5/T8 | `latest` truth table, no ref-derived `${{ }}` in `run:`, container marker | ✅ 5 independent break tests + 3 anti-vacuity probes, all non-zero |
| T5/T8 | **`deploy/docker-workflow-check.py` is not run by any workflow** | ❌ **finding: a check nobody runs** |
| T5/T8 | `commandLineToken` vs pflag | ✅ reverse-verified; two declared-unchased spellings **measured**, one is a real warning gap |
| T9 | frontend SSO 2FA gate + no-2FA path unchanged | ✅ reverse-verified (module + page + browser spec executed locally) |
| P1-13 | Dockerfile.agent ↔ matrix ↔ `containerMarkerPath` | ✅ three-way consistent, break-tested |
| — | `docs/RELEASING.md` claim about the cancelled case | ❌ **contradicts the workflow and the truth table** |
| §3 | assertions deleted from the diff | ✅ none weakened; the one changed expectation is justified |

Gates: root `go vet`/`go test` ✅, agent `go vet`/`go test` ✅, CI race command ✅, `npx tsc -b` ✅,
`npm test` 119/119 ✅, `python deploy/docker-workflow-check.py` ✅ (see §9).

---

## 1. P1-5 — anonymous `uuid` read of a node's `egress_ip`

**What the fix does.** `web/api/ipinfo/handler.go:88-105` resolves the query `uuid` **before** the
upstream lookup and refuses a non-readable one with `404 {"error":{"message":"unknown node: <uuid>"}}`;
the visibility rule lives in `database/unlock/unlock.go:60-94` (`ClientHistoryReadable` + `LoadVisible`),
`unlock.Load` was made unexported (`load`), and `buildLookupData` no longer reads unlock data by itself
(`handler.go:202`). `Refresh` passes `loggedIn=true` because it is mounted under `RequireRole(admin)`
(`web/router/router.go:92,108`).

### Executed evidence, positive

```
$ go test ./web/api/ipinfo/... ./database/unlock/... -count=1
ok  	github.com/Aone2233/nekomari/web/api/ipinfo	0.223s
ok  	github.com/Aone2233/nekomari/database/unlock	0.033s
```

### RV-1 [executed] — data layer, hidden filter removed

`return known && (!isHidden || loggedIn)` → `return known && (isHidden == isHidden)`:

```
=== RUN   TestClientHistoryReadableMatchesTheJsonrpcRule
    unlock_test.go:96: ClientHistoryReadable("unlock-hidden", loggedIn=false) = true, want false
--- FAIL: TestClientHistoryReadableMatchesTheJsonrpcRule (0.00s)
=== RUN   TestLoadVisibleGuestReadsOnlyNonHiddenNodes
    unlock_test.go:117: LoadVisible("unlock-hidden", guest) error = <nil>, want ErrNotVisible
--- FAIL: TestLoadVisibleGuestReadsOnlyNonHiddenNodes (0.00s)
```
Restored → `ok github.com/Aone2233/nekomari/database/unlock`.

### RV-2 [executed] — handler, 404 mapping removed

`respondError(c, http.StatusNotFound, "unknown node: "+uuid)` → `_ = uuid`:

```
    lookup_access_test.go:64: status = 200, want 404: 
--- FAIL: TestLookupHiddenNodeIsNotReadableForGuests (0.00s)
--- PASS: TestHiddenAndUnknownNodesAnswerTheSameWay (0.00s)
```
(The second test passes under this mutation because both answers become 200 — it only asserts that
hidden and unknown *match*, which is worth knowing when reading it.)

### Executed evidence, other claims

- Empty `uuid=` resolves no node: `TestLookupEmptyUUIDStaysResolvableWithoutNodeData` fails the test if
  the loader is entered at all, and passes (executed above).
- `unlock.Load` has no other callers **[executed grep]**:
  `git grep -n 'unlock\.[A-Za-z]' -- '*.go'` → only `unlock.Save` (`web/api/client/report_v2.go:97`)
  and `unlock.LoadVisible` (`web/api/ipinfo/handler.go:226`). No package can still read by uuid
  without the visibility gate.
- Admin path: `isAdmin` reads `api.GetRole`, which is set by `api.IdentityMiddleware`
  (`web/api/Auth.go:30-55`), installed globally at `internal/server/runtime.go:114` — so in production
  an admin really does get `RoleAdmin` on the public route **[read-only]**.

### ❌ Counterexample I constructed (not in the task's list) — [executed]

`pkg/rpc/permission.go` grants `common:*` to **guest** (`Allow("common:*", RoleGuest)`), and
`public:*` likewise (`web/rpc/jsonrpc/public.go:23`). Three anonymous surfaces still answer a
uuid-dependent question without the existence+hidden rule:

I drove the real handlers with an anonymous principal (`rpc.NewAnonymousPrincipal()`), a hidden client,
a visible client and an unknown uuid, in a temporary probe test inside `web/rpc/jsonrpc` (deleted
afterwards; see §10):

```
PROBE common:getNodeRecentStatus hidden  -> ERROR(-32602) "UUID is required"
PROBE common:getNodeRecentStatus visible -> {"count":0,"records":[]}
PROBE common:getNodeRecentStatus unknown -> {"count":0,"records":[]}
PROBE common:getRecords hidden  -> ERROR(-32602) "UUID not found"
PROBE common:getRecords unknown -> ERROR(-32603) "Failed to fetch records"
PROBE public:getPublicPingTasks (all tasks, guest) -> [{"id":1,...,"clients":["zz-verify-hidden-node","zz-verify-visible-node"],...}]
```

Conclusions, all from that output:

1. **`common:getNodeRecentStatus` is a hidden-node oracle.** `web/rpc/jsonrpc/common.go:508-510`
   rejects a hidden uuid with an error and lets an unknown one through to a success envelope, so
   `ERROR(-32602)` vs `{"count":0}` tells an anonymous caller "this uuid is a hidden node". P1-5's own
   fix treats exactly this distinction as the thing to avoid ("两者一旦可区分，这个接口就成了隐藏节点
   存在性的判定器") — the same reasoning was not applied here.
2. **`common:getRecords` is a hidden-node oracle too.** `web/rpc/jsonrpc/common.record.go:77-79`
   returns `-32602 "UUID not found"` for hidden; an unknown uuid takes a different path
   (`-32603` here, or a 200 with empty records when the metric store is up). Again distinguishable.
3. **`public:getPublicPingTasks` hands out hidden nodes' UUIDs.** `web/rpc/jsonrpc/public.go:257-269`
   copies `task.Clients` verbatim with no hidden filtering, so an anonymous caller enumerates the
   node UUIDs of every ping task — including hidden ones. The same raw list is echoed at
   `common.record.go:379` (`common:getRecords`, ping branch) and `public.go:480`
   (`public:getPingRecords`, task filter) **[read-only for those two: same field, same data path]**.

Not a regression from this batch — all four sites are untouched, pre-existing code. But P1-5's class
is not closed, and the audit should say so rather than "anon uuid reads are fixed".

**Minimal fix.** These three RPC methods should answer a uuid question through the same rule the
public history methods already use (`web/rpc/jsonrpc/public.go:57-60 clientHistoryReadable`, which is
byte-for-byte the new `database/unlock.ClientHistoryReadable`): look the uuid up in
`clients.HiddenClients()` and return the identical not-found answer for hidden *and* unknown, and
filter hidden uuids out of the `clients` echo. Sharing one helper (the new exported
`unlock.ClientHistoryReadable` is the natural candidate) also removes the current "两处必须同进同退"
maintenance trap.

---

## 2. P1-7 — SSO bypassed TOTP; bind/unbind were not sensitive operations

**What the fix does.** `web/api/public/oauth.go:180-183` runs `verifySSOSecondFactor` **before**
`accounts.CreateSession` (`:186`); `verifySSOSecondFactor` (`:217-233`) spends the shared
`defaultLoginLimiter`, sets `uuid`, then delegates to `api.VerifySensitive2FA`
(`web/api/AuthSensitive.go:29-62`, the same core `login.go:100-111` obeys). `web/router/router.go:146-147`
puts `api.RequireSensitive2FA()` in front of `/oauth2/bind` and `/oauth2/unbind`.

### Executed evidence, positive

```
$ go test ./web/api/public/... ./web/router/... -count=1
ok  	github.com/Aone2233/nekomari/web/api/public	0.308s
ok  	github.com/Aone2233/nekomari/web/router	0.154s
$ go test -race ./database/accounts ./web/api/public -count=10 -shuffle=on
ok  	github.com/Aone2233/nekomari/database/accounts	3.722s
ok  	github.com/Aone2233/nekomari/web/api/public	8.134s
```

### RV-3 [executed] — the callback gate disabled

`if status, message := verifySSOSecondFactor(...); status != 0 {` → `... ; false && status != 0 {`:

```
    oauth_secondfactor_test.go:88: an SSO login created a session for a 2FA account without a second factor: 302 <a href="/admin/dashboard">Found</a>.
    oauth_secondfactor_test.go:102: a wrong TOTP code still produced a session: 302 <a href="/admin/dashboard">Found</a>.
--- FAIL: TestOAuthCallbackRequiresSecondFactorFor2FAUsers (0.02s)
```
This is the vulnerability itself: P1-7 was a session, not just a missing cookie.

### RV-4 [executed] — the bind/unbind gate removed

Both `api.RequireSensitive2FA(),` arguments deleted:

```
    oauth2_bind_sensitive_test.go:67: bind status 302, want 401: <a href="/api/oauth">Found</a>.
    oauth2_bind_sensitive_test.go:77: unbind status 200, want 401: {"status":"success","message":""}
```
Unbind succeeding with a session cookie alone is the pre-fix behaviour (a credential change, one
`POST`).

### Ordered before `CreateSession` — [executed], DB level

`oauth_secondfactor_test.go:58-72 requireNoSessionFor` reads `accounts.GetAllSessions()` and fails if a
row exists for the account; both refusal subtests call it, and they pass. So the test is not merely
"no cookie" — the database is asserted to have no session.

### Limiter really shared — [executed]

Code says `verifySSOSecondFactor` uses `defaultLoginLimiter`, the same var `login.go:77,94,102,112`
uses **[read-only]**. I also measured it end-to-end with a temporary probe (deleted afterwards): five
refused SSO callbacks from one IP/account, then a **password** login for the same account:

```
PROBE sso-refusal attempt 5 -> status=401 body={"message":"Invalid 2FA code","status":"error"}
PROBE sso-refusal attempt 6 -> status=429 body={"message":"Too many login attempts. Try again later.","status":"error"}
PROBE password login after those refusals -> status=429 retry-after="300" body={"status":"error","message":"Too many login attempts. Try again later."}
```
The 6th SSO attempt hitting the account bucket (burst 5) and the *password* login being throttled with
the account bucket's 300 s refill proves they are one limiter, not two.

### Pending state cannot be replayed — [executed + read-only]

`web/oauth/internal/oauthutil/state.go:37-43` deletes the entry before checking expiry, and both
providers consume it (`generic.go:44`, `github.go:45`) before resolving the user. The existing
`state_test.go:11-29` runs 32 concurrent `Consume` calls and requires exactly one success; the package
is green. So each 2FA refusal burns its state and the attacker needs a fresh `/api/oauth` start, which
the separate per-IP OAuth start bucket (`oauth.go:38-46,88`) bounds.

### OIDC enabled + account with 2FA → fail closed, no half session — [executed]

`TestOAuthCallbackRequiresSecondFactorFor2FAUsers` runs against a *generic OIDC* provider
(`oauthTestInstance(t, "generic", ...)`) with `OAuthEnabledKey=true`; the account is bound to the
provider identity and has TOTP. Refusals are 401 + `requireNoSessionFor`; a valid code is 302 with a
cookie. That is exactly "fail closed, no half session".

### No-2FA path — [executed + read-only]

`TestOAuthCallbackWithout2FAIsUnchanged` (302 + cookie + `Location: /admin/dashboard`) passes.
`api.VerifySensitive2FACore` returns `nil` before touching the code when `user.TwoFactor == ""`
(`AuthSensitive.go:40-42`) **[read-only]**.

One honest caveat: "unchanged for a no-2FA user" is true for the *outcome*, but the callback now first
calls `defaultLoginLimiter.Allow(ip, account, now)` (`oauth.go:220`). `Allow` neither consumes nor
creates a bucket (`login_limiter.go`), so a successful SSO login changes nothing, but an IP/account
already over the login budget now gets **429 on the SSO callback** where it previously logged in. That
is the intended cost of sharing the bucket, not a defect — it is simply not literally "byte-for-byte".

### API Key exemption — [read-only], **no test covers it**

`VerifySensitive2FACore` returns `nil` first thing when `isAPIKey` (`AuthSensitive.go:30-32`), and
`verifySSOSecondFactor` inherits it because `VerifySensitive2FA` reads `c.Get("api_key")`
(`AuthSensitive.go:58`). `git grep -ln 'VerifySensitive2FA' -- '*_test.go'` finds **no test**, and
`web/router/hardening_test.go` does not cover it either. Confirmed as existing design, as the task
asked; flagged only because it is an exemption in a privilege gate with zero coverage.

### Residual risks worth recording (all [read-only], no action required by this task)

1. **The TOTP now travels in a GET query string** — `frontend/src/lib/oauth2Binding.ts:27-38` builds
   `/api/admin/oauth2/bind?2fa_code=…`, and `get2FACode` reads query params (`AuthSensitive.go:76-80`).
   A top-level redirect cannot carry a header, so this is inherent; the consequence is that a one-shot
   TOTP lands in the panel's access log and the browser history. Not a regression (nothing was sent
   before), but `docs/SECRETS.md` documents the *token* handling carefully and does not mention this.
2. **`/bind` is requested twice on the success path — measured** (temporary one-line tightening of the
   browser spec, restored afterwards):
   ```
   AssertionError: 2 != 1 : MEASUREMENT of bind requests on the success path:
   ['http://127.0.0.1:20353/api/admin/oauth2/bind?2fa_code=123456',
    'http://127.0.0.1:20353/api/admin/oauth2/bind?2fa_code=123456']
   ```
   Harmless today: `admin.BindingExternalAccount` only re-sets the same
   `binding_external_account` cookie and 302s (`web/api/admin/oauth.go:13-22`), and `Verify2Fa` is
   stateless (`database/accounts/2fa.go:68-86`), so the code is still valid for the navigation. The
   narrow edge is a TOTP-window rollover between the probe and the navigation (≈ms gap against a 30 s
   window): the navigation would then land on a raw JSON 401. Worth a comment, not a blocker.
   Note the browser spec asserts `set(bind_urls) == {…}`, which by construction cannot see the
   duplicate; that is why I measured it instead of trusting the test.

---

## 3. P1-6 — successful probes reported as 100 % packet loss

**What the fix does.** `agent/server/task.go:582-607`: a retry that returns an error is still `-1`
(loss), but exhausting every retry while *all* measurements succeeded now returns the last measured
latency with `ok == true`, instead of `-1`.

### RV-5 [executed] — the pre-fix ending restored

`return lastLatency, true` → `return -1, false`:

```
--- PASS: TestMeasureWithRetriesReportsSuccessAfterRetransmit (0.01s)
    ping_retry_test.go:162: measureWithRetries = (-1, false), want (1290, true)
--- FAIL: TestMeasureWithRetriesOtherPaths (0.00s)
    ping_retry_test.go:189: 四次探测都成功却被上报为丢包（P1-6）：慢不等于丢
--- FAIL: TestMeasureWithRetriesNeverReportsLossWhenEveryProbeSucceeded (0.00s)
--- PASS: TestMeasureWithRetriesStillReportsLossOnRealFailure (0.00s)
```
Two things worth noting in that output: the new regression test fails, and the *loss* test **passes
both before and after** — so the relaxation did not wash away real loss.

### Real loss still recorded — [executed]

`ping_retry_test.go:110-142` covers `first-attempt-fails` (i/o timeout) and `retry-itself-fails`
(connection refused), both expecting `(-1,false)`; `TestMeasureWithRetriesStillReportsLossOnRealFailure`
(`:190-204`) covers "all attempts slow, last one times out". All pass on the frozen tree, and all but
the new one pass on the reverted tree as well.

### `family` consistency — [read-only]

`family` is written inside the `measure` closure (`task.go:505-527`); `measureWithRetries` assigns
`lastLatency` from every successful call (`:585-597`) and returns that same value, so the family
belongs to the call whose latency is reported. The one case that could have diverged — the last retry
succeeding but being ignored — is exactly what the fix removed (the old code returned `-1` there).

### Was the changed test assertion "just another magic value"? — no

`-want: -1, wantOK: false,` → `+want: 1290, wantOK: true` inverts the *meaning* of the case
(`every-attempt-stays-high-is-slow-not-lost`). Its justification is external to the test and I checked
it independently: `internal/metricstore/ping_records.go:61-64` maps `Value<0` to `ping.loss=1`, and
`internal/sla/availability.go:258` raises a fault event from loss ≥ 0.5 **[read-only]**; the new
`TestSlowButSuccessfulProbeReachesThePanelAsLatencyNotLoss` carries that mapping through the real
`BuildPingResultPayloadWithRoleAndFamily` payload. So the new expectation encodes the panel contract,
not a new constant agreed with the implementation.

---

## 4. T5/T8 — docker workflow, container marker, `-t` deprecation

### 4.1 The checker run and my own break tests — [executed]

Baseline:

```
$ python deploy/docker-workflow-check.py
… truth table (8 rows: workflow_run success/failure/timed_out/action_required/cancelled,
  release published, workflow_dispatch, prerelease) …
latest policy: enable=!contains(steps.tag.outputs.tag, '-') && steps.tag.outputs.release_verified == 'true'
truth table rows checked: 8
interpolations left in `run:` scripts (workflow constants, not ref data):
  - 'Smoke test the image actually starts' still interpolates `${{ matrix.name }}` … (5 notes, all matrix)
all docker workflow contract checks passed
EXIT=0
```

Five破坏 tests, each on a **copy** (or a temporary one-line revert), never trusting the author's
meta-check:

| Break | Mutation | Result |
|---|---|---|
| 1 | `latest` enable drops `&& release_verified == 'true'` | `EXIT=1` — "the latest policy changed" |
| 2 | `tag="$TAG"` → `tag='${{ steps.tag.outputs.tag }}'` (the v1.6.6 shape) | `EXIT=1` — "step 'Check the Release exists' interpolates `${{ steps....`" |
| 3 | `if [ "$EVENT_NAME" != "workflow_run" ] \|\| [ "$TRIGGER_CONCLUSION" = "success" ]` → `if true` | `EXIT=1` — 6 problems, e.g. "release_verified='true' for workflow_run failure, want 'false'" |
| 4 | `RUN touch /.komari-agent-container` removed from `Dockerfile.agent` | `EXIT=1` — "contains no `touch` at all" |
| 5 | `containerMarkerPath` in `agent/update/update.go` changed to `…-v2` | `EXIT=1` — "never touches '/.komari-agent-container-v2' … touch lines found: line 30" |

Break 3 is the important one: the truth table is genuinely produced by **executing the workflow's own
`Resolve tag` script** in bash (the failure appears per case, with values read from `$GITHUB_OUTPUT`),
so it cannot agree with a bug in the Python re-implementation.

Anti-vacuity probes (can it pass by accident?) — [executed]:

- valid YAML with no `jobs` → `EXIT=1` (a Python `KeyError: 'jobs'` traceback, see finding F6)
- unparsable YAML → `EXIT=1` (traceback)
- nonexistent path → `EXIT=1` (`does not exist`)

All three are non-zero, so the checker cannot silently pass on a workflow it failed to understand.

### 4.2 ❌ The checker is run by nobody — [executed]

```
$ git grep -n 'docker-workflow-check' -- .github/ deploy/ docs/
deploy/README.md:53:| `docker-workflow-check.py` | Pins three promises … |
```
The `deploy-scripts` CI job (`.github/workflows/ci.yml:125-141`) runs three shell tests and has no step
for it; no other workflow mentions it; `docs/TESTING.md`'s list (updated in this batch) does not either.
So the only thing that proves the `latest` policy, the ref-injection ban and the container marker is a
script a human has to remember to run — the same "added but never run" failure this batch invented
`frontend/script/browser-spec-registry.test.mjs` to prevent for browser specs (`:10-14` "a new spec file
is simply never run… every CI run stays green").

**Minimal fix.** Add to `deploy-scripts` (ubuntu, offline, no docker needed):
`python -m pip install pyyaml && python deploy/docker-workflow-check.py`. PyYAML must be installed
explicitly because the checker exits 2 when it is missing — by design, and that design is right.

### 4.3 `Resolve tag` has no `${{ }}` — [executed]

The checker's own notes list every remaining `${{ … }}` inside `run:` scripts: five, all
`matrix.*` in the smoke-test step, all workflow constants (matrix entries cannot carry a ref name).
The `Resolve tag`, `Check the Release exists`, `Assemble the build context` and
`Verify the image is anonymously pullable` scripts have none. `TAINTED_EXPRESSION`
(`docker-workflow-check.py:58-60`) covers `github.event*`, `github.head_ref`, `github.ref`,
`github.ref_name`, `inputs.`, `steps.` — i.e. the ref-derived set. Two limits worth knowing
**[read-only]**: `github.event_name` does not match (`\b` fails before `_`), which is harmless because
it is not ref-derived; and `matrix.*`/`github.repository*` are not in the tainted set, which is
correct because they are static.

### 4.4 `commandLineToken` vs pflag — [executed]

`TestCommandLineTokenMatchesPflagForEverySpelling` does not hard-code expectations: it runs the real
`RootCmd.ParseFlags` and compares `flags.Token` with the detector for 12 spellings.

RV-6: I spliced the **HEAD** implementation of `commandLineToken` back in:

```
    token_deprecation_test.go:63: using -t/--token printed no deprecation warning
--- FAIL: TestTokenFlagDeprecationWarningPointsAtTokenFileOnEverySpelling (0.00s)
    token_deprecation_test.go:209: commandLineToken([-tpflag-table-token-value]) says the flag was used=false, but pflag parsed token "pflag-table-token-value"
    token_deprecation_test.go:213: commandLineToken([-t=]) = "", but pflag parsed "="
    token_deprecation_test.go:209: commandLineToken([-token-file x]) says the flag was used=false, but pflag parsed token "oken-file"
    token_deprecation_test.go:209: commandLineToken([-- -t pflag-table-token-value]) says the flag was used=true, but pflag parsed token ""
--- FAIL: TestCommandLineTokenMatchesPflagForEverySpelling (0.00s)
```
Four real pre-fix divergences, including `-tv` (the hole the fix was for) and the `--` terminator.

I then measured the two spellings the comment says are deliberately **not** chased (temporary probe,
deleted afterwards):

```
PROBE pflag args=[-u -e https://panel.example -ut zz-secret] -> token="zz-secret" parseErr=<nil> | detector used=false value=""   AGREEMENT=false
PROBE pflag args=[-e --token zz-secret] -> token="" parseErr=<nil> | detector used=true value="zz-secret"                        AGREEMENT=false
```

- `-ut v`: pflag **does** set the token (`u` is the boolean `--ignore-unsafe-cert`, `t` then takes the
  next argument), the detector says "not used" → **neither warning fires** for a token that really is on
  the command line. This is a false negative in a security warning.
- `-e --token x`: the opposite — a warning fires although pflag took `--token` as `-e`'s value. Because
  the warning prints only a character count, nothing is leaked; the cost is a misleading line.

**Judgement.** Acceptable to ship as documented: the feature is a warning, the flag keeps working, the
detector covers every spelling the panel, the installers and the docs emit, and a shared parser would
be a second pflag — exactly what the comment argues against. If the team wants the `-ut v` gap closed
cheaply, the minimal version is: in a single-dash argument whose first shorthand is a known *boolean*
(`u`), keep scanning the rest of the cluster for `t` and, when found, report "used" with an empty value
so the generic warning still fires (over-warning is harmless: no value is ever printed).

### 4.5 Container marker three-way consistency — [executed + read-only]

- `agent/update/update.go:40` `containerMarkerPath = "/.komari-agent-container"`, stat'ed by
  `isContainerAgent()` (`:155-158`), and `checkAndUpdate` returns early when it exists (`:260-264`)
  **[read-only]**.
- `Dockerfile.agent:30` `RUN touch /.komari-agent-container` **[executed: present, and break test 4
  shows removing it fails the checker]**.
- `.github/workflows/docker.yml:77-84` matrix agent row `dockerfile: Dockerfile.agent`, used by both
  the smoke test (`-f ctx/${{ matrix.dockerfile }}`) and `Build and push` (`file:`) **[read-only]**.

So a hand-`docker run` probe built from this workflow gets the marker and will not replace itself and
`exit(42)`. `agent/Dockerfile` also has the line, but it is not the published image — the checker
reads the matrix row rather than naming a file, which is the right shape.

### 4.6 `GITHUB_TOKEN` gate on the update probe — [executed]

```
$ go test ./update/ -run TestProbeRealTargetedUpdateSelection -v      # no token
    integration_probe_test.go:38: GITHUB_TOKEN is not set: … Set GITHUB_TOKEN to run it (CI does: .github/workflows/ci.yml, the "Test agent" step).
--- SKIP: TestProbeRealTargetedUpdateSelection (0.00s)

$ GITHUB_TOKEN=dummy-token-for-verification go test ./update/ -run … -v
    integration_probe_test.go:47: list: GitHub releases API returned status 401: {
--- FAIL: TestProbeRealTargetedUpdateSelection (0.44s)
```
The second run is the point: with any token the case **runs** and actually reaches api.github.com (401
for my dummy), so the credential does reach the request and the skip is not a permanent hiding place.
The offline companion test (`TestProbeCredentialsPresentMatchesTheRequestThatIsActuallySent`) pins the
gate and the `Authorization` header to each other in both directions.

### 4.7 `latest` policy text vs YAML — [executed]

`docker.yml:224` and `docs/RELEASING.md`/`docker.yml:128-145` agree with the truth table: version tag
always (when the job runs), `latest` only for a non-prerelease tag with `release_verified == 'true'`
(`== 'true'` is load-bearing because `"false"` is a non-empty string); `release published` and
`workflow_dispatch` have no conclusion and keep the old behaviour. Verified by break test 3 plus the
table in §4.1.

### 4.8 ❌ `docs/RELEASING.md` contradicts the workflow on the cancelled case — [executed]

New text in `docs/RELEASING.md`:

> `docker.yml` still runs when the release workflow is *cancelled*, which is deliberate: a cancelled
> run is not evidence about the image, and that behaviour is documented at the top of the file.

The opposite is true. `docker.yml:63` is
`if: github.event_name != 'workflow_run' || github.event.workflow_run.conclusion != 'cancelled'` — the
**job** is skipped when the release run was cancelled; the file's own comment (`:57-62`) says so
("只在 release 工作流被【取消】时跳过"), `docs/RELEASING.md`'s own next paragraph says the gate is
`conclusion == 'success'`, and the checker's table prints it:

```
workflow_run cancelled           SKIPPED   -                 not pushed   not pushed
```

**Minimal fix.** Replace the sentence with what the code does, e.g. "A release run that was
*cancelled* skips the build entirely (the job-level `if`), so neither the version tag nor `latest` is
pushed; only a completed-but-failed run still pushes the version tag while holding `latest` back."

---

## 5. T9 — the frontend side of the sensitive gate

**What the fix does.** `frontend/src/lib/oauth2Binding.ts` owns the two URLs and appends
`?2fa_code=` only when a code exists; `account.tsx` reads `ssoNeedsCode = Boolean(account?.["2fa_enabled"])`
(`:43` — the same flag the existing 2FA buttons use at `:101/:304/:331` **[executed grep]**), collects
the code, refuses an empty one before any request, and probes `/bind` with `redirect:"manual"` so a
refusal is shown instead of leaving the operator on raw JSON.

### RV-7 [executed] — both halves

```
### builder appends 2fa_code unconditionally
not ok 1 - an account without a factor gets exactly the requests that worked before the gate
# pass 3 / # fail 1

### page loses the empty-code guard (`ssoNeedsCode && !ssoCode` → false)
not ok 4 - the page uses those builders and keeps the code behind the 2fa gate
# pass 3 / # fail 1

### restored
# pass 4 / # fail 0
```

### No-2FA requests are the same requests — [executed + read-only]

```
$ git show HEAD:frontend/src/pages/admin/account.tsx | Select-String 'oauth2/unbind|oauth2/bind'
        const response = await fetch("/api/admin/oauth2/unbind", {
          method: "POST",
        window.location.href = "/api/admin/oauth2/bind";
```
Current no-2FA path: `oauth2UnbindUrl("")` → `"/api/admin/oauth2/unbind"` with
`{ method: "POST", credentials: "same-origin" }`; `bindSso("")` takes the early branch
`window.location.href = oauth2BindUrl("")` → `"/api/admin/oauth2/bind"`. Method and URL are identical;
the only textual addition is an explicit `credentials: "same-origin"`, which is `fetch`'s default for
same-origin requests, so the wire request is unchanged. The empty-code guard and the dialog are behind
`ssoNeedsCode`, so a no-factor account sees no prompt and sends no probe.

### The browser spec runs and passes locally — [executed]

```
$ python script/account-sso-2fa.browser.spec.py
Ran 5 tests in 5.290s
OK
```
This is not in the task's local gate list (CI-only); Playwright and Chromium were already installed
here, so I ran it rather than leaving T9 at the unit level.

### Double `/bind` — see §2 residual risk 2 (measured 2 requests, idempotent server side).

---

## 6. P1-13 — container marker (covered in §4.5)

Three-way consistency holds, and break tests 4 and 5 show the checker actually fails when any leg of it
is broken (Dockerfile loses the line; the Go constant drifts).

---

## 7. Was any test weakened? (the task's item 3)

I scanned the whole diff for removed assertion-like lines:

```
$ git diff HEAD | Select-String '^-' | Select-String 'assert|t\.Fatal|t\.Error|expect\(|want|panic|require\.|throw|Skip'
-			want: -1, wantOK: false,
-	if err != nil { t.Fatal(err) }
-	if err != nil { t.Fatalf("list: %v", err) }
-          toast.error(t("account_settings.unbind_sso_failed", { 
-      toast.error(t("account_settings.sso_auth_failed"));
```

- `want: -1, wantOK: false` → the P1-6 expectation, analysed in §3: the *meaning* was inverted with a
  stated reason, and the panel-side rule that justifies it is external to the implementation.
- The two Go lines are reformatted, not removed (`integration_probe_test.go` now has a multi-line
  `t.Fatal(err)` and a richer `list: %v (an HTTP 403 'rate limit exceeded' here means…)`).
- The two frontend lines moved into `unbindSso`/`bindSso`, where the refusal now carries the server's
  message; the browser spec asserts the new text (`Unbinding failed: Invalid 2FA code`).

No test lost a tooth. New tests were added for every fix (see the RV sections), plus
`frontend/script/browser-spec-registry.test.mjs`, which is itself a guard against "added but never
run" specs — and, ironically, §4.2 is the same failure mode for the Python checker.

---

## 8. Gate run (task item 4) — all [executed]

| Command | Result |
|---|---|
| `go vet ./...` (root) | exit 0 |
| `go test ./... -count=1 -skip '^TestIpInfo$\|^TestIpApi$\|^TestGeojs$'` | exit 0, every package `ok` |
| `go test -race ./database/accounts ./web/api/public -count=10 -shuffle=on` | exit 0 |
| `go vet ./...` (`agent/`) | exit 0 |
| `go test ./... -count=1 -skip 'TestICMPPing\|TestTCPPing\|TestHTTPPing'` (`agent/`) | exit 0, every package `ok` |
| `npx tsc -b` (`frontend/`) | exit 0 |
| `npm test` (`frontend/`) | 119 tests, 119 pass, 0 fail |
| `python deploy/docker-workflow-check.py` | exit 0 |
| `python frontend/script/account-sso-2fa.browser.spec.py` | 5 tests OK (not part of the requested list) |

**Flakiness:** none observed. The root and agent suites were run twice (before and after my temporary
reversions) with identical results; the race command runs 10 shuffled iterations. Two skips are
by design and visible on every run: `agent/update`'s GitHub probe without `GITHUB_TOKEN` (§4.6) and
`agent/server`'s real-network/root/symlink cases (`TestProbeAutoProtocolLive`, `TestICMPCapabilityIsNotNoneOnLinux`,
`TestICMPWithoutCapabilitiesLive`, `TestListFilesResolvesSymlinkTargetKind`, three `TestRunTaskCommand*Unix`).
One thing to be aware of when reading a local run: `go test ./agent/server/` **without** the CI `-skip`
flags fails `TestICMPPing`, `TestTCPPing`, `TestHTTPPing` — that is the documented, pre-existing
exclusion, not a regression from this batch.

---

## 9. Hygiene (task item 5) — [executed]

- `git status` at the end is **identical** to the snapshot taken before the review: the same 29 modified
  files, the same 22 untracked entries, plus this report. Nothing I did left a trace.
- Temporary artefacts I created (`web/rpc/jsonrpc/zz_verify_probe_test.go`,
  `web/api/public/zz_verify_limiter_test.go`, `agent/cmd/zz_verify_unchased_test.go`) were deleted; all
  reversions were restored from `$TEMP` backups and re-verified by re-running the affected packages.
- No `zz_temp_repro*`-style files in the diff or status.
- Secret scan: every untracked file and every added line, against JWT/private-key/`Bearer`/password/
  API-key patterns → the only hits are deliberate test placeholders
  (`test-only-token-unlock-visible`, `s3cr3t-value-…-never-printed`, `pflag-table-token-value`). No real
  credential.
- New files all belong: the browser fixture (`.html`/`.tsx`), the two `script/*.test.mjs`, the four Go
  test files, `deploy/docker-workflow-check.py`, the audit notes. The `docs/audits/2026-09-30/` scripts
  (`live-evidence.py`, `sao-repair-repro.cjs`, `metric_repro_test.go.txt`, `rpc_repro_test.go.txt`) are
  audit evidence; keeping repro tests as `.txt` is the right call because they are not compiled.
- Ignored-but-present: `.accuracy-smoke/` (now covered by the new `.gitignore` rule) — the working tree
  is not clean of it, but it is correctly ignored. `frontend/.build-tmp/` is absent at the end of my run
  and covered by `frontend/.gitignore`.

---

## 10. Findings, ordered by what I would fix first

1. **❌ Anonymous hidden-node oracle / UUID enumerator on `common:*` and `public:getPublicPingTasks`**
   (§1). Executed counterexample. Pre-existing, but it means the P1-5 class is only half closed.
   *Fix:* one shared visibility helper for `getNodeRecentStatus`, `getRecords` (both the uuid gate and
   the `clients` echo), `getPublicPingTasks` and `getPingRecords`.
2. **❌ `deploy/docker-workflow-check.py` is not wired into CI** (§4.2). The contract it pins has no
   automated enforcement. *Fix:* one `python -m pip install pyyaml && python deploy/docker-workflow-check.py`
   step in the `deploy-scripts` job.
3. **❌ `docs/RELEASING.md` is wrong about the cancelled case** (§4.8) and contradicts the workflow and
   the checker. *Fix:* one sentence.
4. **⚠ `commandLineToken` misses `-ut v`** (§4.4): a real token on the command line produces no warning.
   Documented, low impact, cheap to close.
5. **⚠ TOTP in a GET query string** (§2 residual 1): a one-shot code in access logs/history; inherent
   to the redirect flow, worth one line in `docs/SECRETS.md`.
6. **⚠ `VerifySensitive2FACore`'s API-key exemption has no test** (§2).
7. **nit** the checker exits 1 via a Python traceback when the workflow is valid YAML without `jobs`
   (§4.1) — the exit code is right, the message is not.
8. **nit** `docs/IP-INFO-API.md` (pre-existing text, untouched here) says the theme reads node addresses
   from `common:getNodes`; the registered method is `public:getNodesInformation`. The new paragraph is
   about `/lookup`, while `/latency` still echoes `uuid` without resolving it **[read-only]** — accurate,
   but one clause would prevent a misread.

---

## 11. What I could not execute

- History claims (`v1.6.6` = `13 passed / 2 failed`, "a docs-only PR turned red") — not verifiable from
  the workspace.
- The workflow's own end-to-end run: I only ran its `Resolve tag` script through the checker, in bash,
  with substituted env. No docker, no registry, no GitHub Actions semantics beyond what the checker
  models.
- `DependencyError → exit 2` for a missing PyYAML/bash: the code path is explicit
  (`docker-workflow-check.py:67-91, 475-480`) **[read-only]**; both dependencies are installed here, so I
  did not exercise it.
- Real panel + real OIDC provider round trip: the OAuth tests use the in-repo fake generic upstream.
