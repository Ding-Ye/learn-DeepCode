// Package main — s06-tool-capable-runner.
//
// File: runner.go — the agent loop. One Provider, one Registry, one for-loop
// with three branches: tool-call → dispatch + continue; final-text → return
// StopDone; iteration cap → return StopMaxIterations with a synthetic
// apology message.
//
// Mirrors core/agent_runtime/runner.py:69-400. Out of scope (deferred to
// upstream stretch goal): empty-content retries, length-recovery cycles,
// orphan tool_result repair, micro-compaction, hooks, injection callbacks,
// streaming, finalization retry.
package main

import (
	"context"
	"errors"
	"fmt"
)

// Runner executes one tool-capable agent loop. Stateless beyond the Provider
// reference — every Run call is independent.
type Runner struct {
	Provider Provider
}

// NewRunner returns a Runner bound to p.
func NewRunner(p Provider) *Runner {
	return &Runner{Provider: p}
}

// defaultMaxIterationsMessage is upstream's
// _DEFAULT_MAX_ITERATIONS_MESSAGE template. We render the integer in Go
// rather than Jinja2.
const defaultMaxIterationsMessage = "I reached the maximum number of tool call iterations (%d) " +
	"without completing the task. You can try breaking the task into smaller steps."

// Run drives the agent loop until the model emits a final text answer or
// MaxIterations is reached.
//
// Algorithm:
//
//  1. Copy spec.InitialMessages into a working slice. Caller's slice is
//     never mutated.
//
//  2. Loop i = 0..MaxIterations-1:
//
//     a. Provider.Chat with the current messages and the registry's
//     schema list.
//
//     b. If the response carries tool_calls: append the assistant turn
//     (text blocks + tool_use blocks), dispatch each call via the
//     registry, append a single user message containing one tool_result
//     ContentBlock per call (matches upstream's "all results in one
//     turn" pattern), continue to next iteration.
//
//     c. Otherwise treat the response as final text: build an assistant
//     message from response.Content and return RunResult{StopDone, ...}.
//
//  3. If the loop runs out of iterations: append a synthetic assistant
//     message with the apology template and return StopMaxIterations.
//
// Errors from Provider.Chat are returned as RunResult{StopError, ...} plus
// the error itself. Tool errors are NOT propagated — they become tool_result
// blocks with IsError=true so the model can react.
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if r.Provider == nil {
		return RunResult{StopReason: StopError}, errors.New("runner: Provider is nil")
	}
	if spec.MaxIterations <= 0 {
		return RunResult{StopReason: StopError}, errors.New("runner: MaxIterations must be > 0")
	}

	// Copy so callers can reuse their slice without surprises.
	messages := make([]Message, len(spec.InitialMessages))
	copy(messages, spec.InitialMessages)

	var schemas []ToolSchema
	if spec.Tools != nil {
		schemas = spec.Tools.List()
	}

	for i := 0; i < spec.MaxIterations; i++ {
		req := ChatRequest{
			Model:       spec.Model,
			Messages:    messages,
			Tools:       schemas,
			MaxTokens:   spec.MaxTokens,
			Temperature: spec.Temperature,
		}
		resp, err := r.Provider.Chat(ctx, req)
		if err != nil {
			return RunResult{
				AllMessages: messages,
				StopReason:  StopError,
				Iterations:  i + 1,
			}, err
		}

		// Branch 1: model wants to call tools.
		if len(resp.ToolCalls) > 0 {
			assistant := Message{Role: "assistant", Content: resp.Content}
			// Some providers emit ToolCalls without echoing tool_use blocks
			// in Content (OpenAI). Make sure the assistant turn carries the
			// tool_use blocks for transcript fidelity.
			assistant.Content = ensureToolUseBlocks(assistant.Content, resp.ToolCalls)
			messages = append(messages, assistant)

			toolResults := make([]ContentBlock, 0, len(resp.ToolCalls))
			for _, call := range resp.ToolCalls {
				toolResults = append(toolResults, dispatchToolCall(ctx, spec.Tools, call, spec.MaxToolBytes))
			}
			// Anthropic-style: tool_result blocks live on a user message.
			messages = append(messages, Message{Role: "user", Content: toolResults})
			continue
		}

		// Branch 2: final text. Whatever's in Content is the answer.
		final := Message{Role: "assistant", Content: resp.Content}
		messages = append(messages, final)
		return RunResult{
			FinalMessage: final,
			AllMessages:  messages,
			StopReason:   StopDone,
			Iterations:   i + 1,
		}, nil
	}

	// Branch 3: hit the cap.
	apology := Message{
		Role: "assistant",
		Content: []ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf(defaultMaxIterationsMessage, spec.MaxIterations),
		}},
	}
	messages = append(messages, apology)
	return RunResult{
		FinalMessage: apology,
		AllMessages:  messages,
		StopReason:   StopMaxIterations,
		Iterations:   spec.MaxIterations,
	}, nil
}

// ensureToolUseBlocks guarantees the assistant Content slice carries one
// "tool_use" block per ToolCallRequest. Anthropic providers already include
// them; OpenAI providers may not (they live in a sibling field). The order
// must match calls so tool_use_id pairing works.
func ensureToolUseBlocks(content []ContentBlock, calls []ToolCallRequest) []ContentBlock {
	have := make(map[string]bool, len(content))
	for _, b := range content {
		if b.Type == "tool_use" && b.ToolUseID != "" {
			have[b.ToolUseID] = true
		}
	}
	out := make([]ContentBlock, 0, len(content)+len(calls))
	out = append(out, content...)
	for _, c := range calls {
		if have[c.ID] {
			continue
		}
		out = append(out, ContentBlock{
			Type:      "tool_use",
			ToolUseID: c.ID,
			ToolName:  c.Name,
			Input:     c.Args,
		})
	}
	return out
}
