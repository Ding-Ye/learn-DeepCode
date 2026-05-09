// File: runtime.go — PlanningRuntime orchestrator.
//
// PlanningRuntime is the small public surface that downstream sessions
// (notably s10) consume. It knows how to:
//
//   - record one planning attempt as a JSONL line (RecordAttempt)
//   - persist the latest in-flight runner state atomically (WriteCheckpoint)
//   - finalize a successful plan with metadata (WriteMeta)
//   - decide whether a previously written plan can be reused (IsExistingPlanUsable)
//
// The orchestrator deliberately holds no I/O state — every method receives
// the task directory it should write under. That makes the type cheap to
// construct, free of background goroutines, and safe to share between
// goroutines (the file-locking lives in jsonl.go).
//
// Upstream counterpart: workflows/planning_runtime.py:74-117 + 240-263.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Attempt is one row in planning_attempts.jsonl. Lowercase JSON keys match
// upstream's snake_case so a Python tool reading the same file works.
type Attempt struct {
	At        time.Time `json:"at"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	PlanBytes int       `json:"plan_bytes"`
}

// Meta is the final planning_result_meta.json shape. Status is "success"
// or "failed"; UpdatedAt is set automatically on every WriteMeta call.
type Meta struct {
	Status         string    `json:"status"`
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	PlanChars      int       `json:"plan_chars"`
	MissingSection []string  `json:"missing_sections,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Checkpoint is the in-flight state snapshot. The shape is intentionally
// loose (a free-form payload map) because what's "in flight" depends on
// the runner — s10's per-file state will not match s07's bare planner.
type Checkpoint struct {
	Phase     string         `json:"phase"`
	Attempt   int            `json:"attempt"`
	Mode      string         `json:"mode"`
	Payload   map[string]any `json:"payload,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// PlanningRuntime is the orchestrator. Zero-value is usable; no New func
// is needed. The Now field exists only for tests that want deterministic
// timestamps; production callers leave it nil and let time.Now() run.
type PlanningRuntime struct {
	Now func() time.Time // optional injectable clock for tests
}

func (r *PlanningRuntime) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

// RecordAttempt appends one Attempt row to <taskDir>/planning_attempts.jsonl.
// If a.At is zero it is set to now. Safe to call concurrently — append is
// serialized by jsonl.go's per-path mutex.
func (r *PlanningRuntime) RecordAttempt(ctx context.Context, taskDir string, a Attempt) error {
	if a.At.IsZero() {
		a.At = r.now()
	}
	return AppendJSONL(ctx, PlanningPaths(taskDir).Attempts, a)
}

// WriteCheckpoint atomically writes c to <taskDir>/planning_checkpoint.json.
// UpdatedAt is overwritten with now(). The atomic write means any concurrent
// reader either sees the previous checkpoint or the new one — never half.
func (r *PlanningRuntime) WriteCheckpoint(ctx context.Context, taskDir string, c Checkpoint) error {
	c.UpdatedAt = r.now()
	return AtomicWriteJSON(ctx, PlanningPaths(taskDir).Checkpoint, c)
}

// WriteMeta atomically writes m to <taskDir>/planning_result_meta.json.
// UpdatedAt is overwritten with now().
func (r *PlanningRuntime) WriteMeta(ctx context.Context, taskDir string, m Meta) error {
	m.UpdatedAt = r.now()
	return AtomicWriteJSON(ctx, PlanningPaths(taskDir).Meta, m)
}

// IsExistingPlanUsable returns true iff:
//   - planning_result_meta.json exists, parses, and has Status == "success"
//   - planning_checkpoint.json exists and parses (we don't inspect content)
//
// This is the cheap "can we resume?" probe that s10 runs at workflow start
// before paying for a fresh planning LLM call. Mirrors upstream's
// is_existing_plan_usable() but without the initial_plan.txt re-validation
// (that one stays in s10 where it belongs).
func (r *PlanningRuntime) IsExistingPlanUsable(taskDir string) bool {
	p := PlanningPaths(taskDir)

	metaBytes, err := os.ReadFile(p.Meta)
	if err != nil {
		return false
	}
	var meta Meta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return false
	}
	if meta.Status != "success" {
		return false
	}

	if _, err := os.Stat(p.Checkpoint); err != nil {
		return false
	}
	return true
}

// String is here so a *PlanningRuntime renders cleanly in test failure
// messages — without it the default %v output is just "&{<nil>}".
func (r *PlanningRuntime) String() string {
	return fmt.Sprintf("PlanningRuntime{custom_clock=%t}", r.Now != nil)
}
