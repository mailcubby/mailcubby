package ratelimit

import (
	"strconv"
	"sync"
	"sync/atomic"
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
		if !newly || reason != LockoutReasonIPAndUsername {
			t.Fatalf("5th failure: got newly=%v reason=%q, want (true, ip_and_username)", newly, reason)
		}
		if !lim.IsLocked("10.0.0.1", "shop-a1b2c3d4") {
			t.Fatalf("expected locked after 5 failures")
		}
		// No global per-username bucket: the same username from another IP is not locked.
		if lim.IsLocked("10.0.0.2", "shop-a1b2c3d4") {
			t.Fatalf("same username from another IP should not be locked")
		}
		// Same IP with another username is not locked (IP ceiling is 5x).
		if lim.IsLocked("10.0.0.1", "other-user") {
			t.Fatalf("other username from the same IP should not be locked")
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

	t.Run("per-IP ceiling is 5x the pair limit across usernames", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := NewSMTPAuthLimiter(3, 5*time.Minute, clk)

		// 14 failures spread over 5 usernames (3+3+3+3+2): every pair but the last is locked, IP is not.
		for u := range 5 {
			n := 3
			if u == 4 {
				n = 2
			}
			for range n {
				if newly, reason := lim.RecordFailure("10.0.0.5", "u"+strconv.Itoa(u)); newly && reason != LockoutReasonIPAndUsername {
					t.Fatalf("unexpected reason %q before the IP ceiling", reason)
				}
			}
		}
		if lim.IsLocked("10.0.0.5", "fresh-user") {
			t.Fatalf("IP locked after 14 failures, ceiling is 15")
		}
		// 15th failure crosses the IP ceiling: every username from that IP is locked.
		newly, reason := lim.RecordFailure("10.0.0.5", "u4")
		if !newly || reason != LockoutReasonIP {
			t.Fatalf("15th failure = (%v, %q), want (true, ip)", newly, reason)
		}
		if !lim.IsLocked("10.0.0.5", "fresh-user") {
			t.Fatalf("expected IP ceiling to lock a fresh username")
		}
		if lim.IsLocked("10.0.0.6", "fresh-user") {
			t.Fatalf("IP ceiling must not affect other IPs")
		}
	})

	t.Run("no global per-username lockout", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := NewSMTPAuthLimiter(3, 5*time.Minute, clk)

		// Many IPs failing for one username (usernames are not secret) never lock
		// the legitimate client on its own IP.
		for i := range 20 {
			lim.RecordFailure("10.0.1."+strconv.Itoa(i), "victim")
		}
		if lim.IsLocked("10.0.2.1", "victim") {
			t.Fatalf("username locked globally; want per-(IP, username) only")
		}
	})

	t.Run("locked pair does not add to the IP ceiling", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := NewSMTPAuthLimiter(2, 15*time.Minute, clk)

		// A misconfigured client retrying forever with a stale password.
		for range 50 {
			if res, locked := lim.Reserve("10.0.0.7", "stale"); !locked {
				lim.CommitFailure(res)
			}
		}
		if !lim.IsLocked("10.0.0.7", "stale") {
			t.Fatalf("expected stale pair locked")
		}
		if lim.IsLocked("10.0.0.7", "healthy") {
			t.Fatalf("retries of a locked pair locked the whole IP")
		}
	})

	// Python smtplib's login() tries AUTH PLAIN, then AUTH LOGIN after a 535,
	// so one wrong login() costs 2 failures.
	t.Run("smtplib two failures per login", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := NewSMTPAuthLimiter(5, 15*time.Minute, clk)

		login := func(user string) (lockedAt int) {
			for attempt := 1; attempt <= 2; attempt++ {
				res, locked := lim.Reserve("172.17.0.1", user)
				if locked {
					return attempt
				}
				lim.CommitFailure(res)
			}
			return 0
		}
		for call, want := range []int{0, 0, 2} {
			if got := login("client-a"); got != want {
				t.Fatalf("login() call %d locked at attempt %d, want %d", call+1, got, want)
			}
		}
		// Another client behind the same IP (Docker bridge gateway) is unaffected.
		res, locked := lim.Reserve("172.17.0.1", "client-b")
		if locked {
			t.Fatalf("client-b locked by client-a's failures")
		}
		lim.Cancel(res)
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

	t.Run("concurrent Reserve bounds attempts to maxFailures and emits lockout once", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
		lim := NewSMTPAuthLimiter(5, 15*time.Minute, clk)

		const workers = 20
		start := make(chan struct{})
		var (
			wg           sync.WaitGroup
			allowedCount atomic.Int32
			lockedCount  atomic.Int32
			auditCount   atomic.Int32
		)
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				res, locked := lim.Reserve("10.0.0.9", "shop-concurrent")
				if locked {
					lockedCount.Add(1)
					return
				}
				allowedCount.Add(1)
				if newly, _ := lim.CommitFailure(res); newly {
					auditCount.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()

		if allowedCount.Load() != 5 {
			t.Fatalf("allowed attempts = %d, want 5", allowedCount.Load())
		}
		if lockedCount.Load() != workers-5 {
			t.Fatalf("locked attempts = %d, want %d", lockedCount.Load(), workers-5)
		}
		if auditCount.Load() != 1 {
			t.Fatalf("newlyLocked audit count = %d, want 1", auditCount.Load())
		}

		// Verify Cancel releases a reserved slot so successful auth never counts as failure.
		lim2 := NewSMTPAuthLimiter(2, 15*time.Minute, clk)
		for range 5 {
			res, locked := lim2.Reserve("10.0.0.10", "shop-ok")
			if locked {
				t.Fatalf("unexpected lockout when all attempts succeed")
			}
			lim2.Cancel(res)
		}
	})
}
