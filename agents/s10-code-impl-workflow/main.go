// Package main — s10-code-impl-workflow.
//
// File: main.go — CLI demo. Wires up a ReplayProvider that reads recorded
// multi-file conversation from testdata, drives Workflow.Run, prints the
// final RunReport.
//
// Run:
//
//	go run . -plan testdata/plan_minimal.yaml -task-dir /tmp/s10-demo
//
// No network, no API key — the replay is hardcoded JSON. That mirrors the
// pattern every chapter from s06 onward uses.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

// ReplayProvider returns canned responses in order. Same shape as s06's.
// A call to Chat pops one ChatResponse off Responses; the index advances
// monotonically. After the queue empties Chat returns an error.
type ReplayProvider struct {
	Responses []ChatResponse
	calls     int
}

// Chat returns the next queued response, or an error if the queue is empty.
func (p *ReplayProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	if p.calls >= len(p.Responses) {
		return ChatResponse{}, errors.New("ReplayProvider: queue empty")
	}
	r := p.Responses[p.calls]
	p.calls++
	return r, nil
}

// Calls returns how many Chat invocations have been served.
func (p *ReplayProvider) Calls() int { return p.calls }

// loadReplay parses a JSON file shaped as []chatResponseDTO into the
// ChatResponse slice ReplayProvider expects.
func loadReplay(path string) ([]ChatResponse, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var dto []chatResponseDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return nil, err
	}
	out := make([]ChatResponse, len(dto))
	for i, d := range dto {
		out[i] = d.toChatResponse()
	}
	return out, nil
}

// chatResponseDTO is the JSON shape for the replay fixture. Flatter than
// ChatResponse so fixture authors don't have to know about Go field tags.
type chatResponseDTO struct {
	FinishReason string `json:"finish_reason"`
	Content      []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	ToolCalls []struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"tool_calls,omitempty"`
}

func (d chatResponseDTO) toChatResponse() ChatResponse {
	out := ChatResponse{FinishReason: d.FinishReason}
	for _, c := range d.Content {
		out.Content = append(out.Content, ContentBlock{Type: c.Type, Text: c.Text})
	}
	for _, t := range d.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallRequest{ID: t.ID, Name: t.Name, Args: t.Args})
	}
	return out
}

func main() {
	var planPath, taskDir, replayPath string
	flag.StringVar(&planPath, "plan", "testdata/plan_minimal.yaml", "path to plan file")
	flag.StringVar(&taskDir, "task-dir", "", "task working directory (default: temp dir)")
	flag.StringVar(&replayPath, "replay", "testdata/replay_three_files.json", "path to replay JSON")
	flag.Parse()

	if taskDir == "" {
		td, err := os.MkdirTemp("", "s10-demo-")
		if err != nil {
			log.Fatalf("mkdtemp: %v", err)
		}
		taskDir = td
	}

	responses, err := loadReplay(replayPath)
	if err != nil {
		log.Fatalf("load replay: %v", err)
	}

	wf := &Workflow{
		Provider:      &ReplayProvider{Responses: responses},
		Model:         "fake-model",
		MaxIterations: 8,
		MaxToolBytes:  4096,
	}

	report, err := wf.Run(context.Background(), planPath, taskDir)
	if err != nil {
		log.Fatalf("workflow: %v", err)
	}

	fmt.Printf("status:           %s\n", report.Status)
	fmt.Printf("reason:           %s\n", report.Reason)
	fmt.Printf("files_completed:  %d/%d\n", report.FilesCompleted, report.Total)
	fmt.Printf("iterations:       %d\n", report.Iterations)
	fmt.Printf("elapsed:          %s\n", report.Elapsed.Round(time.Millisecond))
	fmt.Printf("task_dir:         %s\n", taskDir)
	if len(report.UnimplementedFiles) > 0 {
		fmt.Printf("unimplemented:    %v\n", report.UnimplementedFiles)
	}
}
