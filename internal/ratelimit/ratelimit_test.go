package ratelimit_test

import (
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
