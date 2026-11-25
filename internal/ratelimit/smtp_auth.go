package ratelimit

import (
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

// SMTPAuthIPMultiplier is the per-IP failure ceiling as a multiple of the
// per-(IP, username) limit, mirroring the HTTP login rule (SPEC 12.4).
const SMTPAuthIPMultiplier = 5

// Lockout reasons reported by CommitFailure and RecordFailure and written to
// the smtp.auth_lockout audit metadata.
const (
	// LockoutReasonIPAndUsername means one (IP, username) pair hit its limit.
	LockoutReasonIPAndUsername = "ip_and_username"
	// LockoutReasonIP means one IP hit the per-IP ceiling across usernames.
	LockoutReasonIP = "ip"
)

type lockoutBucket struct {
	failures []time.Time
	audited  bool
}

// SMTPAuthReservation holds an atomically reserved SMTP AUTH failure slot
// returned by Reserve. Callers must call either Cancel (on authentication
// success or a server-side error) or CommitFailure (on authentication failure).
type SMTPAuthReservation struct {
	ip     string
	pair   string
	at     time.Time
	active bool
}

// SMTPAuthLimiter enforces SPEC 12.4 SMTP AUTH brute-force lockout per
// (client IP, username) pair, with a per-IP ceiling of SMTPAuthIPMultiplier
// times that limit across usernames, over a sliding window driven by an
// injected clock. There is no global per-username bucket: one client with a
// stale password must not lock out other clients, and usernames are not secret.
type SMTPAuthLimiter struct {
	maxFailures   int
	ipMaxFailures int
	window        time.Duration
	clk           clock.Clock

	mu        sync.Mutex
	byPair    map[string]*lockoutBucket
	byIP      map[string]*lockoutBucket
	lastPrune time.Time
}

// NewSMTPAuthLimiter creates a brute-force lockout tracker. maxFailures is the
// per-(IP, username) limit; the per-IP ceiling is SMTPAuthIPMultiplier times
// that. When maxFailures <= 0 or window <= 0, lockout is disabled.
func NewSMTPAuthLimiter(maxFailures int, window time.Duration, clk clock.Clock) *SMTPAuthLimiter {
	if clk == nil {
		clk = clock.NewReal()
	}
	return &SMTPAuthLimiter{
		maxFailures:   maxFailures,
		ipMaxFailures: maxFailures * SMTPAuthIPMultiplier,
		window:        window,
		clk:           clk,
		byPair:        make(map[string]*lockoutBucket),
		byIP:          make(map[string]*lockoutBucket),
	}
}

func (l *SMTPAuthLimiter) disabled() bool {
	return l.maxFailures <= 0 || l.window <= 0
}

// pairKey joins ip and username with a separator that cannot appear in an IP.
func pairKey(ip, username string) string {
	return ip + "\x00" + username
}

// IsLocked reports whether the (ip, username) pair or ip itself is currently
// locked within the sliding lockout window.
func (l *SMTPAuthLimiter) IsLocked(ip, username string) bool {
	if l.disabled() {
		return false
	}
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.isLockedLocked(ip, pairKey(ip, username), now)
}

func (l *SMTPAuthLimiter) isLockedLocked(ip, pair string, now time.Time) bool {
	if l.isBucketLockedLocked(l.byPair, pair, l.maxFailures, now) {
		return true
	}
	return ip != "" && l.isBucketLockedLocked(l.byIP, ip, l.ipMaxFailures, now)
}

// Reserve atomically checks whether the (ip, username) pair or ip is already
// locked and, if not, reserves one failure slot in both buckets before
// credential verification so concurrent AUTH attempts cannot bypass the
// limits. A locked attempt reserves nothing, so a locked pair stops adding to
// the IP ceiling. On success (or a server-side error) the caller must call
// Cancel; on bad credentials the caller must call CommitFailure to keep the
// failure and check whether a new lockout audit entry should be emitted.
func (l *SMTPAuthLimiter) Reserve(ip, username string) (res SMTPAuthReservation, locked bool) {
	if l.disabled() {
		return SMTPAuthReservation{}, false
	}
	now := l.clk.Now()
	pair := pairKey(ip, username)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.isLockedLocked(ip, pair, now) {
		return SMTPAuthReservation{}, true
	}
	l.appendBucketLocked(l.byPair, pair, now)
	if ip != "" {
		l.appendBucketLocked(l.byIP, ip, now)
	}
	l.maybePruneLocked(now)

	return SMTPAuthReservation{ip: ip, pair: pair, at: now, active: true}, false
}

// Cancel releases a slot previously obtained via Reserve (called after a
// successful AUTH or a server-side error that is not the client's fault).
func (l *SMTPAuthLimiter) Cancel(res SMTPAuthReservation) {
	if !res.active || l.disabled() {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.removeBucketTimestampLocked(l.byPair, res.pair, res.at, l.maxFailures)
	if res.ip != "" {
		l.removeBucketTimestampLocked(l.byIP, res.ip, res.at, l.ipMaxFailures)
	}
}

// CommitFailure finalizes a reserved slot as a failed AUTH attempt and reports
// newlyLocked=true at most once per lockout epoch of each bucket (reason
// LockoutReasonIP when the per-IP ceiling is crossed, else
// LockoutReasonIPAndUsername) so callers emit one smtp.auth_lockout audit
// entry per lockout (SPEC 12.4).
func (l *SMTPAuthLimiter) CommitFailure(res SMTPAuthReservation) (newlyLocked bool, reason string) {
	if !res.active || l.disabled() {
		return false, ""
	}
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	pairNewly := l.checkNewlyLockedLocked(l.byPair, res.pair, l.maxFailures, now)
	ipNewly := res.ip != "" && l.checkNewlyLockedLocked(l.byIP, res.ip, l.ipMaxFailures, now)
	return lockoutReason(pairNewly, ipNewly)
}

// RecordFailure records one failed SMTP AUTH attempt for the (ip, username)
// pair and for ip, without the lock check of Reserve. It reports newlyLocked
// like CommitFailure.
func (l *SMTPAuthLimiter) RecordFailure(ip, username string) (newlyLocked bool, reason string) {
	if l.disabled() {
		return false, ""
	}
	now := l.clk.Now()
	pair := pairKey(ip, username)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.appendBucketLocked(l.byPair, pair, now)
	pairNewly := l.checkNewlyLockedLocked(l.byPair, pair, l.maxFailures, now)
	var ipNewly bool
	if ip != "" {
		l.appendBucketLocked(l.byIP, ip, now)
		ipNewly = l.checkNewlyLockedLocked(l.byIP, ip, l.ipMaxFailures, now)
	}
	l.maybePruneLocked(now)
	return lockoutReason(pairNewly, ipNewly)
}

func lockoutReason(pairNewly, ipNewly bool) (newlyLocked bool, reason string) {
	switch {
	case ipNewly:
		return true, LockoutReasonIP
	case pairNewly:
		return true, LockoutReasonIPAndUsername
	default:
		return false, ""
	}
}

func (l *SMTPAuthLimiter) isBucketLockedLocked(m map[string]*lockoutBucket, key string, limit int, now time.Time) bool {
	b, ok := m[key]
	if !ok {
		return false
	}
	l.pruneBucketLocked(m, key, b, limit, now)
	return len(b.failures) >= limit
}

// appendBucketLocked records a failure at now. Callers prune the bucket first
// (through isBucketLockedLocked) or prune it right after (checkNewlyLockedLocked).
func (l *SMTPAuthLimiter) appendBucketLocked(m map[string]*lockoutBucket, key string, now time.Time) {
	b, ok := m[key]
	if !ok {
		b = &lockoutBucket{}
		m[key] = b
	}
	b.failures = append(b.failures, now)
}

func (l *SMTPAuthLimiter) checkNewlyLockedLocked(m map[string]*lockoutBucket, key string, limit int, now time.Time) bool {
	b, ok := m[key]
	if !ok {
		return false
	}
	l.pruneBucketLocked(m, key, b, limit, now)
	if len(b.failures) >= limit && !b.audited {
		b.audited = true
		return true
	}
	return false
}

func (l *SMTPAuthLimiter) removeBucketTimestampLocked(m map[string]*lockoutBucket, key string, at time.Time, limit int) {
	b, ok := m[key]
	if !ok {
		return
	}
	for i := len(b.failures) - 1; i >= 0; i-- {
		if !b.failures[i].Equal(at) {
			continue
		}
		if len(b.failures) == 1 {
			delete(m, key)
			return
		}
		out := make([]time.Time, 0, len(b.failures)-1)
		out = append(out, b.failures[:i]...)
		b.failures = append(out, b.failures[i+1:]...)
		if len(b.failures) < limit {
			b.audited = false
		}
		return
	}
}

func (l *SMTPAuthLimiter) maybePruneLocked(now time.Time) {
	if len(l.byPair) <= pruneThreshold && len(l.byIP) <= pruneThreshold {
		return
	}
	minInterval := max(time.Second, l.window/4)
	if !l.lastPrune.IsZero() && now.Sub(l.lastPrune) < minInterval {
		return
	}
	l.lastPrune = now
	if len(l.byPair) > pruneThreshold {
		l.pruneMapLocked(l.byPair, l.maxFailures, now)
	}
	if len(l.byIP) > pruneThreshold {
		l.pruneMapLocked(l.byIP, l.ipMaxFailures, now)
	}
}

func (l *SMTPAuthLimiter) pruneBucketLocked(m map[string]*lockoutBucket, key string, b *lockoutBucket, limit int, now time.Time) {
	cutoff := now.Add(-l.window)
	idx := 0
	for idx < len(b.failures) && !b.failures[idx].After(cutoff) {
		idx++
	}
	if idx == len(b.failures) {
		b.failures = nil
		b.audited = false
		delete(m, key)
		return
	}
	if idx > 0 {
		b.failures = append([]time.Time(nil), b.failures[idx:]...)
	}
	if len(b.failures) < limit {
		b.audited = false
	}
}

func (l *SMTPAuthLimiter) pruneMapLocked(m map[string]*lockoutBucket, limit int, now time.Time) {
	for k, b := range m {
		l.pruneBucketLocked(m, k, b, limit, now)
	}
}
