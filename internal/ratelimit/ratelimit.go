// Package ratelimit implements in-memory sliding-window rate limiters driven
// by an injected clock (SPEC 12.4).
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

const pruneThreshold = 1024

// SlidingWindow enforces at most limit events per key within a rolling window.
// When limit <= 0 or window <= 0, the limiter is disabled and allows all calls.
type SlidingWindow struct {
	limit  int
	window time.Duration
	clk    clock.Clock

	mu      sync.Mutex
	entries map[string][]time.Time
}

// NewSlidingWindow returns a SlidingWindow limiter backed by clk.
func NewSlidingWindow(limit int, window time.Duration, clk clock.Clock) *SlidingWindow {
	if clk == nil {
		clk = clock.NewReal()
	}
	return &SlidingWindow{
		limit:   limit,
		window:  window,
		clk:     clk,
		entries: make(map[string][]time.Time),
	}
}

// Check reports whether key is currently below its limit without recording an
// event. When allowed is false, retryAfter is the positive duration until the
// oldest recorded event leaves the window.
func (sw *SlidingWindow) Check(key string) (allowed bool, retryAfter time.Duration) {
	if sw.limit <= 0 || sw.window <= 0 {
		return true, 0
	}
	now := sw.clk.Now()

	sw.mu.Lock()
	defer sw.mu.Unlock()

	ts := sw.pruneKeyLocked(key, now)
	if len(ts) >= sw.limit {
		return false, computeRetryAfter(ts[0], sw.window, now)
	}
	return true, 0
}

// Record appends an event timestamp for key at the current clock time.
func (sw *SlidingWindow) Record(key string) {
	if sw.limit <= 0 || sw.window <= 0 {
		return
	}
	now := sw.clk.Now()

	sw.mu.Lock()
	defer sw.mu.Unlock()

	ts := sw.pruneKeyLocked(key, now)
	sw.entries[key] = append(ts, now)
	if len(sw.entries) > pruneThreshold {
		sw.pruneAllLocked(now)
	}
}

// Allow atomically checks whether key is below its limit and, if so, records
// an event. When allowed is false, no event is recorded and retryAfter gives
// the wait duration.
func (sw *SlidingWindow) Allow(key string) (allowed bool, retryAfter time.Duration) {
	if sw.limit <= 0 || sw.window <= 0 {
		return true, 0
	}
	now := sw.clk.Now()

	sw.mu.Lock()
	defer sw.mu.Unlock()

	ts := sw.pruneKeyLocked(key, now)
	if len(ts) >= sw.limit {
		return false, computeRetryAfter(ts[0], sw.window, now)
	}
	sw.entries[key] = append(ts, now)
	if len(sw.entries) > pruneThreshold {
		sw.pruneAllLocked(now)
	}
	return true, 0
}

// Reset removes all recorded events for key.
func (sw *SlidingWindow) Reset(key string) {
	sw.mu.Lock()
	delete(sw.entries, key)
	sw.mu.Unlock()
}

// RetryAfterSeconds rounds a positive retry duration up to whole seconds (>= 1).
func RetryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	sec := int(math.Ceil(d.Seconds()))
	if sec < 1 {
		return 1
	}
	return sec
}

func (sw *SlidingWindow) pruneKeyLocked(key string, now time.Time) []time.Time {
	ts := sw.entries[key]
	if len(ts) == 0 {
		return nil
	}
	cutoff := now.Add(-sw.window)
	idx := 0
	for idx < len(ts) && !ts[idx].After(cutoff) {
		idx++
	}
	if idx == len(ts) {
		delete(sw.entries, key)
		return nil
	}
	if idx > 0 {
		ts = append([]time.Time(nil), ts[idx:]...)
		sw.entries[key] = ts
	}
	return ts
}

func (sw *SlidingWindow) pruneAllLocked(now time.Time) {
	cutoff := now.Add(-sw.window)
	for k, ts := range sw.entries {
		idx := 0
		for idx < len(ts) && !ts[idx].After(cutoff) {
			idx++
		}
		if idx == len(ts) {
			delete(sw.entries, k)
		} else if idx > 0 {
			sw.entries[k] = append([]time.Time(nil), ts[idx:]...)
		}
	}
}

func computeRetryAfter(oldest time.Time, window time.Duration, now time.Time) time.Duration {
	rem := oldest.Add(window).Sub(now)
	if rem < time.Second {
		return time.Second
	}
	return rem
}
