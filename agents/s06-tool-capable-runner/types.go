// Package main — s06-tool-capable-runner.
//
// File: types.go — minimal redeclarations of the Provider / Tool interfaces
// and the canonical value types the runner needs (Message, ContentBlock,
// ToolCallRequest, ChatRequest, ChatResponse, Usage).
//
// Session-isolation rule: s06 does NOT import from s02 / s04. Each chapter
// has its own go.mod and redeclares the minimal subset it needs from the
// shared catalog in .learn/plan.md. This keeps every chapter independently
// runnable and stops a curious reader from having to walk the dependency
// graph just to read the runner.
//
// The shapes here are byte-for-byte compatible with s02's Tool/ToolSchema
// and s04's Provider/ChatRequest/ChatResponse — copy a Tool over and it
// satisfies this interface unchanged.
package main

import (
	"context"
	"encoding/json"
)

// Provider is the LLM abstraction. The runner only ever calls Chat — narrow
// on purpose. Streaming, retry, token-counting are out of scope (upstream
// keeps them on the same ABC; we factor them out).
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// Tool is what every callable an agent can invoke must satisfy. Same shape
// as s02's Tool: Name (key), Schema (advertise), Run (execute).
type Tool interface {
	Name() string
	Schema() ToolSchema
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// ChatRequest is the canonical input shared by every backend.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolSchema
	MaxTokens   int
	Temperature float64
}

// ChatResponse is the canonical output. Both Anthropic and OpenAI
// implementations parse their native JSON into this shape; the runner
// decides on FinishReason and ToolCalls.
type ChatResponse struct {
	Content      []ContentBlock
	ToolCalls    []ToolCallRequest
	FinishReason string // FinishStop | FinishToolCalls | FinishLength | FinishError
	Usage        Usage
}

// Message is one chat turn. Role is "user" | "assistant" | "system" | "tool".
type Message struct {
	Role    string
	Content []ContentBlock
}

// ContentBlock is a tagged union. Type selects which fields are populated:
//
//   - "text"        → Text
//   - "tool_use"    → ToolUseID, ToolName, Input
//   - "tool_result" → ToolUseID, Output, IsError
//
// IsError carries upstream's `is_error` flag from a tool_result block: when
// the tool fails the runner still appends a tool_result (so the model can
// see what went wrong) but flags it so downstream code can react.
type ContentBlock struct {
	Type      string
	Text      string
	ToolUseID string
	ToolName  string
	Input     json.RawMessage
	Output    string
	IsError   bool
}

// ToolSchema is what the runner advertises to the model.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolCallRequest is one invocation the model emitted. Args is a JSON
// object as raw bytes — exactly what the tool's Run() expects.
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
// onto exactly one of these — see s04 for the translation tables.
const (
	FinishStop      = "stop"
	FinishToolCalls = "tool_calls"
	FinishLength    = "length"
	FinishError     = "error"
)
