package ratelimit_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
	"github.com/mailcubby/mailcubby/internal/ratelimit"
)

func TestSlidingWindowCheckAndRecord(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	lim := ratelimit.NewSlidingWindow(10, 15*time.Minute, clk)

	// Record 10 failures; check stays allowed before each failure.
	for i := range 10 {
		ok, _ := lim.Check("127.0.0.1|admin@example.com")
		if !ok {
			t.Fatalf("attempt %d: expected allowed before recording", i+1)
		}
		lim.Record("127.0.0.1|admin@example.com")
		clk.Advance(10 * time.Second)
	}

	// 11th attempt within 15m is blocked with Retry-After.
	ok, retry := lim.Check("127.0.0.1|admin@example.com")
	if ok {
		t.Fatalf("expected 11th attempt to be rate-limited")
	}
	if sec := ratelimit.RetryAfterSeconds(retry); sec <= 0 || sec > 900 {
		t.Fatalf("RetryAfterSeconds = %d, want in (0, 900]", sec)
	}

	// Different key is unaffected.
	if ok, _ := lim.Check("127.0.0.1|other@example.com"); !ok {
		t.Fatalf("expected unrelated key to remain allowed")
	}

	// Advancing past the first failure's window allows again.
	clk.Advance(retry)
	if ok, _ := lim.Check("127.0.0.1|admin@example.com"); !ok {
		t.Fatalf("expected key to be allowed after window expiry")
	}
}

func TestSlidingWindowAllow(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	lim := ratelimit.NewSlidingWindow(3, time.Minute, clk)

	for i := range 3 {
		if ok, _ := lim.Allow("10.0.0.1"); !ok {
			t.Fatalf("request %d unexpectedly blocked", i+1)
		}
	}
	if ok, retry := lim.Allow("10.0.0.1"); ok || retry < time.Second {
		t.Fatalf("4th request = (%v, %v), want (false, >=1s)", ok, retry)
	}

	clk.Advance(time.Minute)
	if ok, _ := lim.Allow("10.0.0.1"); !ok {
		t.Fatalf("expected request allowed after 1m window")
	}
}

// SPEC 12.4: Reserve counts an attempt up front and Cancel gives it back, so
// concurrent callers can never exceed the limit while only failures persist.
func TestSlidingWindowReserveAndCancel(t *testing.T) {
	t.Parallel()

	t.Run("reserve enforces the limit and cancel frees one slot", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := ratelimit.NewSlidingWindow(3, time.Minute, clk)

		var ats []time.Time
		for i := range 3 {
			at, ok, _ := lim.Reserve("k")
			if !ok {
				t.Fatalf("reserve %d unexpectedly blocked", i+1)
			}
			if !at.Equal(clk.Now()) {
				t.Fatalf("reserve %d at = %v, want %v", i+1, at, clk.Now())
			}
			ats = append(ats, at)
			clk.Advance(time.Second)
		}
		if at, ok, retry := lim.Reserve("k"); ok || !at.IsZero() || retry < time.Second {
			t.Fatalf("4th reserve = (%v, %v, %v), want (zero, false, >=1s)", at, ok, retry)
		}

		lim.Cancel("k", ats[1])
		if _, ok, _ := lim.Reserve("k"); !ok {
			t.Fatalf("reserve after cancel unexpectedly blocked")
		}
		if _, ok, _ := lim.Reserve("k"); ok {
			t.Fatalf("cancel freed more than one slot")
		}
	})

	t.Run("cancel of an expired event does not remove newer events", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := ratelimit.NewSlidingWindow(2, time.Minute, clk)

		old, _, _ := lim.Reserve("k")
		clk.Advance(2 * time.Minute)
		for i := range 2 {
			if _, ok, _ := lim.Reserve("k"); !ok {
				t.Fatalf("reserve %d after window unexpectedly blocked", i+1)
			}
		}
		lim.Cancel("k", old)
		if ok, _ := lim.Check("k"); ok {
			t.Fatalf("cancel of an expired event removed a newer event")
		}
	})

	t.Run("cancel of the last event clears the key and other keys are untouched", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := ratelimit.NewSlidingWindow(1, time.Minute, clk)

		at, ok, _ := lim.Reserve("k")
		if !ok {
			t.Fatalf("first reserve unexpectedly blocked")
		}
		lim.Cancel("other", at)
		if ok, _ := lim.Check("k"); ok {
			t.Fatalf("cancel on another key freed this key")
		}
		lim.Cancel("k", at)
		if ok, _ := lim.Check("k"); !ok {
			t.Fatalf("key still blocked after cancelling its only event")
		}
	})

	t.Run("disabled limiter always reserves and cancel is a no-op", func(t *testing.T) {
		t.Parallel()
		lim := ratelimit.NewSlidingWindow(0, time.Minute, clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)))
		for range 5 {
			at, ok, _ := lim.Reserve("k")
			if !ok || !at.IsZero() {
				t.Fatalf("disabled reserve = (%v, %v), want (zero, true)", at, ok)
			}
			lim.Cancel("k", at)
		}
	})

	t.Run("concurrent reserves never exceed the limit", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := ratelimit.NewSlidingWindow(10, time.Minute, clk)

		const n = 50
		var granted atomic.Int32
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				if _, ok, _ := lim.Reserve("k"); ok {
					granted.Add(1)
				}
			})
		}
		wg.Wait()
		if got := granted.Load(); got != 10 {
			t.Fatalf("granted = %d, want 10", got)
		}
	})
}
