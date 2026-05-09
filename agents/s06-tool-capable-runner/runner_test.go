// Package main — s06-tool-capable-runner.
//
// File: runner_test.go — five offline tests covering the loop's
// happy / multi-round / cap / truncation / tool-error paths. All use
// ReplayProvider so no network or API key is required.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeTool returns a fixed string from Run. The pre-set Err is returned
// (instead of the string) when non-nil. RunCalls records dispatch counts.
type fakeTool struct {
	NameVal  string
	Out      string
	Err      error
	RunCalls int
}

func (f *fakeTool) Name() string { return f.NameVal }
func (f *fakeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        f.NameVal,
		Description: "test tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}
func (f *fakeTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	f.RunCalls++
	if f.Err != nil {
		return "", f.Err
	}
	return f.Out, nil
}

// TestRun_OneRound — provider returns final text immediately, no tool
// dispatch, StopReason=StopDone.
func TestRun_OneRound(t *testing.T) {
	p := &ReplayProvider{Responses: []ChatResponse{{
		FinishReason: FinishStop,
		Content:      []ContentBlock{{Type: "text", Text: "hello"}},
	}}}

	res, err := NewRunner(p).Run(context.Background(), RunSpec{
		InitialMessages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
		},
		MaxIterations: 5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StopReason != StopDone {
		t.Fatalf("StopReason = %q, want %q", res.StopReason, StopDone)
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1", res.Iterations)
	}
	if len(res.FinalMessage.Content) != 1 || res.FinalMessage.Content[0].Text != "hello" {
		t.Fatalf("FinalMessage = %+v", res.FinalMessage)
	}
	if p.Calls() != 1 {
		t.Fatalf("provider called %d times, want 1", p.Calls())
	}
}

// TestRun_TwoRound — provider returns tool_use then text. Tool runs exactly
// once, FinalMessage is the second-round text.
func TestRun_TwoRound(t *testing.T) {
	tool := &fakeTool{NameVal: "echo", Out: "echoed"}
	reg := NewRegistry()
	reg.Register(tool)

	p := &ReplayProvider{Responses: []ChatResponse{
		{
			FinishReason: FinishToolCalls,
			ToolCalls: []ToolCallRequest{{
				ID:   "tc_1",
				Name: "echo",
				Args: json.RawMessage(`{"text":"x"}`),
			}},
		},
		{
			FinishReason: FinishStop,
			Content:      []ContentBlock{{Type: "text", Text: "all done"}},
		},
	}}

	res, err := NewRunner(p).Run(context.Background(), RunSpec{
		InitialMessages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "do it"}}},
		},
		Tools:         reg,
		MaxIterations: 5,
		MaxToolBytes:  100,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StopReason != StopDone {
		t.Fatalf("StopReason = %q, want %q", res.StopReason, StopDone)
	}
	if tool.RunCalls != 1 {
		t.Fatalf("tool.Run called %d times, want 1", tool.RunCalls)
	}
	if res.Iterations != 2 {
		t.Fatalf("Iterations = %d, want 2", res.Iterations)
	}
	if len(res.FinalMessage.Content) != 1 || res.FinalMessage.Content[0].Text != "all done" {
		t.Fatalf("FinalMessage = %+v", res.FinalMessage)
	}
	// Sanity: transcript should now hold initial user + assistant tool_use
	// + user tool_result + final assistant text = 4 entries.
	if got, want := len(res.AllMessages), 4; got != want {
		t.Fatalf("len(AllMessages) = %d, want %d", got, want)
	}
}

// TestRun_MaxIterations — MaxIterations=1, provider keeps returning
// tool_use, runner returns the synthetic apology.
func TestRun_MaxIterations(t *testing.T) {
	tool := &fakeTool{NameVal: "echo", Out: "ok"}
	reg := NewRegistry()
	reg.Register(tool)

	p := &ReplayProvider{Responses: []ChatResponse{{
		FinishReason: FinishToolCalls,
		ToolCalls: []ToolCallRequest{{
			ID: "tc_1", Name: "echo", Args: json.RawMessage(`{}`),
		}},
	}}}

	res, err := NewRunner(p).Run(context.Background(), RunSpec{
		InitialMessages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "go"}}},
		},
		Tools:         reg,
		MaxIterations: 1,
		MaxToolBytes:  100,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StopReason != StopMaxIterations {
		t.Fatalf("StopReason = %q, want %q", res.StopReason, StopMaxIterations)
	}
	if res.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1", res.Iterations)
	}
	if len(res.FinalMessage.Content) == 0 {
		t.Fatalf("FinalMessage missing content: %+v", res.FinalMessage)
	}
	apology := res.FinalMessage.Content[0].Text
	if !strings.Contains(apology, "maximum number of tool call iterations") {
		t.Fatalf("FinalMessage text = %q, want apology template", apology)
	}
	if !strings.Contains(apology, "(1)") {
		t.Fatalf("FinalMessage text %q should mention the integer max-iterations value", apology)
	}
}

// TestRun_Truncation — tool returns 5000-byte string, MaxToolBytes=200, the
// resulting tool_result block carries the truncation marker and total
// length is exactly 200.
func TestRun_Truncation(t *testing.T) {
	bigOut := strings.Repeat("A", 5000)
	tool := &fakeTool{NameVal: "big", Out: bigOut}
	reg := NewRegistry()
	reg.Register(tool)

	p := &ReplayProvider{Responses: []ChatResponse{
		{
			FinishReason: FinishToolCalls,
			ToolCalls: []ToolCallRequest{{
				ID: "tc_1", Name: "big", Args: json.RawMessage(`{}`),
			}},
		},
		{
			FinishReason: FinishStop,
			Content:      []ContentBlock{{Type: "text", Text: "thanks"}},
		},
	}}

	res, err := NewRunner(p).Run(context.Background(), RunSpec{
		InitialMessages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "fetch"}}},
		},
		Tools:         reg,
		MaxIterations: 5,
		MaxToolBytes:  200,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Find the tool_result block in the transcript.
	var found *ContentBlock
	for _, m := range res.AllMessages {
		for i := range m.Content {
			if m.Content[i].Type == "tool_result" {
				found = &m.Content[i]
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		t.Fatalf("no tool_result block in transcript")
	}
	if !strings.Contains(found.Output, truncationMarker) {
		t.Fatalf("tool_result output missing marker %q: %q", truncationMarker, found.Output)
	}
	if got := len(found.Output); got != 200 {
		t.Fatalf("len(tool_result output) = %d, want 200", got)
	}
}

// TestRun_ToolError — tool.Run returns an error. The loop appends a
// tool_result with IsError=true and continues to the next round (does NOT
// panic, does NOT abort).
func TestRun_ToolError(t *testing.T) {
	tool := &fakeTool{NameVal: "boom", Err: errors.New("kaboom")}
	reg := NewRegistry()
	reg.Register(tool)

	p := &ReplayProvider{Responses: []ChatResponse{
		{
			FinishReason: FinishToolCalls,
			ToolCalls: []ToolCallRequest{{
				ID: "tc_1", Name: "boom", Args: json.RawMessage(`{}`),
			}},
		},
		{
			FinishReason: FinishStop,
			Content:      []ContentBlock{{Type: "text", Text: "I see, recovered"}},
		},
	}}

	res, err := NewRunner(p).Run(context.Background(), RunSpec{
		InitialMessages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "go"}}},
		},
		Tools:         reg,
		MaxIterations: 5,
		MaxToolBytes:  100,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.StopReason != StopDone {
		t.Fatalf("StopReason = %q, want %q (loop should continue past tool error)", res.StopReason, StopDone)
	}
	if tool.RunCalls != 1 {
		t.Fatalf("tool.Run called %d times, want 1", tool.RunCalls)
	}

	// The first tool_result block should have IsError=true.
	var tr *ContentBlock
	for _, m := range res.AllMessages {
		for i := range m.Content {
			if m.Content[i].Type == "tool_result" {
				tr = &m.Content[i]
				break
			}
		}
		if tr != nil {
			break
		}
	}
	if tr == nil {
		t.Fatalf("no tool_result block in transcript")
	}
	if !tr.IsError {
		t.Fatalf("tool_result.IsError = false, want true (output=%q)", tr.Output)
	}
	if !strings.Contains(tr.Output, "kaboom") {
		t.Fatalf("tool_result.Output = %q, should include the underlying error", tr.Output)
	}
	if p.Calls() != 2 {
		t.Fatalf("provider called %d times, want 2 (loop should continue past tool error)", p.Calls())
	}
}
