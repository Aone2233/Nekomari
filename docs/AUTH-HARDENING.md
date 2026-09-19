# Authentication hardening — migration plan

Two changes were deliberately left out of v0.1.11 because both are migrations
rather than patches: the password hash, and login throttling. This is the plan so
a later session does not have to re-derive it.

Neither is a one-line fix, and neither can be validated by the current test suite
until its own tests are added.

## 1. Password hashing

### Current state

`database/accounts/accounts.go`:

```go
const constantSalt = "06Wm4Jv1Hkxx"

func hashPasswd(passwd string) string {
	saltedPassword := passwd + constantSalt
	hash := sha256.New()
	hash.Write([]byte(saltedPassword))
	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}
```

- single-round SHA-256, no key stretching
- **one constant salt shared by every account**, and it is in the source tree
- `users.passwd` stores the resulting 44-character base64 string

Consequence: a database leak is offline-crackable at GPU speed, and two accounts
with the same password have byte-identical hashes. The constant salt is public, so
the legacy hashes are effectively unsalted.

### Target

A memory-hard KDF with a per-password random salt. `golang.org/x/crypto` already
provides both options, so no new dependency is needed:

- **argon2id** (`x/crypto/argon2`) — recommended, memory-hard
- **bcrypt** (`x/crypto/bcrypt`) — lower-effort alternative, salt built in

Store a self-describing string so verification can tell the formats apart:

| Format | Stored shape |
|---|---|
| argon2id | `$argon2id$v=19$m=65536,t=3,p=4$<b64-salt>$<b64-hash>` |
| bcrypt | `$2a$12$...` (bcrypt is already self-describing) |
| legacy | 44-char base64, no `$` prefix |

Suggested argon2id parameters: `m=64 MiB, t=3, p=4`. Tune on the panel host and
record the result; the parameters live inside the stored string, so they can be
raised later without breaking old hashes.

### Migration steps

There is no way to re-hash a password without its plaintext, so the migration is
**transparent, on next login**:

1. Add `hashPassword(plaintext) (string, error)` and
   `verifyPassword(stored, plaintext) (ok bool, needsRehash bool)`.
   - `needsRehash` is true only for the legacy format, after it verified.
2. The legacy branch recomputes the old SHA-256 and compares with
   `crypto/subtle.ConstantTimeCompare`.
3. `CheckPassword` (`database/accounts/accounts.go:20`) becomes:
   - load the user
   - `ok, needsRehash := verifyPassword(user.Passwd, passwd)`
   - if `!ok` → return `"", false`
   - if `needsRehash` → re-hash and `UPDATE users SET passwd = ?`, logging (not
     failing on) a write error, so the login still succeeds
   - return the uuid
4. Every writer switches to the new hash:
   - `ForceResetPassword` (`accounts.go:35`) — used by `cmd/chpasswd.go:35`, the
     documented recovery path, so the CLI gets the new format for free
   - `CreateAccountWithDB` (`accounts.go:60`) — used by the install wizard
     (`web/install/install.go:175`)
   - `UpdateUser` (`accounts.go:140`)
5. Keep the legacy verifier indefinitely. Removing it would lock out any account
   that has not logged in since the upgrade. On a single-admin panel that is one
   login away.
6. Optional: a `migrate-hash` check that reports how many accounts still hold a
   legacy hash, so "we think we migrated" is measurable rather than assumed.

### Tests

- legacy hash verifies and reports `needsRehash == true`
- new hash verifies and reports `needsRehash == false`
- `CheckPassword` rewrites a legacy row — assert the stored value changed and
  still verifies afterwards
- wrong password fails for both formats
- install and `chpasswd` produce a value with the new prefix

### Not in scope

2FA secrets (`users.two_factor`) are stored as-is, and session tokens are stored
in plaintext in `sessions`. Encrypting either at rest is a separate change.

## 2. Login throttling

### Current state

`web/api/public/login.go:62` calls `accounts.CheckPassword` with no attempt
counter, no backoff and no lockout. `Verify2Fa` (`login.go:74`, and
`web/api/AuthSensitive.go:46`) is equally unlimited — a 6-digit TOTP is
brute-forceable if requests can be sent fast enough.

v0.1.10 bounded the request body (1 MiB) but not the attempt rate.

### Target

Throttle **both** the password and the 2FA step, keyed on the client IP and on the
account, with a **soft per-account backoff** rather than a hard lockout — a hard
account lockout is itself a denial-of-service on the admin account.

Suggested policy:

| Scope | Rule |
|---|---|
| per IP | token bucket: burst 10, refill 1 / 90 s → `429` + `Retry-After` |
| per account | after 5 consecutive failures, exponential delay 1s, 2s, 4s … capped at 30s, before responding; reset on success |
| 2FA | same bucket as the password step, or a smaller one (5 / 15 min) |

### Storage

In-memory is enough: the panel is a single process and an attacker cannot restart
it. Reuse the shape of `visitorAuditRateLimiter`
(`web/rpc/jsonrpc/public.audit.go:133`): a bounded map with periodic cleanup, so a
flood of distinct source IPs cannot grow memory without limit.

If a persistent lockout is ever wanted, add a `login_attempts` table and prune it
on a schedule — do not write to the database on every failed attempt.

### Migration steps

1. Add a limiter (e.g. `web/api/public/login_limiter.go`) with
   `Allow(key string, now time.Time) (ok bool, retryAfter time.Duration)`,
   `RecordFailure(key)` and `Reset(key)`.
2. Wire it into `Login` before `CheckPassword` and before `Verify2Fa`:
   - build keys from `c.ClientIP()` (trustworthy after the v0.1.10 trusted-proxy
     fix) and the lowercased username
   - not allowed → `429` with `Retry-After`
   - failure → `RecordFailure`; success → `Reset`
3. Do not reveal whether the username exists: an unknown user must hit the same
   limiter and produce the same response as a wrong password.
4. Config: hardcode sensible defaults first; only add
   `login_max_attempts` / `login_window_seconds` once the defaults are proven.

### Tests

- the limiter allows the burst, denies the next attempt, recovers after the window
- a success resets the counter
- unknown-user and wrong-password paths are indistinguishable in status and timing
- 2FA failures count toward the same limit
- the internal map stays bounded under many distinct keys

### Interaction

- `DisablePasswordLogin` (`login.go:37`) short-circuits before the limiter; skip
  throttling there.
- OAuth/OIDC logins do not use this path.

## Order

1. **Password hashing first** — bigger leak risk, self-contained, no policy choice.
2. **Throttling second** — touches request handling and needs a policy decision.

Either can ship as its own patch release. Both should follow the release rule in
`docs/RELEASING.md`: wait for CI green on the exact commit before tagging.
