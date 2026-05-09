// Package main — s07-planning-runtime.
//
// Path layout for the three on-disk planning artifacts. Sessions are
// isolated (separate go.mod) so we redeclare the minimal subset of s05
// we need: just the task_dir-rooted path joins. Callers pass a TaskDir
// in (typically WorkflowContext.TaskDir() from s05); s07 itself never
// constructs one.
//
// The three artifacts intentionally live as siblings under TaskDir:
//
//	<TaskDir>/planning_checkpoint.json     atomic snapshot of latest in-flight state
//	<TaskDir>/planning_attempts.jsonl      append-only log, one JSON per line
//	<TaskDir>/planning_result_meta.json    final outcome metadata
//
// Upstream counterpart: workflows/planning_runtime.py:31-37 (planning_paths).
package main

import "path/filepath"

// Paths bundles the three derived artifact paths for one task directory.
// Returned by value so callers can pass it around without aliasing concerns.
type Paths struct {
	Checkpoint string // <taskDir>/planning_checkpoint.json
	Attempts   string // <taskDir>/planning_attempts.jsonl
	Meta       string // <taskDir>/planning_result_meta.json
}

// PlanningPaths returns the artifact-path bundle for a given task directory.
// Pure function: no filesystem access, no mkdir — the caller is expected to
// ensure taskDir exists before writing into the returned paths.
func PlanningPaths(taskDir string) Paths {
	return Paths{
		Checkpoint: filepath.Join(taskDir, "planning_checkpoint.json"),
		Attempts:   filepath.Join(taskDir, "planning_attempts.jsonl"),
		Meta:       filepath.Join(taskDir, "planning_result_meta.json"),
	}
}
