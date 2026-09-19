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
func TestLoginLimiterStaysBounded(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()

	for i := 0; i < loginLimiterMaxEntries+100; i++ {
		ip := "10.0." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256)
		limiter.Allow(ip, "admin", now)
	}

	if len(limiter.ips) > loginLimiterMaxEntries {
		t.Fatalf("ip bucket map grew to %d entries, want <= %d", len(limiter.ips), loginLimiterMaxEntries)
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
