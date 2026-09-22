# OAuth integration, admission, and numeric identity migration

Priority 2 of `docs/NEXT-REVIEW-v0.1.19.md`. Production OAuth is disabled, so a
deployment cannot prove the login paths. This document separates what the tests in
this tree prove on an offline `httptest` provider from what still needs a real
GitHub/QQ/aggregator instance, and it records the numeric-identity problem that is
deliberately *not* fixed here.

Read it the way `docs/AUTH-HARDENING.md` asks to be read: sections 1 and 2 are
shipped and tested, section 4's migration is a proposal that is not in the code.

## Status

| Item | State |
|---|---|
| Per-IP admission on OAuth start | **Implemented** — `web/api/public/oauth.go` (`admitIP`), section 1 |
| Global pending-state cap, 5-minute expiry, atomic consume | **Unchanged** — `web/oauth/internal/oauthutil/state.go` |
| QQ callback-query preservation | **Verified against a fake aggregator** — `TestOAuthQQCallbackQueryPreservation` |
| Provider changed mid-login | **Verified** — `TestOAuthProviderReloadInvalidatesPendingLogin` |
| Back-button replay of a consumed callback | **Verified** — `TestOAuthCallbackReplayIsRejected` |
| Exact state equality, disabled-OAuth rejection | **Verified per provider** — `TestOAuthCallbackRequiresExactStateForEveryProvider`, `TestOAuthCallbackRejectsDisabledAndMismatchedState` |
| Numeric identity representation | **Unchanged, deliberately.** The migration below is a proposal |
| Real provider end-to-end login | **Not verified** — section 3 |

## 1. Per-IP admission on OAuth start

### Policy

| Scope | Rule |
|---|---|
| per source IP | token bucket: burst 10, refill 1 per 30 s |
| response | `429` with `Retry-After` (the login limiter's convention) |
| map | bounded at 4096 entries; entries unseen for 30 min are swept every 10 min; a full map evicts the least recently seen entry |
| global state cap | untouched: 4096 pending states, 5-minute TTL, consumed atomically |

### Why

`oauthutil.States` caps *all* pending logins at 4096, which bounds memory but not
fairness. One caller looping `/api/oauth` fills every slot; every other start then
gets the store's `503` until the 5-minute TTL drains. The per-IP bucket puts a
ceiling in front of the shared cap that no single caller can reach.

With these numbers one IP holds at most 10 pending slots and can create about 20
states in a 5-minute window (10 immediately, 10 more from the refill), so filling
all 4096 slots takes on the order of 200 distinct sources instead of one.

### Reuse, and where it differs from login throttling

The bucket is the login limiter's machinery, not a second rate-limiting idiom:
`loginBucket.refill`/`retryAfter`, `loginLimiterMaxEntries`, the TTL sweep, and
eviction of the least recently seen entry when the map is full. Only the question
differs:

- login throttling is a **penalty recorded on failure**, so it splits `Allow`
  (peek, never create) from `RecordFailure` (consume);
- admission control must **charge every attempt**, so `admitIP` consumes as it
  admits. `peekLocked` still never creates a bucket, so a refused caller cannot
  grow the map, and `bucketLocked` still evicts rather than failing closed — the
  failure that once turned a bounded login map into a permanent lockout.

`defaultOAuthStartLimiter` is a separate instance from `defaultLoginLimiter`: a
password flood must not block logins through a provider, and a provider-login
flood must not throttle password logins.

### What it deliberately does not do

- It does not stop a *distributed* flood from filling the global cap. That is what
  the cap is for; admission only removes the single-caller shortcut.
- It does not refund a token when the state store is full or the aggregator call
  fails. The attempt was made and the caller is the one being bounded; the login
  limiter refunds nothing either.
- The key is `c.ClientIP()`, which follows the router's trusted-proxy setting
  exactly as login throttling does. A router configured to trust `X-Forwarded-For`
  from anywhere lets a caller pick its own key; that is a proxy-configuration
  property shared with `login_limiter.go`, not something this bucket can fix.

### Tests

`web/api/public/oauth_test.go`:

- `TestOAuthStartAdmissionRejectsOneIPButNotAnother` — at the endpoint: burst,
  then `429` with a `Retry-After` of 1..30 s, while a second source still starts.
- `TestOAuthStartAdmissionIsABucketNotALock` — expiry: the refused caller is
  admitted again after one refill interval.
- `TestOAuthStartAdmissionFairBetweenTwoIPs` — one source's spent budget does not
  move another's.
- `TestOAuthStartAdmissionStaysBounded` — 4196 distinct sources leave the map at
  its bound and a fresh source is still admitted.
- `TestOAuthStartAdmissionDoesNotGrowOnRefusal` — 1000 refusals add no entries.
- `TestOAuthStartAdmissionExpiresIdleEntries` — an entry for a source that stops
  calling is swept on the login limiter's schedule.
- `TestOAuthStartStillHonoursGlobalStateCap` — a source that never repeats is
  admitted until the store itself is full, and that refusal is the store's `503`,
  not the limiter's `429`.

## 2. Integration paths production cannot prove

All of these run offline against `httptest` upstreams in
`web/api/public/oauth_test.go`; no real provider is contacted.

### QQ callback-query preservation

`TestOAuthQQCallbackQueryPreservation`. The QQ provider embeds the state in the
callback URL it hands the aggregator (that part is pinned by
`web/oauth/qq/qq_test.go`). The new test drives the whole flow against a fake
aggregator: start a login, check that the callback URL the aggregator received
carries the state the panel put in its cookie, then return to
`/api/oauth_callback` with that query **preserved** and assert a session is
created. It then repeats with the query **dropped** and **mangled** and asserts
`400 Invalid state` and that the aggregator's callback endpoint was never reached.

What it proves: the panel's requirement, and that a non-preserving aggregator
fails closed. What it does not prove: that any particular aggregator preserves the
query. An operator must confirm that on their own instance by starting one login
and watching for a return to `/api/oauth_callback` that still carries `state`.

### Provider changed mid-login

`TestOAuthProviderReloadInvalidatesPendingLogin`. A login is started, then the
provider is reloaded — once with a different configuration under the same name,
once as a different provider — and the pending state is replayed. Both are
refused, and neither upstream is contacted.

What it proves: pending states belong to the provider instance that issued them
(`oauth.LoadProvider` publishes a fresh instance and destroys the old one, which
clears its state cache), so a reload cannot resolve a login against a provider
that never issued it. What it does not prove: anything about how a real provider
handles a user who is halfway through authorization when the panel's settings
change. The operator-visible consequence is that the pending login fails and the
user restarts it; the failure is a generic `500` carrying `invalid state`, not a
session.

### Back-button replay

`TestOAuthCallbackReplayIsRejected`. A callback completes and creates a session;
the same request is then re-sent with the same cookie. It is refused, and the
provider's identity endpoint was called exactly once.

What it proves: the state is consumed by the first callback, so a replay cannot
mint a second session for one authorization. It is the HTTP-level counterpart of
the concurrency test in `web/oauth/internal/oauthutil/state_test.go`.

### Everything already hardened, still hardened

`TestOAuthCallbackRequiresExactStateForEveryProvider` starts a login against
generic, QQ and GitHub and replays five variants of the state (appended,
truncated, prefixed, empty, unrelated). All are `400` before any provider is
contacted, and disabled-OAuth rejection plus the mismatched-state cases stay
covered by the pre-existing `TestOAuthCallbackRejectsDisabledAndMismatchedState`.
Upstream bounds and bounded errors are pinned by
`web/oauth/internal/oauthutil/http_test.go` and the provider tests.

## 3. Still unverified, and what would verify it

These need a configured instance and real accounts; no local test can stand in.

- **Real GitHub**: authorize redirect, scope grant, token exchange, `/user`
  response shape, organisation/2FA restrictions, and whether GitHub's `id` is
  within `int64` (it is decoded into an `int`, so an out-of-range id is refused
  rather than rounded — `TestGitHubIdentityIsExactOrRefused`).
- **Real QQ aggregator**: whether the configured aggregator preserves the callback
  query. `TestOAuthQQCallbackQueryPreservation` proves the panel's side only.
- **Redirect-URI allowlisting** at each provider, including the HTTPS cookie
  attributes behind a TLS-terminating proxy.
- **Concurrent logins from one browser profile** (two tabs), and a login started
  before a panel restart.
- **Whether any real provider issues a numeric id above 2^53.** The failure below
  is a property of the code path, not a claim that a specific provider reaches it.

## 4. Numeric identity: current representation and migration

**Nothing in this section is implemented.** It is the policy the v0.1.19 finding
asks for.

### Current representation (verified)

`web/oauth/generic/generic.go` (`OnCallback`) reads the configured
`user_id_field`. A JSON string is kept verbatim. A JSON number is decoded into a
`float64` and formatted with `fmt.Sprint`, and `web/api/public/oauth.go` stores
`<provider name>_<id>` in `users.sso_id`. The stored value is therefore Go's
shortest float representation, not the digits the provider sent:

| Provider JSON | Stored id suffix | Test |
|---|---|---|
| `42` | `42` | `TestGenericNumericIdentityFormattingIsPinned` |
| `1234567` | `1.234567e+06` | same (and the pre-existing `legacy numeric binding` case) |
| `1234567890123` | `1.234567890123e+12` | same |
| `9007199254740992` | `9.007199254740992e+15` | same |
| `9007199254740993` | `9.007199254740992e+15` | same |
| `"9007199254740993"` | `9007199254740993` | same |

Only the **generic** provider with a numeric `user_id_field` is affected. GitHub
decodes its id into an `int` and formats it with `%d`
(`TestGitHubIdentityIsExactOrRefused`), and QQ's `social_uid` is a string.

### Why it exists

The float branch was written so a provider that returns a bare JSON number works
at all. Bindings created through it hold the float form, and `GetUserBySSO` looks
the stored string up exactly. Any change to the formatting — including changing it
to print plain digits — detaches every existing numeric binding. This is why
`docs/REVIEW-2026-09-22.md` records that numeric ids "deliberately retain the old
float-to-string representation".

### The failure

1. **Format drift.** `1.234567e+06` is what is stored today; `1234567` is what a
   corrected formatter would store. The two never compare equal, so the affected
   user is told "please log in and bind your external account first."
2. **Precision collapse (verified).** A number above 2^53 is already rounded by
   `encoding/json` before the panel sees it: `9007199254740992` and
   `9007199254740993` produce the *same* stored id
   (`TestGenericNumericIdentityCollidesAboveFloat64Precision`). `users.sso_id` has
   no unique constraint, and `GetUserBySSO` resolves a duplicate with `First()`,
   so the collision does not fail loudly — it silently resolves to whichever row
   the database returns first. Two distinct provider accounts would share one
   panel account.
3. **Unreadable ids.** Any id at or above 1e6 is stored in exponent form, so what
   an operator sees in the provider's UI is not what the panel stored. That is
   friction for support and for any manual rebind.

Whether any provider actually issues ids that large is **not** established here;
snowflake-style id spaces make it plausible, but that is an argument, not
evidence.

### Interim measure: prefer string ids in new configuration (verified)

Configure `user_id_field` to point at a field the provider returns as a JSON
string. String ids are preserved byte-for-byte (test row above), need no
migration, and are exact for any magnitude. This is the recommended measure for
every new generic provider and for any existing one whose provider can be
configured to emit a string. Providers that only emit numbers have no such
option, and that is the case the migration exists for.

### Migration and rebind procedure (proposal)

0. **Target format.** Keep the literal JSON token: decode into `json.Number` (or
   the raw bytes) and store the digits exactly as sent. Do not decode into
   `float64`, and do not reformat.
1. **Detect affected bindings.** Candidates are rows whose stored value looks like
   a number. On the panel's SQLite database:

   ```sql
   -- candidates, not a decision list
   SELECT uuid, username, sso_id FROM users WHERE sso_id GLOB 'generic_[0-9]*';

   -- duplicates that already collapse two accounts onto one id
   SELECT sso_id, COUNT(*) FROM users GROUP BY sso_id HAVING COUNT(*) > 1;
   ```

   The candidate list is ambiguous by construction: a provider configured with a
   string id that begins with a digit appears here too, and for that row the
   migration is a no-op. A row is only *known* to be affected by comparing the
   stored value with the id the provider shows for that account, or by letting the
   dual-read window below migrate it on the next login. There is no panel report
   across accounts today — `getMe` returns the current user's `sso_id`
   (`web/rpc/jsonrpc/public.go`) — so the SQL above is the operator's tool and it
   needs a SQLite client (`sqlite3 <db> "…"`).
2. **Dual-read and rewrite on login.** Accept both the legacy float form and the
   exact form, and rewrite the stored value on a successful login with a
   compare-and-swap (`WHERE uuid = ? AND sso_id = ?`). This mirrors the password
   migration in `database/accounts/accounts.go` (`CheckPassword`), including the
   CAS, so a concurrent rebind is not clobbered. The legacy reader must stay until
   no candidate rows remain.
3. **Wait, then remove.** After the candidate count reaches zero (or the operator
   accepts the remaining rows), delete the float branch. Removing it first is what
   locks users out.
4. **Rebind.** For users whose provider id genuinely changed (provider switched,
   id space changed, or a collision from step 1's duplicate query), an operator
   with a session rebinds through the existing flow: `GET /oauth2/bind` sets
   `binding_external_account`, then the provider round trip. `users.sso_id` can
   also be corrected directly. Rebinding requires the user to still have a
   session or the operator to act on their behalf.

**What happens to a user mid-migration.** With the dual-read window in place,
nothing: their next successful login rewrites the row and their session continues,
and a user who does not log in is untouched. Without it — that is, if the format
changes alone — every affected user gets "please log in and bind your external
account first." and needs an operator-assisted rebind. That difference is the
reason for the rule below.

### Rollback

- Back up the `users` table (at minimum `uuid` and `sso_id`) before step 3.
- Reverting the binary after step 2 is safe. Rows are rewritten only on a
  successful login, so a user who has not logged in still holds the legacy value,
  which the older binary reads.
- Reverting after step 3 strands any user already rewritten to the exact form: the
  old binary cannot read it. Either restore the backup, or keep the dual-read code
  in the rollback binary. This is the same shape as the Argon2id rollback note in
  `docs/AUTH-HARDENING.md`, and it is why step 3 waits.

### The rule

**The stored `sso_id` format must not change without the dual-read and rebind
migration above.** A change to `fmt.Sprint(number)` — including "fixing" it to
print plain digits — is a breaking change to stored data, not a bug fix. Any
change to it must ship with: the detection query, the dual-read window, the
compare-and-swap rewrite, a `users` backup step, and this document updated from
proposal to implemented.

## 5. How to verify the claims here

```
go test ./web/api/public/... ./web/oauth/... -count=1 -race
go build ./...
go vet ./web/oauth/... ./web/api/public/...
```

Section 1 and the provider-level parts of section 2 are exercised by the named
tests. Section 3 has no test by construction. Section 4 is a proposal: the
*representation* and the *collision* are pinned by
`TestGenericNumericIdentityFormattingIsPinned`,
`TestGenericNumericIdentityCollidesAboveFloat64Precision` and
`TestGitHubIdentityIsExactOrRefused`; the migration procedure itself is not in the
code and no test can exercise it until it is.
