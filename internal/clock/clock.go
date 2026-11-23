// Package clock provides an injectable time abstraction with real and fake
// implementations so time-dependent logic can be tested deterministically.
package clock

import "time"

// Clock abstracts wall-clock time, timers, and tickers.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
	NewTimer(d time.Duration) Timer
	After(d time.Duration) <-chan time.Time
}

// Ticker wraps a repeating time channel.
type Ticker interface {
	C() <-chan time.Time
	Reset(d time.Duration)
	Stop()
}

// Timer wraps a single-shot time channel.
type Timer interface {
	C() <-chan time.Time
	Reset(d time.Duration) bool
	Stop() bool
}

// Real implements Clock using the standard library time package.
type Real struct{}

// NewReal returns a Clock backed by the system clock.
func NewReal() *Real {
	return &Real{}
}

// Now returns the current local time.
func (*Real) Now() time.Time {
	return time.Now()
}

// NewTicker returns a new Ticker containing a channel that receives the time
// after each tick of duration d.
func (*Real) NewTicker(d time.Duration) Ticker {
	return &realTicker{t: time.NewTicker(d)}
}

// NewTimer creates a new Timer that sends the current time on its channel
// after at least duration d.
func (*Real) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

// After waits for the duration to elapse and then sends the current time on
// the returned channel.
func (*Real) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

type realTicker struct {
	t *time.Ticker
}

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Reset(d time.Duration) {
	r.t.Reset(d)
}
func (r *realTicker) Stop() { r.t.Stop() }

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) C() <-chan time.Time      { return r.t.C }
func (r *realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }
func (r *realTimer) Stop() bool               { return r.t.Stop() }
