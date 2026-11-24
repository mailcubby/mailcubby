package ratelimit

import (
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

type lockoutBucket struct {
	failures []time.Time
	audited  bool
}

// SMTPAuthReservation holds an atomically reserved SMTP AUTH failure slot
// returned by Reserve. Callers must call either Cancel (on authentication
// success or transient error) or CommitFailure (on authentication failure).
type SMTPAuthReservation struct {
	ip       string
	username string
	at       time.Time
	active   bool
}

// SMTPAuthLimiter enforces SPEC 12.4 SMTP AUTH brute-force lockout per client IP
// and per username over a sliding window driven by an injected clock.
type SMTPAuthLimiter struct {
	maxFailures int
	window      time.Duration
	clk         clock.Clock

	mu        sync.Mutex
	byIP      map[string]*lockoutBucket
	byUser    map[string]*lockoutBucket
	lastPrune time.Time
}

// NewSMTPAuthLimiter creates a brute-force lockout tracker.
// When maxFailures <= 0 or window <= 0, lockout is disabled.
func NewSMTPAuthLimiter(maxFailures int, window time.Duration, clk clock.Clock) *SMTPAuthLimiter {
	if clk == nil {
		clk = clock.NewReal()
	}
	return &SMTPAuthLimiter{
		maxFailures: maxFailures,
		window:      window,
		clk:         clk,
		byIP:        make(map[string]*lockoutBucket),
		byUser:      make(map[string]*lockoutBucket),
	}
}

// IsLocked reports whether either ip or username currently has at least
// maxFailures recorded failures within the sliding lockout window.
func (l *SMTPAuthLimiter) IsLocked(ip, username string) bool {
	if l.maxFailures <= 0 || l.window <= 0 {
		return false
	}
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if ip != "" && l.isBucketLockedLocked(l.byIP, ip, now) {
		return true
	}
	if username != "" && l.isBucketLockedLocked(l.byUser, username, now) {
		return true
	}
	return false
}

// Reserve atomically checks whether ip or username is already locked and, if
// not, reserves one failure slot in both buckets before credential verification
// so concurrent AUTH attempts cannot bypass maxFailures. On success (or
// transient lookup error) the caller must call Cancel; on bad credentials the
// caller must call CommitFailure to keep the failure and check whether a new
// lockout audit entry should be emitted.
func (l *SMTPAuthLimiter) Reserve(ip, username string) (res SMTPAuthReservation, locked bool) {
	if l.maxFailures <= 0 || l.window <= 0 {
		return SMTPAuthReservation{}, false
	}
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if ip != "" && l.isBucketLockedLocked(l.byIP, ip, now) {
		return SMTPAuthReservation{}, true
	}
	if username != "" && l.isBucketLockedLocked(l.byUser, username, now) {
		return SMTPAuthReservation{}, true
	}

	if ip != "" {
		l.appendBucketLocked(l.byIP, ip, now)
	}
	if username != "" {
		l.appendBucketLocked(l.byUser, username, now)
	}
	l.maybePruneLocked(now)

	return SMTPAuthReservation{
		ip:       ip,
		username: username,
		at:       now,
		active:   true,
	}, false
}

// Cancel releases a slot previously obtained via Reserve (called after a
// successful AUTH or transient non-credential error).
func (l *SMTPAuthLimiter) Cancel(res SMTPAuthReservation) {
	if !res.active || l.maxFailures <= 0 || l.window <= 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if res.ip != "" {
		l.removeBucketTimestampLocked(l.byIP, res.ip, res.at)
	}
	if res.username != "" {
		l.removeBucketTimestampLocked(l.byUser, res.username, res.at)
	}
}

// CommitFailure finalizes a reserved slot as a failed AUTH attempt and reports
// newlyLocked=true at most once per lockout epoch (with reason "ip",
// "username", or "ip_and_username") so callers emit one smtp.auth_lockout audit
// entry per lockout (SPEC 12.4).
func (l *SMTPAuthLimiter) CommitFailure(res SMTPAuthReservation) (newlyLocked bool, reason string) {
	if !res.active || l.maxFailures <= 0 || l.window <= 0 {
		return false, ""
	}
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	var ipNewly, userNewly bool
	if res.ip != "" {
		ipNewly = l.checkNewlyLockedLocked(l.byIP, res.ip, now)
	}
	if res.username != "" {
		userNewly = l.checkNewlyLockedLocked(l.byUser, res.username, now)
	}

	switch {
	case ipNewly && userNewly:
		return true, "ip_and_username"
	case ipNewly:
		return true, "ip"
	case userNewly:
		return true, "username"
	default:
		return false, ""
	}
}

// RecordFailure records one failed SMTP AUTH attempt for ip and username.
// It returns newlyLocked=true at most once per lockout epoch (with reason
// "ip", "username", or "ip_and_username") so callers write one smtp.auth_lockout
// audit entry per lockout (SPEC 12.4).
func (l *SMTPAuthLimiter) RecordFailure(ip, username string) (newlyLocked bool, reason string) {
	if l.maxFailures <= 0 || l.window <= 0 {
		return false, ""
	}
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	var ipNewly, userNewly bool
	if ip != "" {
		ipNewly = l.recordBucketLocked(l.byIP, ip, now)
	}
	if username != "" {
		userNewly = l.recordBucketLocked(l.byUser, username, now)
	}

	l.maybePruneLocked(now)

	switch {
	case ipNewly && userNewly:
		return true, "ip_and_username"
	case ipNewly:
		return true, "ip"
	case userNewly:
		return true, "username"
	default:
		return false, ""
	}
}

func (l *SMTPAuthLimiter) isBucketLockedLocked(m map[string]*lockoutBucket, key string, now time.Time) bool {
	b, ok := m[key]
	if !ok {
		return false
	}
	l.pruneBucketLocked(m, key, b, now)
	return len(b.failures) >= l.maxFailures
}

func (l *SMTPAuthLimiter) appendBucketLocked(m map[string]*lockoutBucket, key string, now time.Time) *lockoutBucket {
	b, ok := m[key]
	if !ok {
		b = &lockoutBucket{}
		m[key] = b
	} else {
		l.pruneBucketLocked(m, key, b, now)
		if _, exists := m[key]; !exists {
			b = &lockoutBucket{}
			m[key] = b
		}
	}
	b.failures = append(b.failures, now)
	return b
}

func (l *SMTPAuthLimiter) checkNewlyLockedLocked(m map[string]*lockoutBucket, key string, now time.Time) bool {
	b, ok := m[key]
	if !ok {
		return false
	}
	l.pruneBucketLocked(m, key, b, now)
	if len(b.failures) >= l.maxFailures && !b.audited {
		b.audited = true
		return true
	}
	return false
}

func (l *SMTPAuthLimiter) removeBucketTimestampLocked(m map[string]*lockoutBucket, key string, at time.Time) {
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
		if len(b.failures) < l.maxFailures {
			b.audited = false
		}
		return
	}
}

func (l *SMTPAuthLimiter) recordBucketLocked(m map[string]*lockoutBucket, key string, now time.Time) bool {
	b := l.appendBucketLocked(m, key, now)
	if len(b.failures) >= l.maxFailures && !b.audited {
		b.audited = true
		return true
	}
	return false
}

func (l *SMTPAuthLimiter) maybePruneLocked(now time.Time) {
	if len(l.byIP) <= pruneThreshold && len(l.byUser) <= pruneThreshold {
		return
	}
	minInterval := max(time.Second, l.window/4)
	if !l.lastPrune.IsZero() && now.Sub(l.lastPrune) < minInterval {
		return
	}
	l.lastPrune = now
	if len(l.byIP) > pruneThreshold {
		l.pruneMapLocked(l.byIP, now)
	}
	if len(l.byUser) > pruneThreshold {
		l.pruneMapLocked(l.byUser, now)
	}
}

func (l *SMTPAuthLimiter) pruneBucketLocked(m map[string]*lockoutBucket, key string, b *lockoutBucket, now time.Time) {
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
	if len(b.failures) < l.maxFailures {
		b.audited = false
	}
}

func (l *SMTPAuthLimiter) pruneMapLocked(m map[string]*lockoutBucket, now time.Time) {
	for k, b := range m {
		l.pruneBucketLocked(m, k, b, now)
	}
}
