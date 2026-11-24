package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

type tokenBucketState struct {
	tokens     float64
	lastRefill time.Time
}

// TokenBucket enforces an average rate of capacity tokens per window per key
// with burst up to capacity, driven by an injected clock (SPEC 12.4).
// When capacity <= 0 or window <= 0, the limiter is disabled and allows all calls.
type TokenBucket struct {
	capacity     float64
	refillPerSec float64
	window       time.Duration
	clk          clock.Clock

	mu        sync.Mutex
	buckets   map[string]*tokenBucketState
	lastPrune time.Time
}

// NewTokenBucket creates a TokenBucket limiter allowing capacity requests per
// window (for example, 1200/m) backed by clk.
func NewTokenBucket(capacity int, window time.Duration, clk clock.Clock) *TokenBucket {
	if clk == nil {
		clk = clock.NewReal()
	}
	var rate float64
	if capacity > 0 && window > 0 {
		rate = float64(capacity) / window.Seconds()
	}
	return &TokenBucket{
		capacity:     float64(capacity),
		refillPerSec: rate,
		window:       window,
		clk:          clk,
		buckets:      make(map[string]*tokenBucketState),
	}
}

// Allow consumes one token for key if available. When allowed is false,
// retryAfter is the positive duration until one full token is refilled.
func (tb *TokenBucket) Allow(key string) (allowed bool, retryAfter time.Duration) {
	if tb.capacity <= 0 || tb.refillPerSec <= 0 {
		return true, 0
	}
	now := tb.clk.Now()

	tb.mu.Lock()
	defer tb.mu.Unlock()

	b, ok := tb.buckets[key]
	if !ok {
		b = &tokenBucketState{
			tokens:     tb.capacity,
			lastRefill: now,
		}
		tb.buckets[key] = b
	} else {
		if elapsed := now.Sub(b.lastRefill); elapsed > 0 {
			b.tokens = math.Min(tb.capacity, b.tokens+elapsed.Seconds()*tb.refillPerSec)
			b.lastRefill = now
		}
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		tb.maybePruneLocked(now)
		return true, 0
	}

	needed := 1.0 - b.tokens
	waitSec := needed / tb.refillPerSec
	wait := time.Duration(math.Ceil(waitSec * float64(time.Second)))
	if wait < time.Second {
		wait = time.Second
	}
	tb.maybePruneLocked(now)
	return false, wait
}

func (tb *TokenBucket) maybePruneLocked(now time.Time) {
	if len(tb.buckets) <= pruneThreshold {
		return
	}
	minInterval := max(time.Second, tb.window/4)
	if !tb.lastPrune.IsZero() && now.Sub(tb.lastPrune) < minInterval {
		return
	}
	tb.lastPrune = now
	for k, b := range tb.buckets {
		if now.Sub(b.lastRefill) >= tb.window {
			delete(tb.buckets, k)
		}
	}
}
