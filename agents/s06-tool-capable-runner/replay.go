// Package main — s06-tool-capable-runner.
//
// File: replay.go — ReplayProvider, a deterministic Provider for tests and
// the CLI demo. It consumes a slice of pre-built ChatResponse values, one
// per Chat() call, in order. After the queue empties it returns an error.
//
// This is the same pattern s10 will use to test the workflow without a real
// LLM. Real providers (Anthropic / OpenAI) live in s04.
package main

import (
	"context"
	"errors"
)

// ReplayProvider returns canned responses in order. Each call to Chat pops
// one ChatResponse off Responses; the index advances monotonically.
type ReplayProvider struct {
	Responses []ChatResponse
	calls     int
}

// Chat returns the next queued response, or an error if the queue is empty.
// The ctx and req are ignored — recording them is the test's job.
func (p *ReplayProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	if p.calls >= len(p.Responses) {
		return ChatResponse{}, errors.New("ReplayProvider: queue empty")
	}
	r := p.Responses[p.calls]
	p.calls++
	return r, nil
}

// Calls returns how many times Chat has been invoked. Useful for asserting
// the loop actually iterated the expected number of times.
func (p *ReplayProvider) Calls() int { return p.calls }
