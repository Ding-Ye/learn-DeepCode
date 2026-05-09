// Package main — s08-loop-detector.
//
// LoopDetector watches an in-progress agent loop for three failure modes and
// returns a structured Status whenever something is wrong:
//
//  1. loop_detected — the same tool name was called MaxRepeats times in a row
//  2. timeout       — wall-clock from startedAt has exceeded Timeout
//  3. stall         — wall-clock since the last progress signal has exceeded
//     StallThreshold (after subtracting any noted LLM wait)
//  4. max_errors    — RecordError has been called MaxErrors times without an
//     intervening RecordSuccess
//
// The single non-obvious idea here is the LLM-wait offset. A naive stall
// detector measuring "seconds since last tool result" will fire every time
// the LLM itself takes 60+ seconds to respond — which is normal behaviour
// for long contexts, retries, or transient network latency. Calling
// NoteLLMWait(d) before the next CheckTool tells the detector "the next d
// seconds were spent waiting on the model, not on a frozen tool", and the
// stall arithmetic skips that interval. The offset is consumed (zeroed)
// inside CheckTool so each LLM round-trip credits exactly one stall window.
//
// Upstream counterpart: utils/loop_detector.py:12-180 (`class LoopDetector`).
// We map upstream's `dict` return to a typed `Status` value, and we replace
// the implicit `time.time()` calls with an injectable `Clock` so tests
// don't sleep.
package main

import (
	"fmt"
	"time"
)

// Status codes — string constants exported for callers that want to switch
// on them. Match upstream's status string literals verbatim so log lines
// from Go and Python pipelines compare cleanly.
const (
	StatusOK           = "ok"
	StatusLoopDetected = "loop_detected"
	StatusTimeout      = "timeout"
	StatusStall        = "stall"
	StatusMaxErrors    = "max_errors"
)

// Default thresholds — match upstream's __init__ defaults exactly.
// Documented in research-notes anti-pattern #10: making these configurable
// (which the upstream Python class also does, via __init__ kwargs) is the
// bare minimum; what's NEW here is exposing the thresholds via functional
// options that read cleanly at every call site.
const (
	defaultMaxRepeats     = 5
	defaultTimeout        = 600 * time.Second
	defaultStallThreshold = 300 * time.Second
	defaultMaxErrors      = 10
	historyWindow         = 10 // upstream keeps last-10 tool names for pattern matching
)

// Status is the typed return value from CheckTool. ShouldStop is a derived
// flag (true for every code except StatusOK) duplicated as a field so
// callers don't have to enumerate the codes.
type Status struct {
	Code       string
	Message    string
	ShouldStop bool
}

// LoopDetector is the safety wrapper around an agent loop. Construct via
// NewLoopDetector(opts...); call CheckTool(name) before each tool dispatch;
// call NoteLLMWait(d) after each LLM round-trip; call RecordSuccess on a
// successful write_file (or your equivalent progress signal) and
// RecordError on any tool/LLM exception.
//
// Zero-value is NOT safe — always go through NewLoopDetector. The
// constructor seeds the timestamps from the injected Clock so a fresh
// detector never reports a spurious "timeout" because of a Unix-epoch start.
type LoopDetector struct {
	// Configurable thresholds — set via Option functions.
	MaxRepeats     int
	Timeout        time.Duration
	StallThreshold time.Duration
	MaxErrors      int

	// Internal state — unexported. Tests in the same package can poke at
	// it if needed, but the public API never exposes mutation.
	clock             Clock
	history           []string // last historyWindow tool names, oldest first
	lastProgressAt    time.Time
	consecutiveErrors int
	pendingLLMOffset  time.Duration
	startedAt         time.Time
}

// Option configures a LoopDetector at construction time. Each Option mutates
// the partially-built detector — the constructor applies defaults first,
// then options override.
type Option func(*LoopDetector)

// WithMaxRepeats overrides the consecutive-same-tool threshold. A value
// less than 2 disables loop detection entirely (since no run of length 1
// is ever "repeated").
func WithMaxRepeats(n int) Option {
	return func(d *LoopDetector) { d.MaxRepeats = n }
}

// WithTimeout overrides the absolute wall-clock budget for one detector
// instance. Pass 0 to disable.
func WithTimeout(t time.Duration) Option {
	return func(d *LoopDetector) { d.Timeout = t }
}

// WithStallThreshold overrides the maximum no-progress interval. The
// LLM-wait offset is subtracted from the measured interval before the
// comparison, so this is "tool-side dead time" not "wall-clock dead time".
func WithStallThreshold(t time.Duration) Option {
	return func(d *LoopDetector) { d.StallThreshold = t }
}

// WithMaxErrors overrides how many consecutive RecordError calls are
// tolerated. RecordSuccess resets the counter to 0.
func WithMaxErrors(n int) Option {
	return func(d *LoopDetector) { d.MaxErrors = n }
}

// WithClock injects a Clock — production code skips this (defaults to
// realClock{}); tests pass a *FakeClock to drive timing deterministically.
func WithClock(c Clock) Option {
	return func(d *LoopDetector) { d.clock = c }
}

// NewLoopDetector builds a fresh detector with upstream-matching defaults,
// then applies any Option overrides. Both timestamp anchors are seeded
// from the (possibly faked) clock so timing is consistent from t=0.
func NewLoopDetector(opts ...Option) *LoopDetector {
	d := &LoopDetector{
		MaxRepeats:     defaultMaxRepeats,
		Timeout:        defaultTimeout,
		StallThreshold: defaultStallThreshold,
		MaxErrors:      defaultMaxErrors,
		clock:          realClock{},
		history:        make([]string, 0, historyWindow),
	}
	for _, opt := range opts {
		opt(d)
	}
	now := d.clock.Now()
	d.startedAt = now
	d.lastProgressAt = now
	return d
}

// CheckTool is invoked before dispatching tool `name`. It (a) appends to the
// history ring, (b) consumes any pending LLM-wait offset by shifting
// lastProgressAt forward, and (c) evaluates the four abort conditions in
// the same order as upstream: loop → timeout → stall → max_errors.
//
// The order matters: a hung tool that loops 5× in a single second should
// surface as "loop_detected" rather than waiting for the stall threshold.
func (d *LoopDetector) CheckTool(name string) Status {
	now := d.clock.Now()

	// (a) record the call in the ring buffer (last historyWindow entries).
	d.history = append(d.history, name)
	if len(d.history) > historyWindow {
		d.history = d.history[len(d.history)-historyWindow:]
	}

	// (b) apply any deferred LLM-wait offset. We move the "last progress"
	//     anchor forward by the offset so the next stall comparison sees a
	//     smaller idle interval. Zero the offset — each LLM round-trip
	//     should credit exactly one stall window.
	if d.pendingLLMOffset > 0 {
		d.lastProgressAt = d.lastProgressAt.Add(d.pendingLLMOffset)
		d.pendingLLMOffset = 0
	}

	// (c1) Loop detection: if the most recent MaxRepeats entries are all
	//      the same tool, abort. A MaxRepeats of <2 disables this check.
	if d.MaxRepeats >= 2 && len(d.history) >= d.MaxRepeats {
		recent := d.history[len(d.history)-d.MaxRepeats:]
		allSame := true
		for _, t := range recent[1:] {
			if t != recent[0] {
				allSame = false
				break
			}
		}
		if allSame {
			return Status{
				Code:       StatusLoopDetected,
				Message:    fmt.Sprintf("loop detected: %q called %d times consecutively", name, d.MaxRepeats),
				ShouldStop: true,
			}
		}
	}

	// (c2) Timeout: total elapsed since constructor exceeds the budget.
	if d.Timeout > 0 && now.Sub(d.startedAt) > d.Timeout {
		return Status{
			Code:       StatusTimeout,
			Message:    fmt.Sprintf("timeout: elapsed %s exceeded budget %s", now.Sub(d.startedAt).Round(time.Second), d.Timeout),
			ShouldStop: true,
		}
	}

	// (c3) Stall: time since last progress (after offset adjustment).
	if d.StallThreshold > 0 {
		idle := now.Sub(d.lastProgressAt)
		if idle > d.StallThreshold {
			return Status{
				Code:       StatusStall,
				Message:    fmt.Sprintf("progress stall: idle %s exceeded threshold %s", idle.Round(time.Second), d.StallThreshold),
				ShouldStop: true,
			}
		}
	}

	// (c4) Too many consecutive errors.
	if d.MaxErrors > 0 && d.consecutiveErrors >= d.MaxErrors {
		return Status{
			Code:       StatusMaxErrors,
			Message:    fmt.Sprintf("max errors: %d consecutive errors without progress", d.consecutiveErrors),
			ShouldStop: true,
		}
	}

	return Status{Code: StatusOK, Message: "processing normally", ShouldStop: false}
}

// RecordError increments the consecutive-error counter. It does NOT reset
// any timestamps — an error still counts as wall-clock time spent. The
// next CheckTool will surface "max_errors" once the counter hits MaxErrors.
func (d *LoopDetector) RecordError() {
	d.consecutiveErrors++
}

// RecordSuccess marks a real progress signal (typically a successful
// write_file, or whatever your workflow's "we got something done" event
// is). It (a) resets the consecutive-error counter, (b) bumps
// lastProgressAt to "now" so the stall window restarts, and (c) clears
// any pending LLM-wait offset (which would otherwise be stale).
func (d *LoopDetector) RecordSuccess() {
	d.consecutiveErrors = 0
	d.lastProgressAt = d.clock.Now()
	d.pendingLLMOffset = 0
}

// NoteLLMWait stages an LLM-call duration to be subtracted from the next
// stall calculation. Call this with `time.Since(llmStart)` after each
// LLM round-trip (including retries). Negative or zero durations are
// silently ignored. Multiple calls accumulate — useful when one logical
// "turn" makes several LLM calls.
//
// The offset is applied (and zeroed) the next time CheckTool runs, so the
// effect is "the stall window started d later than it actually did" — i.e.
// the LLM's idle time doesn't count against the tool-side stall budget.
func (d *LoopDetector) NoteLLMWait(d2 time.Duration) {
	if d2 <= 0 {
		return
	}
	d.pendingLLMOffset += d2
}
