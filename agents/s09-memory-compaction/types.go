// Package main — s09-memory-compaction.
//
// File: types.go — minimal redeclarations of Message and ContentBlock.
//
// Session-isolation rule: s09 does NOT import from s06. The shapes here are
// byte-compatible with s06's `types.go` (same field names, same kinds), but
// each chapter has its own go.mod and redeclares only the subset it needs.
// The reader can read s09 cold without first walking s06.
//
// What s09 needs from this shape:
//   - Role on the message (so we can find the "system" turn at index 0).
//   - Type on the content block ("text" | "tool_use" | "tool_result").
//   - ToolName on tool_use / tool_result blocks (so we can match against
//     the EssentialTools whitelist and find the last write_file boundary).
//   - ToolUseID for pairing tool_use ↔ tool_result blocks (we drop them
//     together — never an orphan).
//   - IsError for tool_result blocks (kept verbatim; s09 does not branch
//     on it but downstream consumers do).
package main

// Message is one chat turn. Role is "user" | "assistant" | "system" | "tool".
// Content holds zero or more blocks; assistant turns may carry text+tool_use,
// user turns either plain text or tool_result blocks.
type Message struct {
	Role    string
	Content []ContentBlock
}

// ContentBlock is a tagged union. Type selects which fields are populated:
//
//   - "text"        → Text
//   - "tool_use"    → ToolUseID, ToolName, Input
//   - "tool_result" → ToolUseID, Output, IsError, ToolName (denormalised
//                     for s09's whitelist match — upstream's tool_result
//                     blocks carry the name on the paired tool_use, but
//                     having it on both ends lets Compact filter without
//                     a second pass).
type ContentBlock struct {
	Type      string
	Text      string
	ToolUseID string
	ToolName  string
	Input     string
	Output    string
	IsError   bool
}
