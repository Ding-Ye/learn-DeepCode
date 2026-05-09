// Package main — s06-tool-capable-runner.
//
// File: main.go — CLI demo. Wires up an `echo` tool, a 3-round
// ReplayProvider (ask-for-tool → tool-result-arrives → final-answer), and
// runs Runner.Run. Prints the transcript so the reader can see exactly
// which messages the loop appended.
//
// No network, no API key — the replay is hardcoded. Run:
//
//	go run .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// echoTool is the smallest possible Tool: it returns the JSON-decoded
// "text" field from its args. Same shape as s02's echo tool, redeclared
// here per the session-isolation rule.
type echoTool struct{}

func (echoTool) Name() string { return "echo" }

func (echoTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "echo",
		Description: "Echo a string back unchanged.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}
}

func (echoTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("decode args: %w", err)
	}
	return p.Text, nil
}

func main() {
	reg := NewRegistry()
	reg.Register(echoTool{})

	// Three-round replay:
	//   round 1 — model asks to call echo("hello, agent")
	//   round 2 — model receives tool_result, asks for echo("world!")
	//   round 3 — model gives final text
	//
	// In a real run rounds 2 and 3 would arrive over HTTP; here they are
	// canned so the demo is hermetic.
	provider := &ReplayProvider{
		Responses: []ChatResponse{
			{
				FinishReason: FinishToolCalls,
				ToolCalls: []ToolCallRequest{{
					ID:   "call_1",
					Name: "echo",
					Args: json.RawMessage(`{"text":"hello, agent"}`),
				}},
			},
			{
				FinishReason: FinishToolCalls,
				ToolCalls: []ToolCallRequest{{
					ID:   "call_2",
					Name: "echo",
					Args: json.RawMessage(`{"text":"world!"}`),
				}},
			},
			{
				FinishReason: FinishStop,
				Content: []ContentBlock{{
					Type: "text",
					Text: "Done. The echo tool returned 'hello, agent' then 'world!'.",
				}},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runner := NewRunner(provider)
	res, err := runner.Run(ctx, RunSpec{
		InitialMessages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "please call echo twice"}}},
		},
		Tools:         reg,
		Model:         "fake-model",
		MaxIterations: 5,
		MaxToolBytes:  1024,
		MaxTokens:     1024,
	})
	if err != nil {
		log.Fatalf("run: %v", err)
	}

	fmt.Printf("stop_reason: %s\n", res.StopReason)
	fmt.Printf("iterations:  %d\n", res.Iterations)
	fmt.Printf("transcript (%d messages):\n", len(res.AllMessages))
	for i, m := range res.AllMessages {
		fmt.Printf("  [%d] %s\n", i, formatMessage(m))
	}
}

func formatMessage(m Message) string {
	parts := make([]string, 0, len(m.Content)+1)
	parts = append(parts, m.Role)
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			parts = append(parts, fmt.Sprintf("text=%q", b.Text))
		case "tool_use":
			parts = append(parts, fmt.Sprintf("tool_use(%s, id=%s, args=%s)", b.ToolName, b.ToolUseID, string(b.Input)))
		case "tool_result":
			tag := ""
			if b.IsError {
				tag = " ERR"
			}
			parts = append(parts, fmt.Sprintf("tool_result(id=%s%s)=%q", b.ToolUseID, tag, b.Output))
		}
	}
	return strings.Join(parts, " ")
}
