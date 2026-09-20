# Authentication hardening

What is **implemented**, what is **not**, and how to check either claim. Sections 1
and 2 describe shipped behavior and cite the file and the test that prove it.
Section 3 holds the proposals; nothing in it is in the code.

Read this document with the rule in section 4 in mind. An earlier revision mixed a
design proposal into a section headed "implemented", and the mismatch it hid was a
real defect (H1, below).

## Status

| Change | State |
|---|---|
| Argon2id password hashing with random salts | **Implemented in v0.1.13** — `database/accounts/password.go` |
| Legacy SHA-256 verification and login-time migration | **Implemented in v0.1.13** — `database/accounts/accounts.go` (`CheckPassword`) |
| Bounded KDF admission | **Implemented in v0.1.13**, corrected after v0.1.13 — `database/accounts/password.go` |
| Login throttling (per IP and per account) | **Implemented in v0.1.12** — `web/api/public/login_limiter.go` |
| Login lockout fix (H1) | **Implemented after v0.1.13** — commit `b703f82`, unreleased |
| 2FA re-enrollment from the panel | **Implemented** — `web/api/admin/2fa.go` (`Rebind2FA`) |
| Encrypting 2FA secrets or session tokens at rest | **Not implemented** — section 3 |

## 1. Password hashing

### Shipped format

New and changed passwords use Argon2id with a random per-password salt:

| Property | Value |
|---|---|
| Stored shape | `$argon2id$v=19$m=19456,t=2,p=1$<b64-salt>$<b64-hash>` |
| Parameters | `m=19456 KiB`, `t=2`, `p=1` |
| Salt / tag | 16 random bytes / 32 bytes, `base64.RawStdEncoding` |
| Legacy shape | 44-character standard-base64 `sha256(password + "06Wm4Jv1Hkxx")`, no `$` |

The verifier accepts **only the exact shipped parameter set**: it matches the
literal prefix above before decoding. Raising the parameters later therefore
requires teaching the reader the older prefix first, or every migrated account is
locked out.

The low-memory profile is deliberate for small monitoring servers. It is not a
claim about production login latency; no production benchmark was taken.

### Migration

Legacy hashes still verify, and a successful login re-hashes in place with a
compare-and-swap (`WHERE uuid = ? AND passwd = ?`) so it cannot overwrite a
concurrent password reset. The legacy branch stays indefinitely: removing it would
lock out any account that has not logged in since the upgrade.

`ForceResetPassword` (`cmd/chpasswd.go`, the documented recovery path),
`CreateAccountWithDB` (the install wizard) and `UpdateUser` all write the new
format. `ForceResetPassword` and a password change through `UpdateUser` invalidate
sessions.

### Admission

At most two Argon2id evaluations run at once; each holds 19456 KiB. Admission is a
bounded wait, not a refusal:

- a check waits up to 5 seconds for a slot;
- if the wait expires, it returns `ErrPasswordBusy`, the login endpoint answers
  `503` with `Retry-After`, and **no login attempt is consumed**;
- passwords over 4096 bytes are rejected.

The first implementation refused the third concurrent check outright, and the
refusal came back through the same path as a failed password: with two evaluations
already running, every further caller — the administrator with the correct password
included — was told `Invalid credentials`. A saturated pool is now reported as
saturation.

### Rollback

Releases before v0.1.13 cannot verify migrated Argon2id hashes. Keep a pre-upgrade
database backup, or use the older binary's password reset command after rollback.
Do not restore a database backup over newer monitoring data without accounting for
that loss.

### Tests

`database/accounts/password_test.go`:

- random salts: two hashes of one password differ, and both carry the shipped prefix
- legacy hash verifies and reports the legacy format
- wrong password and malformed stored values fail
- oversized passwords are rejected
- a saturated pool reports `ErrPasswordBusy` for both hashing and verification, and
  admits the next check once a slot frees

`web/router/hardening_test.go` (`TestSecurityAndResourceRegressions`) inserts a
legacy hash exactly as an installation would store it, verifies it with
`accounts.CheckPassword`, and asserts the stored value was rewritten with the
Argon2id prefix.

## 2. Login throttling

Shipped as `web/api/public/login_limiter.go`, wired into `Login` before
`CheckPassword` and before `Verify2Fa` (`web/api/public/login.go`).

### Shipped policy

| Scope | Rule |
|---|---|
| per IP | token bucket: burst 10, refill 1 per 90 s |
| per account | token bucket: burst 5, refill 1 per 300 s |
| response | `429` with `Retry-After` |
| 2FA | the same buckets; a failed 2FA step consumes from both |
| success | clears the account bucket; the IP bucket is deliberately left alone |

Keys are the source IP and the lowercased username, so an unknown username is
throttled exactly like a real one and the response cannot probe which accounts
exist. It is a bucket, not a lock: it refills, so a legitimate user recovers
without an operator.

Two properties make "a bucket, not a lock" true, and both were wrong in the first
version (H1, fixed in `b703f82`):

1. `Allow` must **not create** buckets. It originally looked both keys up through
   the creating accessor, so a request it was about to deny still inserted an
   account bucket. The account map is bounded, so ~4096 requests carrying distinct
   usernames filled it.
2. A full map must **evict**, not fail closed. The full-map branch handed out a
   zero-token bucket that was never stored and so never refilled. Together with
   (1) that produced a hard lockout: every account not already in the map — the
   admin's included — got `429` with a full `Retry-After` for as long as the
   attacker kept the map topped up. Recovery was a process restart.

The map is bounded at 4096 entries per dimension, entries unseen for 30 minutes are
swept every 10 minutes, and a full map evicts the least recently seen entry.

### 2FA re-enrollment step-up

`POST /api/admin/2fa/rebind` asks the account password so an account that lost its
authenticator can enroll a new one. It checks that password against the **same
buckets** as login, through `AllowSensitivePasswordCheck` /
`RecordSensitivePasswordFailure` in `login_limiter.go`: both endpoints ask the same
question for the same account, so a stolen session must not turn re-enrollment into
a password oracle the login limiter cannot see.

What this gives up, deliberately: a password is a weaker proof than the factor it
replaces, so an attacker holding **both** a live session and the account password
can attach their own authenticator and keep the account. The alternative is what
this fork had before — an account nobody can recover without shell access — and
that is worse for a single-admin panel. The mitigations in place:

- the password is checked against the login buckets, so guessing here costs the same
  as guessing at the login form;
- the existing factor keeps working until a code from the new one verifies, and the
  stored secret is only overwritten if it is still the one the enrollment started
  against, so a failed or raced attempt changes nothing;
- a completed replacement revokes every other session of that account and writes an
  audit entry at `warn`;
- the enrollment token is bound to one account, expires after 10 minutes, and
  accepts five codes.

The endpoint derives the account from the session, so an account whose session has
also expired still needs the `disable2FA` CLI command on the host.

### Interaction

- `DisablePasswordLogin` short-circuits before the limiter; skip throttling there.
- OAuth/OIDC logins do not use this path.
- A saturated KDF (`503`) does not consume an attempt.

### Tests

`web/api/public/login_limiter_test.go` pins the account burst and its refill, the IP
bucket across many usernames, the account bucket across many IPs, the reset on
success, the bounded maps, the TTL sweep, the H1 lockout itself (a full account map
must not deny an account absent from it), and that the re-enrollment step-up shares
the login buckets.

`web/api/admin/2fa_test.go` (`TestRebindReplacesFactorOnlyAfterPasswordAndCode`)
covers the replacement itself: a wrong password changes nothing, a correct one
issues a replacement enrollment, the stored factor keeps working until the new code
verifies, and completing it replaces the secret and revokes the other sessions while
keeping the caller's. Route registration and the route's exemption from
`RequireSensitive2FA` are pinned by `web/router/hardening_test.go`
(`RebindRouteIsRegisteredAndNeedsNoFactorCode`).

## 3. Not implemented

These are proposals. None of them is in the code, and a later session should not
treat this section as a description of behavior.

- **2FA secrets at rest.** `users.two_factor` is stored as the base32 secret.
  Encrypting it needs a key-management story for a single-binary deployment.
- **Session tokens at rest.** `sessions.session` is stored in plaintext.
- **A `migrate-hash` report.** An optional command reporting how many accounts
  still hold a legacy hash, so "we think we migrated" is measurable.
- **Higher Argon2id parameters.** `m=64 MiB, t=3, p=4` was the original suggestion.
  It was not adopted: the panel is expected to run on small monitoring servers, and
  the stored prefix pins the parameter set, so raising it needs an older-parameter
  reader first.

## 4. How to verify a claim here

Every "implemented" row above names a file, and every behavior it describes has a
test. When changing auth code, keep that property:

- a design that is not in the code belongs in section 3, marked as a proposal;
- an "implemented" claim that no test exercises is a claim, not a fact.

The reason is H1. The previous revision of this file described the throttling policy
in proposal form under an "implemented" heading, and the code did not match it: the
proposal said "a bucket, not a lock", and the implementation could lock the only
admin out permanently. The document's own claim is what stopped anyone from
re-deriving the behavior from the code. The defect was found by reading the
implementation, not the plan.
