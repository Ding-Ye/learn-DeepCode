// Package main — s08-loop-detector tests.
//
// All tests use *FakeClock so timing is deterministic — they run in
// microseconds, not the minutes the real thresholds imply. The fake's Now()
// is read by every CheckTool call; Advance(d) steps it forward.
package main

import (
	"testing"
	"time"
)

// epoch is the seed for FakeClock. The absolute value is irrelevant — only
// relative offsets matter — but a stable seed makes test diagnostics
// readable.
var epoch = time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)

// Test 1: five identical tool names in a row → Code=="loop_detected".
func TestCheckTool_LoopDetectedAfterFiveSame(t *testing.T) {
	fc := NewFakeClock(epoch)
	d := NewLoopDetector(WithClock(fc))

	const name = "write_file"
	for i := 1; i <= 4; i++ {
		st := d.CheckTool(name)
		if st.Code != StatusOK {
			t.Fatalf("call #%d: got %q want %q", i, st.Code, StatusOK)
		}
	}
	st := d.CheckTool(name)
	if st.Code != StatusLoopDetected {
		t.Errorf("5th call: got code %q want %q (msg=%s)", st.Code, StatusLoopDetected, st.Message)
	}
	if !st.ShouldStop {
		t.Errorf("5th call: ShouldStop=false, want true")
	}
}

// Test 2: wall-clock advances past Timeout → Code=="timeout".
// We use a short Timeout so a single Advance crosses the boundary.
func TestCheckTool_TimeoutAfterBudgetExceeded(t *testing.T) {
	fc := NewFakeClock(epoch)
	d := NewLoopDetector(
		WithClock(fc),
		WithTimeout(10*time.Second),
		WithStallThreshold(0), // disable stall so it can't shadow the timeout
	)

	// One quick OK call, then advance well past the timeout.
	if st := d.CheckTool("read_file"); st.Code != StatusOK {
		t.Fatalf("baseline call: got %q want %q", st.Code, StatusOK)
	}
	fc.Advance(11 * time.Second)
	st := d.CheckTool("read_file")
	if st.Code != StatusTimeout {
		t.Errorf("after 11s: got code %q want %q (msg=%s)", st.Code, StatusTimeout, st.Message)
	}
	if !st.ShouldStop {
		t.Errorf("ShouldStop=false, want true")
	}
}

// Test 3: stall — idle longer than StallThreshold without any LLM offset.
func TestCheckTool_StallWithoutOffset(t *testing.T) {
	fc := NewFakeClock(epoch)
	d := NewLoopDetector(
		WithClock(fc),
		WithStallThreshold(30*time.Second),
		WithTimeout(0), // disable timeout so stall is the only firing rule
	)

	if st := d.CheckTool("read_file"); st.Code != StatusOK {
		t.Fatalf("baseline call: got %q want %q", st.Code, StatusOK)
	}
	fc.Advance(31 * time.Second)
	st := d.CheckTool("read_file")
	if st.Code != StatusStall {
		t.Errorf("after 31s idle: got code %q want %q (msg=%s)", st.Code, StatusStall, st.Message)
	}
}

// Test 4: same scenario as #3 but with NoteLLMWait(stall+1s) — the offset
// must consume the idle interval so the next CheckTool returns "ok".
// This is the load-bearing test for s08's headline feature.
func TestCheckTool_NoteLLMWaitDefeatsStall(t *testing.T) {
	fc := NewFakeClock(epoch)
	const stall = 30 * time.Second
	d := NewLoopDetector(
		WithClock(fc),
		WithStallThreshold(stall),
		WithTimeout(0),
	)

	if st := d.CheckTool("read_file"); st.Code != StatusOK {
		t.Fatalf("baseline call: got %q want %q", st.Code, StatusOK)
	}
	// Advance past the stall threshold, but tell the detector that all of
	// that time was an LLM wait. After the offset is applied inside
	// CheckTool, the effective idle interval should be (31s - 31s) = 0.
	fc.Advance(31 * time.Second)
	d.NoteLLMWait(stall + time.Second) // 31s — exactly the wall-clock advance
	st := d.CheckTool("read_file")
	if st.Code != StatusOK {
		t.Errorf("with offset: got code %q want %q (msg=%s)", st.Code, StatusOK, st.Message)
	}
	if st.ShouldStop {
		t.Errorf("with offset: ShouldStop=true, want false")
	}
}

// Test 5: MaxErrors RecordError calls in a row → next CheckTool returns
// "max_errors". Verifies the consecutive-error counter is wired.
func TestCheckTool_MaxErrorsAfterRecordErrorBurst(t *testing.T) {
	fc := NewFakeClock(epoch)
	d := NewLoopDetector(
		WithClock(fc),
		WithMaxErrors(3),
		WithStallThreshold(0),
		WithTimeout(0),
	)

	// One harmless call to seed history.
	if st := d.CheckTool("read_file"); st.Code != StatusOK {
		t.Fatalf("baseline call: got %q want %q", st.Code, StatusOK)
	}
	for i := 0; i < 3; i++ {
		d.RecordError()
	}
	st := d.CheckTool("write_file")
	if st.Code != StatusMaxErrors {
		t.Errorf("after 3 RecordError: got code %q want %q (msg=%s)", st.Code, StatusMaxErrors, st.Message)
	}
	if !st.ShouldStop {
		t.Errorf("ShouldStop=false, want true")
	}
}
