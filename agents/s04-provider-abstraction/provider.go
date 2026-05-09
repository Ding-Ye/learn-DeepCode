// Package main — s04-provider-abstraction.
//
// File: provider.go — the canonical Provider interface plus the value types
// every backend must agree on. This file is the contract; anthropic.go and
// openai.go are two implementations that translate their wire formats into
// these shapes.
//
// Upstream counterpart: core/providers/base.py. Upstream's `LLMProvider` ABC
// carries retry / streaming / image-strip helpers (~600 LOC); s04 stays at the
// minimum surface needed to teach polymorphism: one method, one request type,
// one response type, plus the small set of value types they reference.
package main

import (
	"context"
	"encoding/json"
)

// Provider is the abstraction that lets the agent loop talk to any LLM
// backend. Implementations (AnthropicProvider, OpenAIProvider) hide their
// SDK quirks behind this single method.
//
// Note the context.Context-first signature: cancellation, deadlines, and
// trace IDs all flow through ctx. No package-level state.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// ChatRequest is the canonical input. Both backends translate from this
// shape into their native wire format.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolSchema
	MaxTokens   int
	Temperature float64
}

// ChatResponse is the canonical output. Each backend parses its native body
// (Anthropic content blocks vs. OpenAI choices) and emits this struct.
type ChatResponse struct {
	Content      []ContentBlock
	ToolCalls    []ToolCallRequest
	FinishReason string // see Finish* constants below
	Usage        Usage
}

// Message is one chat turn. Role is "user" | "assistant" | "system" | "tool".
// Content is a slice of typed blocks because tool calls / tool results are
// first-class — a single string field would lose them.
type Message struct {
	Role    string
	Content []ContentBlock
}

// ContentBlock is a tagged union. Type selects which fields are populated:
//
//   - "text"        → Text
//   - "tool_use"    → ToolUseID, ToolName, Input
//   - "tool_result" → ToolUseID, Output
//
// We use one struct with optional fields rather than a Go interface so JSON
// marshalling stays mechanical and tests can compare values with == /
// reflect.DeepEqual without implementing custom matchers.
type ContentBlock struct {
	Type      string
	Text      string
	ToolUseID string
	ToolName  string
	Input     json.RawMessage
	Output    string
}

// ToolSchema is a tool advertised to the model. InputSchema is JSON-Schema
// draft 2020-12, kept as raw bytes so we never re-encode it.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolCallRequest is a tool invocation the model emitted. Args is the raw
// argument object — Anthropic gives us a JSON object, OpenAI gives us a JSON
// string; both are normalized to a json.RawMessage holding the object form.
type ToolCallRequest struct {
	ID   string
	Name string
	Args json.RawMessage
}

// Usage echoes whatever token accounting the provider returned. Both
// backends report input + output token counts.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Canonical finish-reason constants. Each provider's native value is mapped
// onto exactly one of these by its Chat() implementation.
//
//   - FinishStop      — model produced a final answer (Anthropic "end_turn",
//     OpenAI "stop").
//   - FinishToolCalls — model wants the caller to execute one or more tools
//     (Anthropic "tool_use", OpenAI "tool_calls").
//   - FinishLength    — output truncated at MaxTokens (both backends "length"
//     for OpenAI; Anthropic "max_tokens").
//   - FinishError     — provider call failed (transport / auth / 5xx); the
//     returned ChatResponse may still carry a partial Content slice.
const (
	FinishStop      = "stop"
	FinishToolCalls = "tool_calls"
	FinishLength    = "length"
	FinishError     = "error"
)
