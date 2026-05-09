// Package main — s09-memory-compaction.
//
// File: agent_test.go — five hermetic tests for Compact, the boundary scan,
// the pairing invariant, and the byte-length tokenizer's monotonicity.
package main

import (
	"strings"
	"testing"
)

// build50 returns a synthetic 50-message conversation with the same shape
// as the JSON fixture: system + initial user, then assistant tool_use /
// user tool_result pairs alternating between essential tools and the
// non-essential web_fetch, with three write_file events scattered through.
func build50() []Message {
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "system prompt"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "initial human request"}}},
	}
	tools := []string{"read_file", "web_fetch", "execute_python", "search_code", "read_file", "web_fetch"}
	writeAt := map[int]bool{8: true, 16: true, 22: true} // pair-indices that get a write_file pair
	for i := 0; i < 24; i++ {
		var name string
		if writeAt[i] {
			name = "write_file"
		} else {
			name = tools[i%len(tools)]
		}
		id := "call-" + itoa(i)
		msgs = append(msgs,
			Message{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolUseID: id, ToolName: name, Input: "{}"}}},
			Message{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: id, ToolName: name, Output: "ok"}}},
		)
	}
	return msgs // 2 + 48 = 50
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestCompact_50MessagesPreservesSystemAndPlan(t *testing.T) {
	agent := &MemoryAgent{InitialPlan: "PLAN BODY"}
	in := build50()
	if len(in) != 50 {
		t.Fatalf("fixture builder broken: got %d messages", len(in))
	}
	out := agent.Compact(in)

	if len(out) < 2 {
		t.Fatalf("result too short: %d messages", len(out))
	}
	if out[0].Role != "system" {
		t.Fatalf("result[0].Role = %q, want system", out[0].Role)
	}
	if out[1].Role != "user" {
		t.Fatalf("result[1].Role = %q, want user (synthetic plan)", out[1].Role)
	}
	if len(out[1].Content) == 0 || out[1].Content[0].Text != "PLAN BODY" {
		t.Fatalf("result[1] content = %+v, want synthetic plan with PLAN BODY", out[1].Content)
	}
	if len(out) >= len(in) {
		t.Fatalf("compaction did not shrink: in=%d out=%d", len(in), len(out))
	}
}

func TestCompact_LastWriteFileBoundaryPreserved(t *testing.T) {
	agent := &MemoryAgent{InitialPlan: "P"}
	in := build50()
	// Last write_file pair sits at the assistant turn index 2 + 22*2 = 46 in build50.
	out := agent.Compact(in)

	// Find the write_file event in the kept window.
	foundWriteFile := false
	for _, m := range out {
		for _, b := range m.Content {
			if b.ToolName == "write_file" {
				foundWriteFile = true
			}
		}
	}
	if !foundWriteFile {
		t.Fatalf("expected last write_file boundary to be preserved in result")
	}

	// The boundary message in the input was an assistant tool_use turn;
	// after compaction, result should hold both halves of the final pair.
	writeUseSeen, writeResultSeen := false, false
	for _, m := range out {
		for _, b := range m.Content {
			if b.ToolName == "write_file" && b.Type == "tool_use" {
				writeUseSeen = true
			}
			if b.ToolName == "write_file" && b.Type == "tool_result" {
				writeResultSeen = true
			}
		}
	}
	if !writeUseSeen || !writeResultSeen {
		t.Fatalf("expected both halves of last write_file pair, got use=%v result=%v", writeUseSeen, writeResultSeen)
	}
}

func TestCompact_NonEssentialToolsDropped(t *testing.T) {
	// Construct a conversation: system, plan, assistant tool_use(web_fetch),
	// user tool_result(web_fetch), assistant write_file, user write_file result.
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "sys"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "go"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolUseID: "wf-1", ToolName: "web_fetch", Input: "{}"}}},
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "wf-1", ToolName: "web_fetch", Output: "html"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolUseID: "wr-1", ToolName: "write_file", Input: "{}"}}},
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "wr-1", ToolName: "write_file", Output: "wrote"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolUseID: "wf-2", ToolName: "web_fetch", Input: "{}"}}},
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "wf-2", ToolName: "web_fetch", Output: "html2"}}},
	}
	agent := &MemoryAgent{InitialPlan: "P"}
	out := agent.Compact(msgs)

	for _, m := range out {
		for _, b := range m.Content {
			if b.ToolName == "web_fetch" {
				t.Fatalf("web_fetch survived compaction: %+v", b)
			}
		}
	}
}

func TestCompact_ToolPairingInvariant(t *testing.T) {
	agent := &MemoryAgent{InitialPlan: "P"}
	out := agent.Compact(build50())

	uses := map[string]bool{}
	results := map[string]bool{}
	for _, m := range out {
		for _, b := range m.Content {
			if b.Type == "tool_use" && b.ToolUseID != "" {
				uses[b.ToolUseID] = true
			}
			if b.Type == "tool_result" && b.ToolUseID != "" {
				results[b.ToolUseID] = true
			}
		}
	}
	for id := range uses {
		if !results[id] {
			t.Fatalf("orphan tool_use without matching tool_result: %s", id)
		}
	}
	for id := range results {
		if !uses[id] {
			t.Fatalf("orphan tool_result without matching tool_use: %s", id)
		}
	}
}

func TestTokenizer_MonotonicAndBounded(t *testing.T) {
	tz := ByteLengthTokenizer{}

	cases := []string{
		"",
		"hi",
		"hello world",
		strings.Repeat("a", 100),
		strings.Repeat("longer string ", 50),
	}
	prev := -1
	for _, s := range cases {
		got := tz.CountTokens(s)
		if got < prev {
			t.Fatalf("monotonicity violated: count(%q)=%d < prev=%d", s, got, prev)
		}
		prev = got
		// bound: within ±20% of len(s)/4. ByteLengthTokenizer returns
		// exactly len(s)/4, so this is trivially satisfied — but we
		// keep the assertion so a future swap-in BPE backend is held
		// to the same accuracy contract.
		want := len(s) / 4
		minOK := want - want/5 // -20%
		maxOK := want + want/5 // +20%
		if got < minOK || got > maxOK {
			t.Fatalf("token count for %q = %d, want within ±20%% of %d (range [%d,%d])", s, got, want, minOK, maxOK)
		}
	}
}
