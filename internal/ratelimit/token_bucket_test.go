package ratelimit

import (
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

func TestTokenBucket(t *testing.T) {
	t.Parallel()

	t.Run("enforces burst capacity and refills over time", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		// 60 per minute = 1 token/sec, burst 60.
		tb := NewTokenBucket(60, time.Minute, clk)

		for i := range 60 {
			ok, retry := tb.Allow("session:a")
			if !ok || retry != 0 {
				t.Fatalf("call %d: Allow = (%v, %v), want (true, 0)", i+1, ok, retry)
			}
		}

		// 61st call rejected with Retry-After >= 1s.
		ok, retry := tb.Allow("session:a")
		if ok || retry < time.Second {
			t.Fatalf("61st call: Allow = (%v, %v), want (false, >=1s)", ok, retry)
		}

		// Different key has its own independent bucket.
		if okOther, _ := tb.Allow("token:b"); !okOther {
			t.Fatalf("different key should be allowed")
		}

		// Advancing 500ms is not enough for 1 full token.
		clk.Advance(500 * time.Millisecond)
		if okHalf, _ := tb.Allow("session:a"); okHalf {
			t.Fatalf("expected still limited after 500ms")
		}

		// Advancing another 500ms refills 1 full token.
		clk.Advance(500 * time.Millisecond)
		if okOne, _ := tb.Allow("session:a"); !okOne {
			t.Fatalf("expected 1 token refilled after 1s")
		}
		if okExtra, _ := tb.Allow("session:a"); okExtra {
			t.Fatalf("expected bucket empty again after consuming refilled token")
		}

		// Advancing past a full window restores full capacity without exceeding it.
		clk.Advance(5 * time.Minute)
		for i := range 60 {
			if okFull, _ := tb.Allow("session:a"); !okFull {
				t.Fatalf("post-refill call %d denied unexpectedly", i+1)
			}
		}
		if okOver, _ := tb.Allow("session:a"); okOver {
			t.Fatalf("capacity exceeded burst after long idle")
		}
	})

	t.Run("disabled when capacity or window non-positive", func(t *testing.T) {
		t.Parallel()
		tb := NewTokenBucket(0, time.Minute, nil)
		for range 50 {
			if ok, _ := tb.Allow("k"); !ok {
				t.Fatalf("disabled token bucket must always allow")
			}
		}
	})
}
