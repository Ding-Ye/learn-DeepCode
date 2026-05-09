// Package main — s10-code-impl-workflow.
//
// File: report.go — RunReport value type returned by Workflow.Run, plus
// the canonical Status taxonomy.
//
// Mirrors upstream's `_last_run_state` dict in
// workflows/code_implementation_workflow.py:71-79, but as a typed struct
// rather than a stringly-keyed dict (research-notes anti-pattern #5).
package main

import "time"

// Status values returned in RunReport.Status. Exactly one is set per run.
const (
	StatusCompleted     = "completed"      // every file in the plan was implemented
	StatusAborted       = "aborted"        // LoopDetector or LLM-error surfaced before finish
	StatusMaxIterations = "max_iterations" // Runner hit its per-file iteration cap
	StatusMaxTime       = "max_time"       // Workflow's wall-clock budget exhausted
	StatusError         = "error"          // setup or I/O failure outside the loop
)

// RunReport summarises what one CodeImplementationWorkflow.Run produced.
// Identical fields (in spirit) to upstream's _last_run_state dict.
type RunReport struct {
	Status             string
	Reason             string
	FilesCompleted     int
	Total              int
	Iterations         int
	Elapsed            time.Duration
	UnimplementedFiles []string
}
