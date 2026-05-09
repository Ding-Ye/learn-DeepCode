// Package main — s10-code-impl-workflow.
//
// File: runner.go — a stripped s06 runner with one extra responsibility:
// before dispatching each tool, ask the LoopDetector whether to abort. If
// the detector returns a non-OK status, the runner stops with
// StopReason="aborted" and surfaces the detector's Message in AbortReason
// so the workflow can populate RunReport.Reason without a second lookup.
//
// Compared to s06 we drop:
//   - empty-content retries, length-recovery cycles, micro-compaction
//   - the fancy ensureToolUseBlocks shim — s10's replay providers always
//     emit tool_use blocks in Content, so the simpler form suffices.
//
// What we add:
//   - LoopDetector pre-tool gate (`detector.CheckTool(name)`)
//   - StopAborted return path with AbortReason
//
// Mirrors core/agent_runtime/runner.py:69-400 plus the loop-detector
// integration in code_implementation_workflow.py:507-524.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// StopReason values returned by Runner.Run.
const (
	StopDone          = "done"
	StopMaxIterations = "max_iterations"
	StopAborted       = "aborted"
	StopError         = "error"
)

// RunSpec is the input for one runner execution.
type RunSpec struct {
	InitialMessages []Message
	Tools           *Registry
	Provider        Provider
	Detector        *LoopDetector
	Model           string
	MaxIterations   int
	MaxToolBytes    int
	MaxTokens       int
	Temperature     float64

	// OnToolResult is an optional callback fired after each tool run. The
	// workflow uses it to detect successful write_file events without
	// reaching back into the runner's transcript.
	OnToolResult func(name string, args json.RawMessage, result string, isError bool)
}

// RunResult is the output.
type RunResult struct {
	FinalMessage Message
	AllMessages  []Message
	StopReason   string
	AbortReason  string
	Iterations   int
}

// defaultMaxIterationsMessage matches s06 / upstream's apology template.
const defaultMaxIterationsMessage = "I reached the maximum number of tool call iterations (%d) " +
	"without completing the task. You can try breaking the task into smaller steps."

// Runner executes one tool-capable agent loop with loop-detector gating.
type Runner struct {
	Provider Provider
	Detector *LoopDetector
}

// NewRunner returns a Runner bound to (provider, detector). Either may be
// nil at construction time and overridden via RunSpec, but Run requires
// both to be set on either field by the time it runs.
func NewRunner(p Provider, d *LoopDetector) *Runner {
	return &Runner{Provider: p, Detector: d}
}

// Run drives the agent loop. The algorithm is:
//
//  1. Each iteration: provider.Chat with current messages.
//  2. If the response carries tool_calls: for each call, ask the detector
//     should we keep going? If not — stop with StopAborted + reason. Else
//     dispatch the tool, append the tool_result block, optionally invoke
//     OnToolResult, continue.
//  3. If the response is final text: stop with StopDone.
//  4. If MaxIterations is hit: stop with StopMaxIterations + apology.
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	provider := spec.Provider
	if provider == nil {
		provider = r.Provider
	}
	if provider == nil {
		return RunResult{StopReason: StopError}, errors.New("runner: Provider is nil")
	}
	detector := spec.Detector
	if detector == nil {
		detector = r.Detector
	}
	if spec.MaxIterations <= 0 {
		return RunResult{StopReason: StopError}, errors.New("runner: MaxIterations must be > 0")
	}

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
		resp, err := provider.Chat(ctx, req)
		if err != nil {
			return RunResult{
				AllMessages: messages,
				StopReason:  StopError,
				Iterations:  i + 1,
			}, err
		}

		if len(resp.ToolCalls) > 0 {
			// Pre-tool gate: if the detector says abort on ANY of the
			// queued calls, stop the whole run before dispatching.
			if detector != nil {
				for _, call := range resp.ToolCalls {
					if status := detector.CheckTool(call.Name); status.ShouldStop {
						return RunResult{
							AllMessages: messages,
							StopReason:  StopAborted,
							AbortReason: status.Message,
							Iterations:  i + 1,
						}, nil
					}
				}
			}

			assistant := Message{Role: "assistant", Content: resp.Content}
			messages = append(messages, assistant)

			toolResults := make([]ContentBlock, 0, len(resp.ToolCalls))
			for _, call := range resp.ToolCalls {
				block := dispatchToolCall(ctx, spec.Tools, call, spec.MaxToolBytes)
				toolResults = append(toolResults, block)
				if spec.OnToolResult != nil {
					spec.OnToolResult(call.Name, call.Args, block.Output, block.IsError)
				}
				if detector != nil {
					if block.IsError {
						detector.RecordError()
					} else if call.Name == "write_file" {
						// Treat a successful write_file as the canonical
						// progress signal — same as upstream's
						// `loop_detector.record_success()` after a
						// non-error tool result.
						detector.RecordSuccess()
					}
				}
			}
			messages = append(messages, Message{Role: "user", Content: toolResults})
			continue
		}

		final := Message{Role: "assistant", Content: resp.Content}
		messages = append(messages, final)
		return RunResult{
			FinalMessage: final,
			AllMessages:  messages,
			StopReason:   StopDone,
			Iterations:   i + 1,
		}, nil
	}

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

// dispatchToolCall is the same pattern as s06's: registry lookup, run, wrap
// errors as is_error tool_result blocks, truncate oversized output.
func dispatchToolCall(ctx context.Context, reg *Registry, call ToolCallRequest, maxBytes int) ContentBlock {
	if reg == nil {
		return ContentBlock{
			Type: "tool_result", ToolUseID: call.ID,
			Output:  fmt.Sprintf("tool %q called but no registry is configured", call.Name),
			IsError: true,
		}
	}
	tool, ok := reg.Get(call.Name)
	if !ok {
		return ContentBlock{
			Type: "tool_result", ToolUseID: call.ID, ToolName: call.Name,
			Output:  fmt.Sprintf("tool %q not found", call.Name),
			IsError: true,
		}
	}
	out, err := tool.Run(ctx, call.Args)
	if err != nil {
		return ContentBlock{
			Type: "tool_result", ToolUseID: call.ID, ToolName: call.Name,
			Output:  fmt.Sprintf("tool %q failed: %v", call.Name, err),
			IsError: true,
		}
	}
	return ContentBlock{
		Type: "tool_result", ToolUseID: call.ID, ToolName: call.Name,
		Output: truncate(out, maxBytes),
	}
}

const truncationMarker = "… [truncated]"

func truncate(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	if maxBytes <= len(truncationMarker) {
		return truncationMarker
	}
	keep := maxBytes - len(truncationMarker)
	return s[:keep] + truncationMarker
}
