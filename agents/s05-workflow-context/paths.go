// Package main — s05-workflow-context.
//
// Derived path methods on WorkflowContext. Every path under the task
// directory should be computed here, not hard-coded by callers — that's
// the whole point of having a context object instead of stringly typed
// dicts threaded through the pipeline.
//
// All methods use filepath.Join for cross-platform safety (Windows uses
// backslashes; macOS/Linux use forward slashes; never concatenate with
// raw "/").
//
// Upstream counterpart: workflows/workflow_context.py:108-126
// (the @property block on WorkflowContext).
package main

import "path/filepath"

// ReferencePath is where the reference paper / extracted text lives.
// Upstream uses .txt; we use .md because Go's downstream tooling treats
// markdown as a richer format and the rest of learn-DeepCode is markdown.
func (c WorkflowContext) ReferencePath() string {
	return filepath.Join(c.taskDir, "reference.md")
}

// InitialPlanPath holds the planning runtime's structured output (s07).
// The five required sections live in this file.
func (c WorkflowContext) InitialPlanPath() string {
	return filepath.Join(c.taskDir, "initial_plan.md")
}

// ImplementationReportPath holds the final code-impl summary (s10).
func (c WorkflowContext) ImplementationReportPath() string {
	return filepath.Join(c.taskDir, "implementation_report.md")
}

// LogsDir holds JSONL attempt logs (planning attempts, per-file logs).
func (c WorkflowContext) LogsDir() string {
	return filepath.Join(c.taskDir, "logs")
}

// GenerateCodeDir is the root of generated source files (one tree per task).
// s10 writes file-by-file under here.
func (c WorkflowContext) GenerateCodeDir() string {
	return filepath.Join(c.taskDir, "generate_code")
}
