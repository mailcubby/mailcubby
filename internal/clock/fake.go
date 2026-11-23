package clock

import (
	"sync"
	"time"
)

// Fake is a deterministic Clock whose time advances only when Advance is called.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*fakeTimer]struct{}
	tickers map[*fakeTicker]struct{}
}

// NewFake creates a Fake clock starting at start.
func NewFake(start time.Time) *Fake {
	return &Fake{
		now:     start,
		timers:  make(map[*fakeTimer]struct{}),
		tickers: make(map[*fakeTicker]struct{}),
	}
}

// Now returns the fake clock's current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the fake clock forward by d and fires any timers or tickers
// whose deadline is at or before the new time. Non-positive durations are a no-op.
func (f *Fake) Advance(d time.Duration) {
	if d <= 0 {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.now = f.now.Add(d)

	for t := range f.timers {
		if !t.deadline.After(f.now) {
			delete(f.timers, t)
			t.active = false
			select {
			case t.ch <- t.deadline:
			default:
			}
		}
	}

	for tk := range f.tickers {
		if !tk.next.After(f.now) {
			fireAt := tk.next
			for !tk.next.After(f.now) {
				tk.next = tk.next.Add(tk.period)
			}
			select {
			case tk.ch <- fireAt:
			default:
			}
		}
	}
}

// NewTimer creates a fake Timer scheduled to fire after d from Now.
func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()

	t := &fakeTimer{
		clock: f,
		ch:    make(chan time.Time, 1),
	}
	if d <= 0 {
		t.ch <- f.now
		return t
	}
	t.deadline = f.now.Add(d)
	t.active = true
	f.timers[t] = struct{}{}
	return t
}

// After returns a channel that receives the fake time once the clock advances by d.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	return f.NewTimer(d).C()
}

// NewTicker creates a fake Ticker that ticks every d as the clock advances.
// It panics if d <= 0, matching time.NewTicker.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	tk := &fakeTicker{
		clock:  f,
		ch:     make(chan time.Time, 1),
		period: d,
		next:   f.now.Add(d),
	}
	f.tickers[tk] = struct{}{}
	return tk
}

type fakeTimer struct {
	clock    *Fake
	ch       chan time.Time
	deadline time.Time
	active   bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	wasActive := t.active
	if t.active {
		delete(t.clock.timers, t)
		t.active = false
	}
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	wasActive := t.active
	if t.active {
		delete(t.clock.timers, t)
		t.active = false
	}
	if d <= 0 {
		select {
		case t.ch <- t.clock.now:
		default:
		}
		return wasActive
	}
	t.deadline = t.clock.now.Add(d)
	t.active = true
	t.clock.timers[t] = struct{}{}
	return wasActive
}

type fakeTicker struct {
	clock  *Fake
	ch     chan time.Time
	period time.Duration
	next   time.Time
}

func (tk *fakeTicker) C() <-chan time.Time { return tk.ch }

func (tk *fakeTicker) Stop() {
	tk.clock.mu.Lock()
	defer tk.clock.mu.Unlock()
	delete(tk.clock.tickers, tk)
}

func (tk *fakeTicker) Reset(d time.Duration) {
	if d <= 0 {
		panic("clock: non-positive interval for Ticker.Reset")
	}
	tk.clock.mu.Lock()
	defer tk.clock.mu.Unlock()
	tk.period = d
	tk.next = tk.clock.now.Add(d)
	tk.clock.tickers[tk] = struct{}{}
}
