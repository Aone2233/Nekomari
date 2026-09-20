package public

import (
	"math"
	"sync"
	"time"
)

// Login throttling.
//
// Two token buckets are checked before the password is verified and again before
// a 2FA code is checked:
//
//   - per source IP: burst 10, one token per 90s. Stops a single source from
//     grinding passwords.
//   - per account: burst 5, one token per 5 min. Slows a distributed attack on
//     one account. It is a bucket, not a lock — it refills — so the admin can
//     never be permanently locked out by someone else's guesses. That property
//     depends on two details that were both wrong at first: Allow must not create
//     buckets (or a denied flood fills the map), and a full map must evict rather
//     than hand out a bucket that is never stored and so never refills. See Allow
//     and bucketLocked.
//
// A failed password or 2FA step consumes one token from both. A fully successful
// login clears the account bucket; the IP bucket is deliberately left alone, so a
// valid login cannot launder throttling that other attempts earned.
//
// Keys are the source IP and the lowercased username, so an unknown username is
// throttled exactly like a real one and the response cannot be used to probe
// which accounts exist.
const (
	loginIPBurst         = 10.0
	loginIPRefillSeconds = 90.0

	loginAccountBurst      = 5.0
	loginAccountRefillSecs = 300.0

	loginLimiterCleanupInterval = 10 * time.Minute
	loginLimiterEntryTTL        = 30 * time.Minute
	loginLimiterMaxEntries      = 4096
)

type loginBucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

// refill tops the bucket up for the elapsed time, capped at burst.
func (b *loginBucket) refill(now time.Time, burst, refillSeconds float64) {
	if elapsed := now.Sub(b.lastRefill).Seconds(); elapsed > 0 {
		b.tokens = math.Min(burst, b.tokens+elapsed/refillSeconds)
		b.lastRefill = now
	}
	b.lastSeen = now
}

// retryAfter is how long until the bucket holds one token again.
func (b *loginBucket) retryAfter(refillSeconds float64) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	return time.Duration((1 - b.tokens) * refillSeconds * float64(time.Second))
}

type loginLimiter struct {
	mu          sync.Mutex
	ips         map[string]*loginBucket
	accounts    map[string]*loginBucket
	lastCleanup time.Time
}

var defaultLoginLimiter = newLoginLimiter()

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{ips: map[string]*loginBucket{}, accounts: map[string]*loginBucket{}}
}

// Allow reports whether an attempt may proceed. It does not consume a token and
// it does not create a bucket: RecordFailure does both. Two consequences, both
// deliberate:
//
//   - a successful login is never penalised, and
//   - a request that will be denied does not get to add an entry to the maps.
//
// The second one used to be false. Allow looked up both keys through the
// creating accessor, so every denied request still inserted an account bucket —
// and the account map is bounded, so ~4096 requests carrying distinct usernames
// filled it. The full-map branch then handed out a zero-token bucket that was
// never stored, which therefore never refilled: every account not already in the
// map, the admin's included, got 429 with a full Retry-After for as long as the
// attacker kept the map topped up. That is the hard-lockout-as-DoS this limiter
// exists to avoid.
func (l *loginLimiter) Allow(ip, account string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)

	ipRetry := l.peekLocked(l.ips, ip, now, loginIPBurst, loginIPRefillSeconds).retryAfter(loginIPRefillSeconds)
	accountRetry := l.peekLocked(l.accounts, account, now, loginAccountBurst, loginAccountRefillSecs).retryAfter(loginAccountRefillSecs)

	if ipRetry <= 0 && accountRetry <= 0 {
		return true, 0
	}
	if accountRetry > ipRetry {
		return false, accountRetry
	}
	return false, ipRetry
}

// RecordFailure consumes one token from both buckets.
func (l *loginLimiter) RecordFailure(ip, account string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)
	l.consumeLocked(l.ips, ip, now, loginIPBurst, loginIPRefillSeconds)
	l.consumeLocked(l.accounts, account, now, loginAccountBurst, loginAccountRefillSecs)
}

// Reset clears an account's failures after a fully successful login.
func (l *loginLimiter) Reset(account string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.accounts, account)
}

// peekLocked returns an existing bucket without creating one. An unknown key means
// "no failures recorded", so it reports a full bucket — which is what makes Allow
// safe to call on a request it is about to deny.
func (l *loginLimiter) peekLocked(buckets map[string]*loginBucket, key string, now time.Time, burst, refillSeconds float64) *loginBucket {
	if bucket, ok := buckets[key]; ok {
		bucket.refill(now, burst, refillSeconds)
		return bucket
	}
	return &loginBucket{tokens: burst, lastRefill: now, lastSeen: now}
}

// bucketLocked returns the bucket for key, creating a full one when needed.
//
// When a map is full it evicts the least recently seen entry rather than refusing.
// Failing closed here is what turned a bounded map into a lockout: the throwaway
// bucket held zero tokens, was never stored, and so could never refill. Eviction
// means a flood of distinct keys costs the attacker its own entries, and a real
// account always gets a bucket of its own.
func (l *loginLimiter) bucketLocked(buckets map[string]*loginBucket, key string, now time.Time, burst, refillSeconds float64) *loginBucket {
	if bucket, ok := buckets[key]; ok {
		bucket.refill(now, burst, refillSeconds)
		return bucket
	}
	if len(buckets) >= loginLimiterMaxEntries {
		l.evictOldestLocked(buckets)
	}
	bucket := &loginBucket{tokens: burst, lastRefill: now, lastSeen: now}
	buckets[key] = bucket
	return bucket
}

// evictOldestLocked drops the entry seen longest ago to make room for a new key.
func (l *loginLimiter) evictOldestLocked(buckets map[string]*loginBucket) {
	var oldestKey string
	var oldestSeen time.Time
	for key, bucket := range buckets {
		if oldestKey == "" || bucket.lastSeen.Before(oldestSeen) {
			oldestKey, oldestSeen = key, bucket.lastSeen
		}
	}
	if oldestKey != "" {
		delete(buckets, oldestKey)
	}
}

func (l *loginLimiter) consumeLocked(buckets map[string]*loginBucket, key string, now time.Time, burst, refillSeconds float64) {
	bucket := l.bucketLocked(buckets, key, now, burst, refillSeconds)
	bucket.tokens = math.Max(0, bucket.tokens-1)
}

func (l *loginLimiter) cleanupLocked(now time.Time) {
	if !l.lastCleanup.IsZero() && now.Sub(l.lastCleanup) < loginLimiterCleanupInterval {
		return
	}
	for key, bucket := range l.ips {
		if now.Sub(bucket.lastSeen) >= loginLimiterEntryTTL {
			delete(l.ips, key)
		}
	}
	for key, bucket := range l.accounts {
		if now.Sub(bucket.lastSeen) >= loginLimiterEntryTTL {
			delete(l.accounts, key)
		}
	}
	l.lastCleanup = now
}

// Step-up password checks share the login buckets.
//
// The 2FA re-enrollment endpoint asks the same question as login ("is this the
// account password?") for the same account, so it must spend the same budget: a
// stolen session must not turn re-enrollment into an unlimited password oracle
// that the login limiter cannot see.
func AllowSensitivePasswordCheck(ip, account string) (bool, time.Duration) {
	return defaultLoginLimiter.Allow(ip, account, time.Now())
}

// RecordSensitivePasswordFailure consumes one token from both step-up buckets.
func RecordSensitivePasswordFailure(ip, account string) {
	defaultLoginLimiter.RecordFailure(ip, account, time.Now())
}

// ResetSensitivePasswordFailures clears the account bucket after a correct
// password, exactly as a successful login does.
func ResetSensitivePasswordFailures(account string) {
	defaultLoginLimiter.Reset(account)
}
