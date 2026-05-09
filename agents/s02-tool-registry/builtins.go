package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EchoTool — trivial demo tool. Returns its input verbatim.
type EchoTool struct{}

// NewEchoTool returns the singleton-ish demo tool.
func NewEchoTool() Tool { return &EchoTool{} }

func (e *EchoTool) Name() string { return "echo" }

func (e *EchoTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "echo",
		Description: "Echoes the input string back unchanged.",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "text": {"type": "string", "description": "The string to echo."}
  },
  "required": ["text"]
}`),
	}
}

func (e *EchoTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	return p.Text, nil
}

// NowTool — returns the current UTC time in RFC3339.
// Demonstrates a tool with no input arguments.
type NowTool struct{}

// NewNowTool returns the demo tool.
func NewNowTool() Tool { return &NowTool{} }

func (n *NowTool) Name() string { return "now" }

func (n *NowTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "now",
		Description: "Returns the current UTC time as RFC3339.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
	}
}

func (n *NowTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return time.Now().UTC().Format(time.RFC3339), nil
}

// MCPSubprocessTool simulates an MCP-server-backed tool: it implements
// CloserTool so we can exercise the registry's lifecycle ownership without
// actually spawning a subprocess. (Real MCP stdio framing is left as
// Appendix B exercise #5.)
type MCPSubprocessTool struct {
	name      string
	closed    bool
	CloseHook func() error // injected by tests
}

// NewMCPSubprocessTool creates a fake MCP tool with the conventional
// "mcp_" prefix so we can verify registry sort order.
func NewMCPSubprocessTool(suffix string) *MCPSubprocessTool {
	return &MCPSubprocessTool{name: "mcp_" + suffix}
}

func (m *MCPSubprocessTool) Name() string { return m.name }

func (m *MCPSubprocessTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        m.name,
		Description: "Simulated MCP-backed tool (no actual subprocess).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
	}
}

func (m *MCPSubprocessTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	if m.closed {
		return "", fmt.Errorf("%s: closed", m.name)
	}
	return "ok-from-" + m.name, nil
}

// Close marks the tool closed (and runs an injected hook for tests).
func (m *MCPSubprocessTool) Close() error {
	m.closed = true
	if m.CloseHook != nil {
		return m.CloseHook()
	}
	return nil
}
