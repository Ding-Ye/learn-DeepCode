// Package main — s09-memory-compaction.
//
// File: main.go — a tiny CLI demo that reads testdata/long_conversation.json
// (a recorded ~50-message run) and shows the before/after of one Compact()
// call. Run: `go run .` from the agents/s09-memory-compaction directory.
//
// The fixture is JSON-encoded with a flatter shape than ContentBlock —
// the DTO below maps onto our internal types without forcing fixture
// authors to know about Go-style field tags. Keeps the demo and tests
// reading from the same file.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type fixtureBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Input     string `json:"input,omitempty"`
	Output    string `json:"output,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type fixtureMessage struct {
	Role    string         `json:"role"`
	Content []fixtureBlock `json:"content"`
}

func loadFixture(path string) ([]Message, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var msgs []fixtureMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, err
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		blocks := make([]ContentBlock, len(m.Content))
		for j, b := range m.Content {
			blocks[j] = ContentBlock{
				Type:      b.Type,
				Text:      b.Text,
				ToolUseID: b.ToolUseID,
				ToolName:  b.ToolName,
				Input:     b.Input,
				Output:    b.Output,
				IsError:   b.IsError,
			}
		}
		out[i] = Message{Role: m.Role, Content: blocks}
	}
	return out, nil
}

func main() {
	path := "testdata/long_conversation.json"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	msgs, err := loadFixture(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load %s: %v\n", path, err)
		os.Exit(1)
	}

	agent := &MemoryAgent{
		InitialPlan: "PLAN: implement files A, B, C with tests.",
	}

	before := MessagesTokens(agent.Tokenizer, msgs)
	out := agent.Compact(msgs)
	after := MessagesTokens(agent.Tokenizer, out)

	fmt.Println("MemoryAgent.Compact demo")
	fmt.Println("------------------------")
	fmt.Printf("input  messages: %3d  est-tokens: %6d\n", len(msgs), before)
	fmt.Printf("output messages: %3d  est-tokens: %6d  (kept system + plan + last write_file round)\n", len(out), after)
	if before > 0 {
		fmt.Printf("compaction ratio: %.1f%%\n", 100*float64(after)/float64(before))
	}
}
