// Package main — s10-code-impl-workflow.
//
// File: workflow_test.go — five integration-style tests that exercise the
// composed workflow end-to-end, but stay hermetic by using a ReplayProvider
// and t.TempDir() for the working directory. No network, no real LLM, no
// shared state across tests.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper — write a JSON plan with a given file list to a path, return the path.
func writePlan(t *testing.T, dir string, files []string) string {
	t.Helper()
	planPath := filepath.Join(dir, "plan.yaml")
	body := map[string]any{"files": files}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := os.WriteFile(planPath, raw, 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	return planPath
}

// helper — build a ChatResponse with one write_file tool_use call.
func writeFileCall(id, file, content string) ChatResponse {
	args, _ := json.Marshal(map[string]string{"file_path": file, "content": content})
	return ChatResponse{
		FinishReason: FinishToolCalls,
		ToolCalls: []ToolCallRequest{{
			ID:   id,
			Name: "write_file",
			Args: args,
		}},
	}
}

// helper — build a final-text ChatResponse.
func finalText(text string) ChatResponse {
	return ChatResponse{
		FinishReason: FinishStop,
		Content:      []ContentBlock{{Type: "text", Text: text}},
	}
}

// Test 1 — happy path: plan with 3 files, replay produces (write_file, final)
// for each → all 3 files appear under taskDir/generate_code/, status=="completed".
func TestWorkflow_HappyPathThreeFiles(t *testing.T) {
	taskDir := t.TempDir()
	planPath := writePlan(t, taskDir, []string{"main.go", "config.go", "handler.go"})

	provider := &ReplayProvider{Responses: []ChatResponse{
		writeFileCall("c1", "main.go", "package main\n"),
		finalText("wrote main.go"),
		writeFileCall("c2", "config.go", "package main\n"),
		finalText("wrote config.go"),
		writeFileCall("c3", "handler.go", "package main\n"),
		finalText("wrote handler.go"),
	}}

	wf := &Workflow{Provider: provider, Model: "fake", MaxIterations: 8}
	report, err := wf.Run(context.Background(), planPath, taskDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StatusCompleted {
		t.Fatalf("status=%q reason=%q want completed", report.Status, report.Reason)
	}
	if report.FilesCompleted != 3 || report.Total != 3 {
		t.Fatalf("files=%d/%d want 3/3", report.FilesCompleted, report.Total)
	}

	codeDir := filepath.Join(taskDir, "generate_code")
	for _, f := range []string{"main.go", "config.go", "handler.go"} {
		full := filepath.Join(codeDir, f)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
}

// Test 2 — loop detector aborts when the replay repeats the same tool 5+ times.
func TestWorkflow_AbortedByLoopDetector(t *testing.T) {
	taskDir := t.TempDir()
	planPath := writePlan(t, taskDir, []string{"main.go"})

	// Build six identical tool_use calls — should trip MaxRepeats=5 on the
	// fifth call inside the same Runner.Run.
	responses := make([]ChatResponse, 0, 6)
	for i := 0; i < 6; i++ {
		// Use a NON-write_file name so RecordSuccess doesn't reset the
		// counter; loop detector tracks repeated names regardless of
		// whether they progressed.
		args, _ := json.Marshal(map[string]string{"file_path": "any.go"})
		responses = append(responses, ChatResponse{
			FinishReason: FinishToolCalls,
			ToolCalls: []ToolCallRequest{{
				ID:   "call_x",
				Name: "read_file",
				Args: args,
			}},
		})
	}
	provider := &ReplayProvider{Responses: responses}
	wf := &Workflow{Provider: provider, Model: "fake", MaxIterations: 16}

	report, err := wf.Run(context.Background(), planPath, taskDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StatusAborted {
		t.Fatalf("status=%q reason=%q want aborted", report.Status, report.Reason)
	}
	if !strings.Contains(report.Reason, "loop") {
		t.Errorf("reason=%q expected to mention loop", report.Reason)
	}
}

// countingTokenizer is a Tokenizer mock that increments a counter every
// time CountTokens is called. The workflow calls MessagesTokens once per
// Compact invocation, so any non-zero call count proves the configured
// tokenizer was actually exercised.
type countingTokenizer struct {
	calls int
}

func (c *countingTokenizer) CountTokens(s string) int {
	c.calls++
	return len(s) / 4
}

// Test 3 — verify Compact is invoked exactly once per write_file by
// counting OnCompact firings against the number of write_file events in
// the replay. A counting Tokenizer plugged into MemoryTokenizer must
// also see at least one CountTokens call (the workflow exercises it via
// MessagesTokens after every Compact).
func TestWorkflow_MemoryCompactionInvokedPerWriteFile(t *testing.T) {
	taskDir := t.TempDir()
	planPath := writePlan(t, taskDir, []string{"a.go", "b.go", "c.go"})

	provider := &ReplayProvider{Responses: []ChatResponse{
		writeFileCall("c1", "a.go", "package main\n"),
		finalText("wrote a"),
		writeFileCall("c2", "b.go", "package main\n"),
		finalText("wrote b"),
		writeFileCall("c3", "c.go", "package main\n"),
		finalText("wrote c"),
	}}
	expectedWriteFiles := writeFileCallCount(provider.Responses)

	tok := &countingTokenizer{}
	compactCalls := 0
	wf := &Workflow{
		Provider:        provider,
		Model:           "fake",
		MaxIterations:   8,
		MemoryTokenizer: tok,
		OnCompact:       func() { compactCalls++ },
	}
	report, err := wf.Run(context.Background(), planPath, taskDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StatusCompleted {
		t.Fatalf("status=%q want completed", report.Status)
	}
	if compactCalls != expectedWriteFiles {
		t.Errorf("OnCompact fired %d times, expected %d (one per write_file)", compactCalls, expectedWriteFiles)
	}
	if compactCalls != 3 {
		t.Errorf("want 3 Compact invocations, got %d", compactCalls)
	}
	if tok.calls == 0 {
		t.Error("counting tokenizer never invoked; workflow did not exercise MemoryTokenizer")
	}
}

// writeFileCallCount counts how many ChatResponses in rs carry a
// write_file tool_use call. Used by Test 3 as the expected Compact count.
func writeFileCallCount(rs []ChatResponse) int {
	n := 0
	for _, r := range rs {
		for _, c := range r.ToolCalls {
			if c.Name == "write_file" {
				n++
			}
		}
	}
	return n
}

// Test 4 — JSONL attempt log has one line per file with non-empty timestamps.
func TestWorkflow_JSONLAttemptLogOnePerFile(t *testing.T) {
	taskDir := t.TempDir()
	planPath := writePlan(t, taskDir, []string{"a.go", "b.go", "c.go"})

	provider := &ReplayProvider{Responses: []ChatResponse{
		writeFileCall("c1", "a.go", "package main\n"),
		finalText("ok"),
		writeFileCall("c2", "b.go", "package main\n"),
		finalText("ok"),
		writeFileCall("c3", "c.go", "package main\n"),
		finalText("ok"),
	}}
	wf := &Workflow{Provider: provider, Model: "fake", MaxIterations: 8}
	if _, err := wf.Run(context.Background(), planPath, taskDir); err != nil {
		t.Fatalf("Run: %v", err)
	}

	logPath := filepath.Join(taskDir, "implementation_attempts.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("attempts log = %d lines, want 3 — got:\n%s", len(lines), string(raw))
	}
	for i, ln := range lines {
		var rec fileAttempt
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatalf("line %d unmarshal: %v\nline=%q", i, err, ln)
		}
		if rec.File == "" {
			t.Errorf("line %d: empty File", i)
		}
		if rec.Timestamp.IsZero() {
			t.Errorf("line %d: zero Timestamp", i)
		}
		if rec.StopReason == "" {
			t.Errorf("line %d: empty StopReason", i)
		}
	}
}

// Test 5 — resume from checkpoint: write 1 file ahead of time, run workflow
// with same plan; only the missing 2 files are attempted.
func TestWorkflow_ResumeFromCheckpoint(t *testing.T) {
	taskDir := t.TempDir()
	planPath := writePlan(t, taskDir, []string{"a.go", "b.go", "c.go"})

	// Pre-create a.go so the workflow should skip it.
	codeDir := filepath.Join(taskDir, "generate_code")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codeDir, "a.go"), []byte("// pre-existing\n"), 0o644); err != nil {
		t.Fatalf("pre-write a.go: %v", err)
	}

	// Replay only enough responses for two files (b.go, c.go).
	provider := &ReplayProvider{Responses: []ChatResponse{
		writeFileCall("c1", "b.go", "package main\n"),
		finalText("wrote b"),
		writeFileCall("c2", "c.go", "package main\n"),
		finalText("wrote c"),
	}}
	wf := &Workflow{Provider: provider, Model: "fake", MaxIterations: 8}
	report, err := wf.Run(context.Background(), planPath, taskDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Status != StatusCompleted {
		t.Fatalf("status=%q reason=%q want completed", report.Status, report.Reason)
	}
	if report.FilesCompleted != 3 {
		t.Errorf("FilesCompleted=%d want 3 (one resumed, two new)", report.FilesCompleted)
	}

	// We only queued 4 responses (2 per skipped file). If the workflow
	// had attempted a.go again it would have run out of responses and
	// surfaced a StopError, so completion proves resume worked.
	if provider.Calls() != 4 {
		t.Errorf("provider.Calls()=%d want 4 — workflow may have re-attempted resumed file", provider.Calls())
	}

	// Pre-existing content survived (workflow must not overwrite).
	pre, err := os.ReadFile(filepath.Join(codeDir, "a.go"))
	if err != nil {
		t.Fatalf("read a.go: %v", err)
	}
	if !strings.Contains(string(pre), "pre-existing") {
		t.Errorf("a.go was overwritten; got %q", string(pre))
	}
}
