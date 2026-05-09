// Package main — s02-tool-registry.
//
// Tool interface + ToolSchema. Every tool an agent can call (s06+) implements
// Tool; the registry stores them by name and caches the JSON-Schema list it
// hands to the LLM.
//
// Upstream counterpart: core/agent_runtime/tools/base.py (Tool ABC) +
// core/agent_runtime/tools/registry.py (ToolRegistry class).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Tool is the contract every callable an agent can invoke must satisfy.
//
// The registry stores Tools by Name(); the LLM sees them by Schema(); the
// runner (s06) invokes them via Run().
type Tool interface {
	// Name is the canonical identifier the LLM uses.
	// Tools whose name starts with "mcp_" are sorted to the end of List() —
	// matches upstream's ordering convention.
	Name() string

	// Schema returns the JSON-Schema description (draft 2020-12) the LLM
	// consumes. Must be stable: the registry caches the slice and only
	// invalidates it when Register / Unregister is called.
	Schema() ToolSchema

	// Run executes the tool. ctx carries cancellation; args is the raw JSON
	// argument object exactly as the LLM produced it.
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// ToolSchema is what gets serialized into the request body's `tools` array.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// CloserTool is an optional extension. Tools that hold subprocess /
// file-handle resources (real MCP stdio servers in a future chapter)
// implement io.Closer; the registry's Close() walks every registered
// CloserTool and aggregates errors.
//
// We model it via the standard io.Closer rather than inventing a new
// interface — keeps the surface area small.
type CloserTool interface {
	Tool
	io.Closer
}

// ErrToolNotFound is returned by Get when the name is unknown.
var ErrToolNotFound = errors.New("tool not found")
