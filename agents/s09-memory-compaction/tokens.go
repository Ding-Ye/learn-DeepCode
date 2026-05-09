// Package main — s09-memory-compaction.
//
// File: tokens.go — Tokenizer interface + a byte-length proxy.
//
// Why an interface, not a function: research-notes anti-pattern #9 calls out
// upstream's "implicit token budgeting (tiktoken fallback to string length)".
// In Go we have no idiomatic tiktoken port, so we ship a documented byte
// proxy as the default and let readers swap in a real BPE library later
// without touching the agent. The Tokenizer ABI is one method:
//
//	CountTokens(s string) int
//
// MessagesTokens walks every Message's Content slice and sums Text + Output
// byte counts via the chosen tokenizer. tool_use blocks contribute their
// Input field (raw JSON bytes); tool_result blocks contribute their Output.
package main

// Tokenizer estimates how many tokens a string occupies. Implementations are
// expected to be deterministic and roughly monotonic (longer strings ≥
// shorter strings). The default ByteLengthTokenizer below satisfies both.
type Tokenizer interface {
	CountTokens(s string) int
}

// ByteLengthTokenizer is a stand-in for a real BPE tokenizer. It returns
// len(s) / 4 — the common rule of thumb for English text under cl100k /
// o200k_base. Bias: code (especially CJK) compresses worse than English,
// so this proxy underestimates real token counts for non-prose payloads.
// Tests assert monotonicity and a ±20% bound vs. len(s)/4 — never an exact
// number, because there is no exact number to assert.
type ByteLengthTokenizer struct{}

// CountTokens implements Tokenizer.
func (ByteLengthTokenizer) CountTokens(s string) int {
	return len(s) / 4
}

// MessagesTokens sums tokens across every block in every message. The cost
// model intentionally ignores Role / Type framing overhead — what we care
// about is the payload size, since that's what dominates real context use.
func MessagesTokens(t Tokenizer, msgs []Message) int {
	if t == nil {
		t = ByteLengthTokenizer{}
	}
	total := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			total += t.CountTokens(b.Text)
			total += t.CountTokens(b.Input)
			total += t.CountTokens(b.Output)
		}
	}
	return total
}
