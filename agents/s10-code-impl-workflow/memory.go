// Package main — s10-code-impl-workflow.
//
// File: memory.go — minimal MemoryAgent.Compact (clean-slate strategy).
// Mirrors s09's algorithm: keep [system, synthetic-plan, ...messages from
// last write_file boundary] with non-essential tool blocks dropped.
//
// Why redeclare instead of import s09? Session-isolation rule. Each
// chapter's go.mod is a separate module. Testing-wise this works in our
// favor: a tokenizer mock can be plugged into MemoryAgent.Tokenizer
// directly without dragging in s09's interface from another module.
package main

// EssentialTools is the upstream whitelist verbatim — same eight names
// as s09's essential.go. Any tool whose name is NOT in this map gets its
// tool_use / tool_result blocks dropped during Compact.
var EssentialTools = map[string]bool{
	"read_file":             true,
	"write_file":            true,
	"execute_python":        true,
	"execute_bash":          true,
	"search_code":           true,
	"search_reference_code": true,
	"get_file_structure":    true,
	"read_code_mem":         true,
}

// Tokenizer estimates token cost for budgeting. The default is byte-length
// over 4 (rule of thumb for English). Tests pass a mock to count Compact
// invocations.
type Tokenizer interface {
	CountTokens(s string) int
}

// ByteLengthTokenizer returns len(s)/4. Documented bias: code (CJK in
// particular) under-counts. Good enough for ShouldCompact heuristics.
type ByteLengthTokenizer struct{}

// CountTokens implements Tokenizer.
func (ByteLengthTokenizer) CountTokens(s string) int { return len(s) / 4 }

// MessagesTokens sums tokens across every block in every message. Used by
// the workflow as a single point that exercises the configured tokenizer
// once per Compact invocation — that's how tests count Compact calls
// indirectly via a tokenizer mock.
func MessagesTokens(t Tokenizer, msgs []Message) int {
	if t == nil {
		t = ByteLengthTokenizer{}
	}
	total := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			total += t.CountTokens(b.Text)
			total += t.CountTokens(string(b.Input))
			total += t.CountTokens(b.Output)
		}
	}
	return total
}

// MemoryAgent holds the configuration that drives compaction. State-free
// after construction.
type MemoryAgent struct {
	InitialPlan      string
	EssentialTools   map[string]bool
	Tokenizer        Tokenizer
	MaxContextTokens int
	TokenBuffer      int
}

func (a *MemoryAgent) defaults() {
	if a.MaxContextTokens == 0 {
		a.MaxContextTokens = 200000
	}
	if a.TokenBuffer == 0 {
		a.TokenBuffer = 10000
	}
	if a.EssentialTools == nil {
		a.EssentialTools = EssentialTools
	}
	if a.Tokenizer == nil {
		a.Tokenizer = ByteLengthTokenizer{}
	}
}

// Compact returns [system, synthetic-plan, ...kept]. Pure function.
func (a *MemoryAgent) Compact(messages []Message) []Message {
	a.defaults()

	out := make([]Message, 0, len(messages)+2)
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, messages[0])
	}
	out = append(out, Message{
		Role:    "user",
		Content: []ContentBlock{{Type: "text", Text: a.InitialPlan}},
	})

	boundary := findLastWriteFileBoundary(messages)
	if boundary < 0 {
		return out
	}

	dropped := map[string]bool{}
	for i := boundary; i < len(messages); i++ {
		filtered := a.filterMessage(messages[i], dropped)
		if len(filtered.Content) == 0 {
			continue
		}
		out = append(out, filtered)
	}
	return out
}

func findLastWriteFileBoundary(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, b := range messages[i].Content {
			if b.ToolName != "write_file" {
				continue
			}
			if b.Type == "tool_use" {
				return i
			}
			if b.Type == "tool_result" {
				for j := i - 1; j >= 0; j-- {
					for _, bb := range messages[j].Content {
						if bb.Type == "tool_use" && bb.ToolUseID == b.ToolUseID {
							return j
						}
					}
				}
				return i
			}
		}
	}
	return -1
}

func (a *MemoryAgent) filterMessage(m Message, dropped map[string]bool) Message {
	if len(m.Content) == 0 {
		return m
	}
	kept := make([]ContentBlock, 0, len(m.Content))
	for _, b := range m.Content {
		switch b.Type {
		case "tool_use":
			if !a.EssentialTools[b.ToolName] {
				if b.ToolUseID != "" {
					dropped[b.ToolUseID] = true
				}
				continue
			}
			kept = append(kept, b)
		case "tool_result":
			if dropped[b.ToolUseID] {
				continue
			}
			if b.ToolName != "" && !a.EssentialTools[b.ToolName] {
				continue
			}
			kept = append(kept, b)
		default:
			kept = append(kept, b)
		}
	}
	return Message{Role: m.Role, Content: kept}
}
