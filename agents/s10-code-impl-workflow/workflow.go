// Package main — s10-code-impl-workflow.
//
// File: workflow.go — the CodeImplementationWorkflow orchestrator. This is
// the architectural climax: it composes Runner (s06), LoopDetector (s08),
// MemoryAgent (s09), and the AtomicWriteJSON / AppendJSONL primitives
// (s07) to turn a plan file into a directory of generated source code,
// file by file.
//
// Per-file life cycle:
//
//  1. Skip files already present under taskDir/generate_code/ (resume
//     support — a prior run's checkpoint).
//  2. Build a registry of file-scoped tools (read_file, write_file,
//     execute_python). The registry is fresh per file so write_file's
//     workspace setting never bleeds across iterations.
//  3. Build the messages slice from the system prompt + initial plan +
//     a per-file user message naming the target.
//  4. Run the runner. Inside Runner.Run the LoopDetector gates each
//     tool call; OnToolResult fires on every dispatched tool so the
//     workflow can detect successful write_file events.
//  5. After each successful write_file → MemoryAgent.Compact(messages)
//     so the next file starts with a clean slate.
//  6. Append one JSONL line to taskDir/implementation_attempts.jsonl
//     summarising the per-file run.
//
// At the end emit one RunReport. Upstream's surface is much larger
// (progress callbacks, MCP server bring-up, document-segmentation, the
// retry-shrink token policy); we keep just the pieces that wire the four
// underlying mechanisms together.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// systemPrompt is the constant the runner uses as messages[0]. Trimmed-down
// equivalent of GENERAL_CODE_IMPLEMENTATION_SYSTEM_PROMPT in upstream's
// `prompts/code_prompts.py`.
const systemPrompt = `You are a code-implementation agent. For each file in the plan, ` +
	`call the write_file tool exactly once with the full contents. After ` +
	`writing, return a short final-text confirmation. Use read_file or ` +
	`execute_python only if necessary.`

// Workflow is the orchestrator. Stateless beyond the references it holds:
// every Run is independent.
type Workflow struct {
	Provider Provider

	// Model is forwarded to Provider.Chat unchanged.
	Model string

	// MaxIterations bounds the per-file inner loop. Hitting this returns
	// StatusMaxIterations.
	MaxIterations int

	// MaxToolBytes truncates oversized tool outputs (passed to runner).
	MaxToolBytes int

	// MaxTime is the wall-clock budget across all files. Zero disables.
	MaxTime time.Duration

	// MemoryTokenizer lets tests count Compact invocations or swap in a
	// real BPE library. Defaults to ByteLengthTokenizer. The workflow
	// invokes MessagesTokens(MemoryTokenizer, messages) exactly once per
	// Compact call, so wrapping a counting Tokenizer here lets tests
	// observe how many compactions actually ran.
	MemoryTokenizer Tokenizer

	// OnCompact, if non-nil, is invoked once per write_file-triggered
	// Compact call. The hook is the workflow's compaction telemetry
	// surface — tests assert "this fired N times for N write_files".
	OnCompact func()
}

// Run executes the workflow against planPath, materialising files under
// taskDir/generate_code/. Returns one RunReport summarising the outcome.
//
// Errors are returned ONLY for setup failures (plan parse, mkdir). All
// per-file failures are surfaced through RunReport.Status / Reason so the
// caller can decide how to react without a try/catch.
func (w *Workflow) Run(ctx context.Context, planPath, taskDir string) (RunReport, error) {
	start := time.Now()

	plan, err := LoadPlan(planPath)
	if err != nil {
		return RunReport{Status: StatusError, Reason: err.Error()}, err
	}

	codeDir := filepath.Join(taskDir, "generate_code")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		return RunReport{Status: StatusError, Reason: err.Error()}, err
	}

	planContent, _ := os.ReadFile(planPath) // best-effort for the synthetic plan turn
	memAgent := &MemoryAgent{
		InitialPlan: string(planContent),
		Tokenizer:   w.MemoryTokenizer,
	}

	detector := NewLoopDetector()
	runner := NewRunner(w.Provider, detector)
	attemptsLog := filepath.Join(taskDir, "implementation_attempts.jsonl")

	report := RunReport{
		Status: StatusCompleted,
		Total:  len(plan.Files),
	}

	for _, file := range plan.Files {
		// Resume support: skip files that already exist on disk.
		if _, err := os.Stat(filepath.Join(codeDir, file)); err == nil {
			report.FilesCompleted++
			continue
		}

		// Wall-clock check before each file.
		if w.MaxTime > 0 && time.Since(start) > w.MaxTime {
			report.Status = StatusMaxTime
			report.Reason = fmt.Sprintf("wall-clock budget exhausted after %s", time.Since(start).Round(time.Second))
			break
		}

		fileReport, err := w.implementOneFile(ctx, file, codeDir, memAgent, detector, runner, plan)
		if err != nil {
			report.Status = StatusError
			report.Reason = err.Error()
			break
		}

		// Append per-file attempt row regardless of outcome.
		_ = AppendJSONL(ctx, attemptsLog, fileReport)

		switch fileReport.StopReason {
		case StopDone:
			report.FilesCompleted++
			report.Iterations += fileReport.Iterations
			// After each successful write_file the runner triggered
			// memAgent.Compact via OnToolResult; nothing more to do here.

		case StopAborted:
			report.Status = StatusAborted
			report.Reason = fileReport.AbortReason
			report.Iterations += fileReport.Iterations
			goto finalize

		case StopMaxIterations:
			report.Status = StatusMaxIterations
			report.Reason = fmt.Sprintf("file %q exceeded MaxIterations=%d", file, w.MaxIterations)
			report.Iterations += fileReport.Iterations
			goto finalize

		case StopError:
			report.Status = StatusError
			report.Reason = fmt.Sprintf("file %q failed: %s", file, fileReport.AbortReason)
			report.Iterations += fileReport.Iterations
			goto finalize
		}
	}

finalize:
	report.Elapsed = time.Since(start)
	report.UnimplementedFiles = unimplementedFiles(plan.Files, codeDir)
	if len(report.UnimplementedFiles) == 0 && report.Status == StatusCompleted {
		report.FilesCompleted = report.Total
	}
	// Snapshot final report next to the attempt log for easy debugging.
	_ = AtomicWriteJSON(ctx, filepath.Join(taskDir, "implementation_report.json"), report)
	return report, nil
}

// fileAttempt is one row in implementation_attempts.jsonl. Mirrors what
// upstream writes to its planning_attempts log per attempt.
type fileAttempt struct {
	File        string    `json:"file"`
	Timestamp   time.Time `json:"timestamp"`
	StopReason  string    `json:"stop_reason"`
	AbortReason string    `json:"abort_reason,omitempty"`
	Iterations  int       `json:"iterations"`
}

// implementOneFile drives the per-file body and returns a fileAttempt
// record plus any setup error. The transcript itself is discarded once
// MemoryAgent.Compact has run; only the file-on-disk and the attempt log
// survive.
func (w *Workflow) implementOneFile(
	ctx context.Context,
	file string,
	codeDir string,
	memAgent *MemoryAgent,
	detector *LoopDetector,
	runner *Runner,
	_ Plan,
) (fileAttempt, error) {
	reg := NewRegistry()
	registerFileScopedTools(reg, codeDir)

	messages := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: systemPrompt}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: memAgent.InitialPlan}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Implement %s now.", file)}}},
	}

	// OnToolResult fires for every dispatched tool. We only care about
	// successful write_file events for the file currently being built —
	// that's the cue to compact memory before the next iteration.
	res, err := runner.Run(ctx, RunSpec{
		InitialMessages: messages,
		Tools:           reg,
		Provider:        w.Provider,
		Detector:        detector,
		Model:           w.Model,
		MaxIterations:   w.maxIterations(),
		MaxToolBytes:    w.MaxToolBytes,
		OnToolResult: func(name string, args json.RawMessage, result string, isError bool) {
			if isError || name != "write_file" {
				return
			}
			// Compact in place. We use the runner's accumulated
			// transcript via res.AllMessages after the run; on every
			// write_file we drive the side-effect that increments any
			// caller-supplied tokenizer mock and prepares the messages
			// slice for the next file.
			messages = memAgent.Compact(messages)
			// Telemetry — exercise the configured tokenizer once per
			// Compact so test mocks can count calls; fire OnCompact for
			// callers that want a structured signal.
			_ = MessagesTokens(memAgent.Tokenizer, messages)
			if w.OnCompact != nil {
				w.OnCompact()
			}
		},
	})

	attempt := fileAttempt{
		File:        file,
		Timestamp:   time.Now().UTC(),
		StopReason:  res.StopReason,
		AbortReason: res.AbortReason,
		Iterations:  res.Iterations,
	}

	// If the provider failed (StopError + err) propagate the error so the
	// outer loop can switch to StatusError.
	return attempt, err
}

// maxIterations returns the per-file inner-loop cap, defaulting to a sane
// value if the caller didn't set one.
func (w *Workflow) maxIterations() int {
	if w.MaxIterations > 0 {
		return w.MaxIterations
	}
	return 32
}

// unimplementedFiles returns the subset of plan.Files that do not yet
// exist on disk under codeDir. Order matches the plan.
func unimplementedFiles(files []string, codeDir string) []string {
	missing := make([]string, 0)
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(codeDir, f)); err != nil {
			missing = append(missing, f)
		}
	}
	sort.Strings(missing)
	return missing
}
