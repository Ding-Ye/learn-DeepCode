// Package main — s09-memory-compaction.
//
// File: agent.go — the MemoryAgent value type and its core method, Compact.
//
// Compaction is a pure function of the input message slice:
//
//	Compact(msgs) → msgs'
//
// No I/O, no goroutines, no side effects on the agent. This is deliberate —
// it makes the function trivially testable and makes s10's per-file workflow
// loop a single line ("messages = agent.Compact(messages)").
//
// Algorithm in plain English:
//
//  1. Always keep messages[0] when it is the system prompt — it carries
//     the role rules the model was trained against and dropping it makes
//     every later turn drift.
//  2. Always synthesise a fresh user message containing InitialPlan. Why
//     synthesise instead of preserve? Because the original "here is your
//     plan" turn could have been compressed, mutated, or interleaved with
//     tool calls; a clean re-emit guarantees the model sees the canonical
//     plan text on every compaction.
//  3. Walk the input from the end backwards looking for the boundary that
//     marks the *current round*. The boundary is the message holding the
//     most recent write_file event — either an assistant tool_use whose
//     ToolName == "write_file", or a user tool_result whose ToolName ==
//     "write_file". Keep every message at and after that boundary.
//  4. Inside the kept window, drop any tool_use / tool_result block whose
//     ToolName is NOT in EssentialTools. Pairing is preserved: dropping a
//     tool_use also drops the matching tool_result on the next user turn,
//     and vice versa. Plain text blocks are always preserved.
//
// What we do NOT do (deliberately):
//   - Token-budget the kept window. ShouldCompact decides whether to call
//     Compact at all; once we're inside Compact we trust that the kept
//     window is small enough. Adding a second budget pass here would
//     duplicate the ShouldCompact check and risk dropping the boundary
//     write_file itself (which would break s10's progress tracking).
//   - Preserve any "summary of prior conversation". Upstream's "concise"
//     mode writes one too; s09 explicitly does not, because synthesising
//     a summary requires another LLM call and we want Compact to be pure.
//   - Re-order messages. The result is a contiguous slice in the original
//     order: [system, plan, ...kept].
package main

// MemoryAgent holds the configuration that drives compaction. The struct
// itself carries no mutable state — Compact reads `messages` and returns a
// new slice; the agent fields are read-only after construction.
type MemoryAgent struct {
	// InitialPlan is re-emitted as a synthetic user message at index 1 of
	// every compaction result. Treat it as the "constitution" of the run.
	InitialPlan string

	// EssentialTools is the whitelist of tool names whose tool_use /
	// tool_result blocks survive compaction. If nil, defaults to the
	// package-level EssentialTools map.
	EssentialTools map[string]bool

	// Tokenizer estimates message size. If nil, ByteLengthTokenizer is
	// used. ShouldCompact reads this; Compact itself does not.
	Tokenizer Tokenizer

	// MaxContextTokens is the model's hard context limit. Default 200000
	// matches Claude Sonnet 4 on the upstream config.
	MaxContextTokens int

	// TokenBuffer is reserved headroom for the next response. Default 10000.
	// ShouldCompact triggers when MessagesTokens > MaxContextTokens-TokenBuffer.
	TokenBuffer int
}

// defaults applies the upstream-equivalent constants to a freshly
// zero-valued MemoryAgent. This mirrors `ConciseMemoryAgent.__init__`'s
// `self.max_context_tokens = 200000` / `self.token_buffer = 10000`.
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

// ShouldCompact reports whether the message slice is close enough to the
// model's context limit that Compact should be called before the next turn.
// The threshold is MaxContextTokens - TokenBuffer (i.e. "leave headroom
// for the response we're about to ask for"). Pure function over `messages`.
func (a *MemoryAgent) ShouldCompact(messages []Message) bool {
	a.defaults()
	threshold := a.MaxContextTokens - a.TokenBuffer
	return MessagesTokens(a.Tokenizer, messages) > threshold
}

// Compact returns a new message slice consisting of:
//
//	[system prompt, synthetic plan, ...messages from last write_file boundary]
//
// where every tool_use/tool_result pair inside the kept window has been
// filtered through EssentialTools. The input slice is never mutated.
//
// Edge cases:
//   - If messages is empty: returns [synthetic plan] only (no system to keep).
//   - If messages[0].Role != "system": skips the system slot but still
//     emits the synthetic plan as result[0]. (Tests cover this — it
//     matches upstream's behaviour when the caller didn't prepend a
//     system turn.)
//   - If no write_file boundary is found: every non-system message is
//     dropped. This means a run that hasn't yet generated any file gets
//     compacted down to [system, plan] — which is the desired clean
//     slate per upstream's "before first write_file" branch.
func (a *MemoryAgent) Compact(messages []Message) []Message {
	a.defaults()

	out := make([]Message, 0, len(messages)+2)

	// 1. Preserve the system prompt by convention (role == "system" at
	//    index 0). If the caller didn't put one there, skip silently.
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, messages[0])
	}

	// 2. Always re-emit the initial plan as a synthetic user message.
	//    This is the contract: a compaction result contains the plan
	//    even if the input didn't.
	out = append(out, Message{
		Role: "user",
		Content: []ContentBlock{{
			Type: "text",
			Text: a.InitialPlan,
		}},
	})

	// 3. Find the last write_file boundary. We scan from the end and
	//    stop at the first message that contains any block (tool_use or
	//    tool_result) whose ToolName == "write_file".
	boundary := findLastWriteFileBoundary(messages)
	if boundary < 0 {
		// No write_file ever happened — clean slate is just [system, plan].
		return out
	}

	// 4. Within messages[boundary:], drop tool blocks whose ToolName is
	//    not in EssentialTools. Plain text is preserved as-is. Pairing
	//    is enforced by tracking which ToolUseIDs got dropped on the
	//    tool_use side and dropping the matching tool_result.
	dropped := map[string]bool{}
	for i := boundary; i < len(messages); i++ {
		filtered := a.filterMessage(messages[i], dropped)
		// Drop the message entirely if its content list emptied out —
		// otherwise the model sees a bare {role: user, content: []} that
		// most providers reject.
		if len(filtered.Content) == 0 {
			continue
		}
		out = append(out, filtered)
	}

	return out
}

// findLastWriteFileBoundary returns the index of the *earliest* message
// belonging to the last write_file pair, or -1 if no write_file ever ran.
//
// Walk back from the end. The first write_file block we hit is either the
// tool_use (the assistant's request) or the tool_result (the user's reply
// after dispatch). Either way, we want to keep BOTH halves of the pair
// in the result, so:
//
//   - if we land on a tool_use, that's already the earlier half — return its
//     message index.
//   - if we land on a tool_result, the matching tool_use lives in the
//     immediately preceding message (the assistant's prior turn). Walk
//     back one more step and verify; return that earlier index.
//
// Returning the earlier index means the kept window starts at the
// tool_use, which preserves the pairing invariant downstream filtering
// relies on.
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
				// Look backwards for the paired tool_use. In a
				// well-formed transcript that's i-1, but defend
				// against edge cases (e.g. interleaved text-only
				// assistant turns) by scanning back until we find
				// a matching ToolUseID or run out of messages.
				for j := i - 1; j >= 0; j-- {
					for _, bb := range messages[j].Content {
						if bb.Type == "tool_use" && bb.ToolUseID == b.ToolUseID {
							return j
						}
					}
				}
				// No match found — fall back to keeping just from
				// the tool_result onward (still useful but less
				// complete).
				return i
			}
		}
	}
	return -1
}

// filterMessage returns a copy of m with non-essential tool blocks removed.
// `dropped` is a running set of ToolUseIDs whose tool_use was already
// dropped — when we then encounter the matching tool_result we drop it too,
// preserving the invariant that every tool_use has a matching tool_result
// and vice versa.
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
			// Two reasons to drop a tool_result: (a) its paired tool_use
			// was already dropped — drop both halves together; (b) its
			// ToolName itself isn't essential. Both lead to the same
			// outcome here.
			if dropped[b.ToolUseID] {
				continue
			}
			if b.ToolName != "" && !a.EssentialTools[b.ToolName] {
				continue
			}
			kept = append(kept, b)
		default:
			// "text" and anything unknown — preserve. Upstream's whitelist
			// is about tool clutter, not assistant prose.
			kept = append(kept, b)
		}
	}
	return Message{Role: m.Role, Content: kept}
}
