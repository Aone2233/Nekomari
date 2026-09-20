package public

import (
	"strconv"
	"testing"
	"time"
)

// TestLoginLimiterThrottlesAccountAfterBurst pins the account bucket: the same
// account gets loginAccountBurst attempts, then is throttled until the bucket
// refills. It is a bucket, not a lock, so it recovers on its own.
func TestLoginLimiterThrottlesAccountAfterBurst(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	ip, account := "203.0.113.9", "admin"

	for i := 0; i < int(loginAccountBurst); i++ {
		if allowed, _ := limiter.Allow(ip, account, now); !allowed {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		limiter.RecordFailure(ip, account, now)
	}

	allowed, retry := limiter.Allow(ip, account, now)
	if allowed {
		t.Fatal("an attempt after the account burst should be throttled")
	}
	if retry < 250*time.Second || retry > 300*time.Second {
		t.Fatalf("retry-after = %v, want about 300s", retry)
	}

	if allowed, _ := limiter.Allow(ip, account, now.Add(loginAccountRefillSecs*time.Second)); !allowed {
		t.Fatal("an attempt should be allowed once the account bucket refills")
	}
}

// TestLoginLimiterThrottlesIPAcrossAccounts pins the IP bucket: attempts spread
// across many usernames still drain one source, so a single host cannot grind.
func TestLoginLimiterThrottlesIPAcrossAccounts(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	ip := "203.0.113.9"

	for i := 0; i < int(loginIPBurst); i++ {
		account := "user" + strconv.Itoa(i)
		if allowed, _ := limiter.Allow(ip, account, now); !allowed {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		limiter.RecordFailure(ip, account, now)
	}

	if allowed, _ := limiter.Allow(ip, "fresh-account", now); allowed {
		t.Fatal("a new account from a drained IP should be throttled")
	}
}

// TestLoginLimiterAccountBucketCoversDistributedAttack pins the reason the
// account bucket exists: many different source IPs on one account still hit a
// shared limit.
func TestLoginLimiterAccountBucketCoversDistributedAttack(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	account := "admin"

	for i := 0; i < int(loginAccountBurst); i++ {
		ip := "198.51.100." + strconv.Itoa(i)
		if allowed, _ := limiter.Allow(ip, account, now); !allowed {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		limiter.RecordFailure(ip, account, now)
	}

	if allowed, _ := limiter.Allow("192.0.2.200", account, now); allowed {
		t.Fatal("a fresh IP should still be blocked by the account bucket")
	}
}

// TestLoginLimiterResetClearsAccount: a fully successful login clears the
// account's failures, so a legitimate user who mistyped is not penalised.
func TestLoginLimiterResetClearsAccount(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	ip, account := "203.0.113.9", "admin"

	for i := 0; i < int(loginAccountBurst); i++ {
		limiter.RecordFailure(ip, account, now)
	}
	if allowed, _ := limiter.Allow(ip, account, now); allowed {
		t.Fatal("the account should be throttled before Reset")
	}

	limiter.Reset(account)

	if allowed, _ := limiter.Allow(ip, account, now); !allowed {
		t.Fatal("a successful login should clear the account bucket")
	}
}

// TestLoginLimiterStaysBounded: a flood of distinct keys must not grow memory
// without limit.
//
// It drives RecordFailure rather than Allow, because Allow deliberately no longer
// creates buckets — driving it here would have made the assertion vacuous.
func TestLoginLimiterStaysBounded(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()

	for i := 0; i < loginLimiterMaxEntries+100; i++ {
		ip := "10.0." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256)
		limiter.RecordFailure(ip, "admin", now)
	}

	if len(limiter.ips) > loginLimiterMaxEntries {
		t.Fatalf("ip bucket map grew to %d entries, want <= %d", len(limiter.ips), loginLimiterMaxEntries)
	}
}

// TestLoginLimiterAllowDoesNotCreateBuckets: an attempt that will be denied must
// not add entries to the maps.
//
// It used to. Allow looked both keys up through the creating accessor, so a
// request already denied by the IP bucket still inserted an account bucket — and
// the account map is bounded, which is what made the lockout below reachable.
func TestLoginLimiterAllowDoesNotCreateBuckets(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()

	for i := 0; i < 1000; i++ {
		limiter.Allow("203.0.113.9", "user"+strconv.Itoa(i), now)
	}

	if len(limiter.accounts) != 0 {
		t.Fatalf("Allow created %d account buckets; a denied request must not grow the map", len(limiter.accounts))
	}
	if len(limiter.ips) != 0 {
		t.Fatalf("Allow created %d IP buckets; it must not create any", len(limiter.ips))
	}
}

// TestLoginLimiterFullAccountMapDoesNotLockOutNewAccount is the regression test
// for the lockout: ~4096 cheap requests carrying distinct usernames must not be
// able to lock the only admin out.
//
// The old full-map branch returned a zero-token bucket that was never stored, so
// it never refilled: every account absent from the map got 429 with a full
// Retry-After for as long as the attacker kept the map topped up. Recovery was a
// process restart. A bucket that does not exist means "no failures recorded", so
// an unknown account must be allowed.
func TestLoginLimiterFullAccountMapDoesNotLockOutNewAccount(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()

	for i := 0; i < loginLimiterMaxEntries; i++ {
		limiter.RecordFailure("198.51.100.7", "flood"+strconv.Itoa(i), now)
	}
	if len(limiter.accounts) < loginLimiterMaxEntries {
		t.Fatalf("expected the account map to be full, got %d entries", len(limiter.accounts))
	}

	if allowed, retry := limiter.Allow("203.0.113.9", "admin", now); !allowed {
		t.Fatalf("the admin was locked out by a full account map (retry-after %v)", retry)
	}
}

// TestLoginLimiterFullMapEvictsOldest: a full map makes room by dropping the entry
// seen longest ago, so a flood costs the attacker its own oldest entries rather
// than locking everyone else out.
func TestLoginLimiterFullMapEvictsOldest(t *testing.T) {
	limiter := newLoginLimiter()
	base := time.Now()

	for i := 0; i < loginLimiterMaxEntries; i++ {
		limiter.RecordFailure("198.51.100.7", "flood"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Second))
	}
	if _, ok := limiter.accounts["flood0"]; ok {
		t.Error("the oldest account bucket should have been evicted to make room")
	}
	if _, ok := limiter.accounts["flood"+strconv.Itoa(loginLimiterMaxEntries-1)]; !ok {
		t.Error("the most recent account bucket should still be present")
	}
}

// TestLoginLimiterExpiresIdleEntries: entries are cleaned up after the TTL, so a
// long-running panel does not accumulate them.
func TestLoginLimiterExpiresIdleEntries(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	limiter.Allow("203.0.113.9", "admin", now)

	later := now.Add(loginLimiterEntryTTL + loginLimiterCleanupInterval)
	limiter.Allow("192.0.2.1", "admin", later)

	if _, ok := limiter.ips["203.0.113.9"]; ok {
		t.Fatal("an idle IP bucket should have been cleaned up")
	}
}
