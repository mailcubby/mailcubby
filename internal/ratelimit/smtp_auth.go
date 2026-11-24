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

// SMTPAuthLimiter enforces SPEC 12.4 SMTP AUTH brute-force lockout per client IP
// and per username over a sliding window driven by an injected clock.
type SMTPAuthLimiter struct {
	maxFailures int
	window      time.Duration
	clk         clock.Clock

	mu     sync.Mutex
	byIP   map[string]*lockoutBucket
	byUser map[string]*lockoutBucket
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

	if len(l.byIP) > pruneThreshold {
		l.pruneMapLocked(l.byIP, now)
	}
	if len(l.byUser) > pruneThreshold {
		l.pruneMapLocked(l.byUser, now)
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

func (l *SMTPAuthLimiter) isBucketLockedLocked(m map[string]*lockoutBucket, key string, now time.Time) bool {
	b, ok := m[key]
	if !ok {
		return false
	}
	l.pruneBucketLocked(m, key, b, now)
	return len(b.failures) >= l.maxFailures
}

func (l *SMTPAuthLimiter) recordBucketLocked(m map[string]*lockoutBucket, key string, now time.Time) bool {
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
	if len(b.failures) >= l.maxFailures && !b.audited {
		b.audited = true
		return true
	}
	return false
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
