// Package main — s08-loop-detector.
//
// Clock is the smallest possible abstraction over wall-clock time so that the
// loop detector's stall/timeout checks can be unit-tested in microseconds
// rather than minutes. Production code uses realClock{} (delegates to
// time.Now); tests use *FakeClock and call Advance(d) to step time forward
// deterministically.
//
// Why an interface, not a function value (`now func() time.Time`): an
// interface lets a *FakeClock carry its own mutable `now` field while still
// presenting a value-type `Clock` to callers. A bare function closure would
// also work, but the interface form reads better at the constructor's
// option site (`WithClock(fc)` vs `WithNow(fc.Now)`).
package main

import "time"

// Clock yields the current instant. The single method matches time.Now's
// signature so realClock is a one-liner and *FakeClock can substitute it
// without ceremony.
type Clock interface {
	Now() time.Time
}

// realClock is the production implementation. Pass a value, not a pointer —
// it has no state.
type realClock struct{}

// Now returns time.Now() — production callers get monotonic-clock-aware
// timestamps automatically.
func (realClock) Now() time.Time { return time.Now() }

// FakeClock is a deterministic Clock for tests. The `now` field starts at
// the zero time.Time unless seeded; call Advance(d) to step it forward.
type FakeClock struct {
	now time.Time
}

// NewFakeClock seeds a FakeClock at the supplied instant. Tests that don't
// care about the absolute value can pass time.Unix(0, 0).
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

// Now returns the fake's current instant. Required to satisfy Clock.
func (f *FakeClock) Now() time.Time { return f.now }

// Advance moves the fake clock forward by d. Negative durations are allowed
// but discouraged — they exist only for exotic clock-skew tests.
func (f *FakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }
