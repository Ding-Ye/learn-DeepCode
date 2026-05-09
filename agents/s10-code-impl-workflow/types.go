// Package main — s10-code-impl-workflow.
//
// File: types.go — minimal redeclarations of the shared Provider / Tool
// surface s10's workflow needs. s10 is the architectural climax of the
// curriculum, but it still obeys the project's session-isolation rule:
// it does NOT import from s02 / s04 / s06 / s07 / s08 / s09. Every
// chapter has its own go.mod and redeclares the thinnest possible subset
// of types from the canonical catalog in `.learn/plan.md`.
//
// The shapes here are byte-for-byte compatible with s06's runner types —
// copy a Tool over from s06 and it satisfies this interface unchanged.
package main

import (
	"context"
	"encoding/json"
)

// Provider is the LLM abstraction. The workflow only ever calls Chat —
// streaming, retry, and token-counting are out of scope. The provider here
// is identical to s06's: ChatRequest in, ChatResponse out, no leakage of
// Anthropic-vs-OpenAI quirks.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// Tool is a single callable. Same shape as s02 / s06: Name (key), Schema
// (advertise to the model), Run (execute). Run returns a string because
// every tool result becomes a tool_result block carrying string Output.
type Tool interface {
	Name() string
	Schema() ToolSchema
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// ChatRequest is the canonical input shape.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolSchema
	MaxTokens   int
	Temperature float64
}

// ChatResponse is the canonical output shape. Both Anthropic and OpenAI
// implementations would parse their native JSON into this.
type ChatResponse struct {
	Content      []ContentBlock
	ToolCalls    []ToolCallRequest
	FinishReason string // "stop" | "tool_calls" | "length" | "error"
	Usage        Usage
}

// Message is one chat turn.
type Message struct {
	Role    string // "user" | "assistant" | "system"
	Content []ContentBlock
}

// ContentBlock is a tagged union. Type selects which fields are populated:
//
//   - "text"        → Text
//   - "tool_use"    → ToolUseID, ToolName, Input
//   - "tool_result" → ToolUseID, ToolName, Output, IsError
type ContentBlock struct {
	Type      string
	Text      string
	ToolUseID string
	ToolName  string
	Input     json.RawMessage
	Output    string
	IsError   bool
}

// ToolSchema is the per-tool advertisement the runner sends to the model.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolCallRequest is one invocation the model emitted.
type ToolCallRequest struct {
	ID   string
	Name string
	Args json.RawMessage
}

// Usage echoes whatever token accounting the provider returned.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Canonical finish-reason constants. Each provider's native value is mapped
// onto exactly one of these. Identical to s06.
const (
	FinishStop      = "stop"
	FinishToolCalls = "tool_calls"
	FinishLength    = "length"
	FinishError     = "error"
)
