package ratelimit

import (
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

func TestSMTPAuthLimiter(t *testing.T) {
	t.Parallel()

	t.Run("locks after maxFailures and recovers after window", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := NewSMTPAuthLimiter(5, 15*time.Minute, clk)

		for i := 1; i <= 4; i++ {
			if lim.IsLocked("10.0.0.1", "shop-a1b2c3d4") {
				t.Fatalf("unexpectedly locked before failure %d", i)
			}
			newly, reason := lim.RecordFailure("10.0.0.1", "shop-a1b2c3d4")
			if newly || reason != "" {
				t.Fatalf("failure %d: got newly=%v reason=%q, want false", i, newly, reason)
			}
		}

		// 5th failure triggers lockout and reports newlyLocked once.
		newly, reason := lim.RecordFailure("10.0.0.1", "shop-a1b2c3d4")
		if !newly || reason != "ip_and_username" {
			t.Fatalf("5th failure: got newly=%v reason=%q, want (true, ip_and_username)", newly, reason)
		}
		if !lim.IsLocked("10.0.0.1", "shop-a1b2c3d4") {
			t.Fatalf("expected locked after 5 failures")
		}
		// Other IP with same username is locked.
		if !lim.IsLocked("10.0.0.2", "shop-a1b2c3d4") {
			t.Fatalf("expected username lockout across IPs")
		}
		// Same IP with other username is locked.
		if !lim.IsLocked("10.0.0.1", "other-user") {
			t.Fatalf("expected IP lockout across usernames")
		}
		// Unrelated IP + username is not locked.
		if lim.IsLocked("10.0.0.2", "other-user") {
			t.Fatalf("unrelated IP and username should not be locked")
		}

		// Recording another failure while already locked does NOT re-trigger audit.
		newly, _ = lim.RecordFailure("10.0.0.1", "shop-a1b2c3d4")
		if newly {
			t.Fatalf("expected newlyLocked=false while already locked")
		}

		// Still locked right before window expiry.
		clk.Advance(14*time.Minute + 59*time.Second)
		if !lim.IsLocked("10.0.0.1", "shop-a1b2c3d4") {
			t.Fatalf("expected still locked at 14m59s")
		}

		// Unlocked once window passes.
		clk.Advance(2 * time.Second)
		if lim.IsLocked("10.0.0.1", "shop-a1b2c3d4") {
			t.Fatalf("expected unlocked after 15m window")
		}

		// A fresh set of 5 failures triggers a new lockout audit.
		for i := 1; i <= 4; i++ {
			if n, _ := lim.RecordFailure("10.0.0.1", "shop-a1b2c3d4"); n {
				t.Fatalf("unexpected early lockout on second epoch failure %d", i)
			}
		}
		if n, _ := lim.RecordFailure("10.0.0.1", "shop-a1b2c3d4"); !n {
			t.Fatalf("expected newlyLocked=true on second lockout epoch")
		}
	})

	t.Run("ip-only and username-only lockouts", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := NewSMTPAuthLimiter(3, 5*time.Minute, clk)

		// 3 failures from same IP across different usernames -> ip lockout.
		lim.RecordFailure("10.0.0.5", "u1")
		lim.RecordFailure("10.0.0.5", "u2")
		newly, reason := lim.RecordFailure("10.0.0.5", "u3")
		if !newly || reason != "ip" {
			t.Fatalf("ip-only lockout = (%v, %q), want (true, ip)", newly, reason)
		}

		// 3 failures for same username across different IPs -> username lockout.
		lim.RecordFailure("10.0.1.1", "victim")
		lim.RecordFailure("10.0.1.2", "victim")
		newly, reason = lim.RecordFailure("10.0.1.3", "victim")
		if !newly || reason != "username" {
			t.Fatalf("user-only lockout = (%v, %q), want (true, username)", newly, reason)
		}
	})

	t.Run("disabled when limit or window non-positive", func(t *testing.T) {
		t.Parallel()
		lim := NewSMTPAuthLimiter(0, 15*time.Minute, nil)
		for range 10 {
			if n, _ := lim.RecordFailure("10.0.0.1", "u"); n {
				t.Fatalf("disabled limiter should never report lockout")
			}
		}
		if lim.IsLocked("10.0.0.1", "u") {
			t.Fatalf("disabled limiter should never lock")
		}
	})
}
