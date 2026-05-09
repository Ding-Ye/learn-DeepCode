// Package main — s10-code-impl-workflow.
//
// File: loop_detector.go — minimal loop-detector that mirrors s08's Status
// surface. Just enough to drive the workflow's pre-tool gate: the runner
// calls CheckTool(name) before dispatching every tool, and any non-OK
// status aborts the per-file body with StopReason="aborted".
//
// Compared to s08 we drop:
//   - the Clock interface (real time.Now is fine for s10 — tests use
//     replay providers that exit before any real timer can fire)
//   - functional Option helpers (s10 builds the detector inline)
//   - RecordError / RecordSuccess accounting beyond the consecutive-error
//     counter; the workflow drives them directly through this struct's
//     exported methods.
//
// The 5 status codes (loop_detected | timeout | stall | max_errors | ok)
// match s08 verbatim — log lines from s08 and s10 pipelines compare cleanly.
package main

import (
	"fmt"
	"time"
)

// Status codes — match s08 verbatim.
const (
	StatusOK           = "ok"
	StatusLoopDetected = "loop_detected"
	StatusTimeout      = "timeout"
	StatusStall        = "stall"
	StatusMaxErrors    = "max_errors"
)

// Status is the typed return value from CheckTool.
type Status struct {
	Code       string
	Message    string
	ShouldStop bool
}

// LoopDetector watches the agent loop for runaway behaviour. Construct via
// NewLoopDetector — the zero value's startedAt would be the Unix epoch and
// would trip the timeout check on the first tool call.
type LoopDetector struct {
	MaxRepeats     int
	Timeout        time.Duration
	StallThreshold time.Duration
	MaxErrors      int

	history           []string
	lastProgressAt    time.Time
	consecutiveErrors int
	startedAt         time.Time
}

// NewLoopDetector seeds the timestamps from the real wall clock. Defaults
// match s08 (and upstream): 5 repeats, 600s timeout, 300s stall, 10 errors.
func NewLoopDetector() *LoopDetector {
	now := time.Now()
	return &LoopDetector{
		MaxRepeats:     5,
		Timeout:        600 * time.Second,
		StallThreshold: 300 * time.Second,
		MaxErrors:      10,
		history:        make([]string, 0, 10),
		lastProgressAt: now,
		startedAt:      now,
	}
}

// CheckTool is invoked before dispatching tool `name`. Order of checks
// (loop → timeout → stall → max_errors) matches s08.
func (d *LoopDetector) CheckTool(name string) Status {
	now := time.Now()

	d.history = append(d.history, name)
	if len(d.history) > 10 {
		d.history = d.history[len(d.history)-10:]
	}

	// Loop detection.
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

	// Timeout.
	if d.Timeout > 0 && now.Sub(d.startedAt) > d.Timeout {
		return Status{
			Code:       StatusTimeout,
			Message:    fmt.Sprintf("timeout: elapsed %s exceeded budget %s", now.Sub(d.startedAt).Round(time.Second), d.Timeout),
			ShouldStop: true,
		}
	}

	// Stall.
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

	// Too many consecutive errors.
	if d.MaxErrors > 0 && d.consecutiveErrors >= d.MaxErrors {
		return Status{
			Code:       StatusMaxErrors,
			Message:    fmt.Sprintf("max errors: %d consecutive errors without progress", d.consecutiveErrors),
			ShouldStop: true,
		}
	}

	return Status{Code: StatusOK, Message: "processing normally", ShouldStop: false}
}

// RecordSuccess marks a real progress signal (typically a successful
// write_file). Resets the consecutive-error counter and bumps the stall
// anchor to "now" so the stall window restarts.
func (d *LoopDetector) RecordSuccess() {
	d.consecutiveErrors = 0
	d.lastProgressAt = time.Now()
}

// RecordError increments the consecutive-error counter. Does NOT reset
// timestamps — wall-clock keeps ticking.
func (d *LoopDetector) RecordError() {
	d.consecutiveErrors++
}
