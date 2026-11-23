package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/mailcubby/mailcubby/internal/clock"
)

func TestFakeNowAndAdvance(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)

	if got := clk.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	clk.Advance(0)
	clk.Advance(-5 * time.Second)
	if got := clk.Now(); !got.Equal(start) {
		t.Fatalf("non-positive Advance changed time to %v", got)
	}

	clk.Advance(15 * time.Second)
	want := start.Add(15 * time.Second)
	if got := clk.Now(); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}

func TestFakeTimerAndAfter(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)

	afterCh := clk.After(10 * time.Second)
	timer := clk.NewTimer(20 * time.Second)

	clk.Advance(9 * time.Second)
	select {
	case ts := <-afterCh:
		t.Fatalf("After fired early at %v", ts)
	default:
	}

	clk.Advance(1 * time.Second)
	select {
	case ts := <-afterCh:
		if !ts.Equal(start.Add(10 * time.Second)) {
			t.Fatalf("After timestamp = %v, want %v", ts, start.Add(10*time.Second))
		}
	default:
		t.Fatal("After did not fire when deadline reached")
	}

	if !timer.Stop() {
		t.Fatal("Stop() = false, want true for active timer")
	}
	if timer.Stop() {
		t.Fatal("second Stop() = true, want false")
	}

	clk.Advance(30 * time.Second)
	select {
	case ts := <-timer.C():
		t.Fatalf("stopped timer fired at %v", ts)
	default:
	}

	if timer.Reset(5 * time.Second) {
		t.Fatal("Reset() on stopped timer = true, want false")
	}
	if !timer.Reset(10 * time.Second) {
		t.Fatal("Reset() on active timer = false, want true")
	}
	clk.Advance(10 * time.Second)
	select {
	case ts := <-timer.C():
		if !ts.Equal(clk.Now()) {
			t.Fatalf("reset timer timestamp = %v, want %v", ts, clk.Now())
		}
	default:
		t.Fatal("reset timer did not fire")
	}
}

func TestFakeTimerImmediate(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)

	timer := clk.NewTimer(0)
	select {
	case ts := <-timer.C():
		if !ts.Equal(start) {
			t.Fatalf("timestamp = %v, want %v", ts, start)
		}
	default:
		t.Fatal("zero-duration timer did not fire immediately")
	}
}

func TestFakeTicker(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)

	tk := clk.NewTicker(25 * time.Second)
	defer tk.Stop()

	clk.Advance(24 * time.Second)
	select {
	case ts := <-tk.C():
		t.Fatalf("ticker fired early at %v", ts)
	default:
	}

	clk.Advance(1 * time.Second)
	select {
	case ts := <-tk.C():
		if !ts.Equal(start.Add(25 * time.Second)) {
			t.Fatalf("first tick = %v, want %v", ts, start.Add(25*time.Second))
		}
	default:
		t.Fatal("first tick did not fire")
	}

	// Advancing past multiple periods delivers one tick and reschedules past now.
	clk.Advance(60 * time.Second)
	select {
	case ts := <-tk.C():
		if !ts.Equal(start.Add(50 * time.Second)) {
			t.Fatalf("second tick = %v, want %v", ts, start.Add(50*time.Second))
		}
	default:
		t.Fatal("second tick did not fire")
	}

	tk.Reset(10 * time.Second)
	clk.Advance(10 * time.Second)
	select {
	case <-tk.C():
	default:
		t.Fatal("ticker did not fire after Reset")
	}

	tk.Stop()
	clk.Advance(100 * time.Second)
	select {
	case ts := <-tk.C():
		t.Fatalf("stopped ticker fired at %v", ts)
	default:
	}
}

func TestFakeConcurrent(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(1_700_000_000, 0).UTC())
	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm := clk.NewTimer(time.Second)
			defer tm.Stop()
			_ = clk.Now()
		}()
	}

	clk.Advance(2 * time.Second)
	wg.Wait()
}

func TestRealClockSmoke(t *testing.T) {
	t.Parallel()

	var clk clock.Clock = clock.NewReal()
	if clk.Now().IsZero() {
		t.Fatal("Real.Now() returned zero time")
	}

	tm := clk.NewTimer(time.Nanosecond)
	<-tm.C()
	tm.Stop()

	tk := clk.NewTicker(time.Millisecond)
	<-tk.C()
	tk.Reset(time.Millisecond)
	tk.Stop()

	<-clk.After(time.Nanosecond)
}
